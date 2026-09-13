package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jsnjack/e/internal/config"
)

const (
	remoteCompletionTimeout = 8 * time.Second
	localCompletionTimeout  = 30 * time.Second
)

// KeyFunc supplies the API key for an endpoint.
type KeyFunc func(provider, endpoint string) string

type openAIProvider struct {
	client  *http.Client
	model   config.Model
	apiKey  string
	keyFor  KeyFunc
	keyOnce sync.Once
}

var configuredUserAgent atomic.Value

// SetUserAgent identifies E and its release to model servers.
func SetUserAgent(value string) { configuredUserAgent.Store(strings.TrimSpace(value)) }

func userAgent() string {
	if value, ok := configuredUserAgent.Load().(string); ok && value != "" {
		return value
	}
	return "E"
}

// NewProviders validates model entries and constructs their providers.
func NewProviders(models []config.Model, keyFor KeyFunc) ([]Provider, error) {
	providers := make([]Provider, 0, len(models))
	for _, model := range models {
		if model.Provider != "openai" && model.Provider != "litellm" {
			return nil, fmt.Errorf("unsupported autocomplete provider %q", model.Provider)
		}
		if err := validateEndpoint(model.Endpoint, model.AllowInsecureHTTP); err != nil {
			return nil, err
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{Timeout: 700 * time.Millisecond, KeepAlive: 30 * time.Second}).DialContext
		providers = append(providers, &openAIProvider{
			client: &http.Client{Timeout: completionTimeout(model.Endpoint), Transport: transport},
			model:  model, keyFor: keyFor,
		})
	}
	return providers, nil
}

func completionTimeout(endpoint string) time.Duration {
	if endpointIsLoopback(endpoint) {
		return localCompletionTimeout
	}
	return remoteCompletionTimeout
}

func (p *openAIProvider) Label() string { return p.model.Name }

func (p *openAIProvider) Healthy(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(p.model.Endpoint, "/")+"/models", nil)
	if err != nil {
		return fmt.Errorf("build availability request: %w", err)
	}
	p.authorize(req)
	req.Header.Set("User-Agent", userAgent())
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("check availability: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Debug("close availability response", "model", p.model.Name, "err", err)
		}
	}()
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); err != nil {
		return fmt.Errorf("read availability response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("availability returned %s", resp.Status)
	}
	return nil
}

func (p *openAIProvider) Complete(ctx context.Context, request Request) (string, error) {
	var endpoint string
	var payload map[string]any
	if request.Instruction != "" {
		endpoint = "/chat/completions"
		payload = map[string]any{
			"model": p.model.Name, "stream": false, "temperature": 0.1, "max_tokens": 2048,
			"reasoning_effort":     "none",
			"chat_template_kwargs": map[string]any{"enable_thinking": false},
			"messages": []map[string]string{
				{"role": "system", "content": "Transform the selected text exactly as requested. Return only the complete replacement text. Preserve formatting unless asked to change it. Do not explain and do not use Markdown fences."},
				{"role": "user", "content": transformPrompt(request)},
			},
		}
	} else {
		switch p.model.Protocol {
		case "fim-qwen":
			endpoint = "/completions"
			payload = map[string]any{
				"model": p.model.Name, "stream": false, "temperature": 0.1, "max_tokens": 96,
				"prompt": "<|fim_prefix|>" + request.Prefix + "<|fim_suffix|>" + request.Suffix + "<|fim_middle|>",
				"stop":   []string{"<|fim_prefix|>", "<|fim_suffix|>", "<|fim_middle|>", "<|endoftext|>"},
			}
		default:
			endpoint = "/chat/completions"
			payload = map[string]any{
				"model": p.model.Name, "stream": false, "temperature": 0.1, "max_tokens": 96,
				"reasoning_effort":     "none",
				"chat_template_kwargs": map[string]any{"enable_thinking": false},
				"messages": []map[string]string{
					{"role": "system", "content": "Complete the text at <CURSOR>. Return only the exact text to insert. Preserve style and indentation. Do not explain and do not use Markdown fences."},
					{"role": "user", "content": completionPrompt(request)},
				},
			}
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode completion request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(p.model.Endpoint, "/")+endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build completion request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent())
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request completion: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Debug("close completion response", "model", p.model.Name, "err", err)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", fmt.Errorf("read completion: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("completion returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var decoded struct {
		Choices []struct {
			Text    string `json:"text"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", fmt.Errorf("decode completion: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("completion contained no choices")
	}
	if decoded.Choices[0].Text != "" {
		return decoded.Choices[0].Text, nil
	}
	return decoded.Choices[0].Message.Content, nil
}

func (p *openAIProvider) authorize(req *http.Request) {
	p.keyOnce.Do(func() {
		if p.keyFor != nil && !endpointIsLoopback(p.model.Endpoint) {
			p.apiKey = p.keyFor(p.model.Provider, p.model.Endpoint)
		}
	})
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

func endpointIsLoopback(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := strings.Trim(strings.ToLower(u.Hostname()), "[]")
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}

func completionPrompt(request Request) string {
	return "File: " + request.Filename + "\nLanguage: " + request.Language + "\n<PREFIX>\n" + request.Prefix + "<CURSOR><SUFFIX>\n" + request.Suffix + "\n</SUFFIX>"
}

func transformPrompt(request Request) string {
	return "File: " + request.Filename + "\nLanguage: " + request.Language + "\nInstruction: " + request.Instruction + "\n<SELECTION>\n" + request.Selection + "\n</SELECTION>"
}

func validateEndpoint(endpoint string, allowInsecure bool) error {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || !u.IsAbs() || u.Hostname() == "" {
		return fmt.Errorf("AI endpoint must be an absolute HTTP(S) URL")
	}
	if u.User != nil {
		return fmt.Errorf("AI endpoint must not contain credentials")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme != "http" {
		return fmt.Errorf("AI endpoint must use HTTP or HTTPS")
	}
	host := strings.Trim(strings.ToLower(u.Hostname()), "[]")
	ip := net.ParseIP(host)
	if host == "localhost" || ip != nil && ip.IsLoopback() || allowInsecure {
		return nil
	}
	return fmt.Errorf("AI endpoint %q uses insecure HTTP outside localhost", endpoint)
}
