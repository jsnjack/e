package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jsnjack/e/internal/config"
)

func TestLocalProviderDoesNotReadKeyring(t *testing.T) {
	SetUserAgent("E/test")
	t.Cleanup(func() { SetUserAgent("E") })
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("User-Agent"); got != "E/test" {
			t.Errorf("User-Agent = %q, want E/test", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		var body string
		switch request.URL.Path {
		case "/v1/models":
			body = `{"data":[]}`
		case "/v1/chat/completions":
			body = `{"choices":[{"message":{"content":"done"}}]}`
		default:
			http.NotFound(writer, request)
			return
		}
		if _, err := fmt.Fprint(writer, body); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	var keyReads atomic.Int32
	providers, err := NewProviders([]config.Model{{Name: "local", Provider: "openai", Endpoint: server.URL + "/v1"}}, func(_, _ string) string {
		keyReads.Add(1)
		return "unused"
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := providers[0].Complete(context.Background(), Request{Prefix: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "done" {
		t.Fatalf("completion = %q, want done", result)
	}
	if got := keyReads.Load(); got != 0 {
		t.Fatalf("local provider read keyring %d times", got)
	}
}

func TestProviderRequestModes(t *testing.T) {
	tests := []struct {
		name         string
		protocol     string
		request      Request
		wantPath     string
		wantPayload  string
		responseBody string
		wantResult   string
	}{
		{
			name: "chat completion", protocol: "chat", request: Request{Prefix: "before", Suffix: "after"},
			wantPath: "/v1/chat/completions", wantPayload: "<CURSOR>",
			responseBody: `{"choices":[{"message":{"content":"inserted"}}]}`, wantResult: "inserted",
		},
		{
			name: "qwen fim", protocol: "fim-qwen", request: Request{Prefix: "before", Suffix: "after"},
			wantPath: "/v1/completions", wantPayload: "<|fim_middle|>",
			responseBody: `{"choices":[{"text":"inserted"}]}`, wantResult: "inserted",
		},
		{
			name: "selection transform", protocol: "fim-qwen", request: Request{Instruction: "uppercase", Selection: "word"},
			wantPath: "/v1/chat/completions", wantPayload: "uppercase",
			responseBody: `{"choices":[{"message":{"content":"WORD"}}]}`, wantResult: "WORD",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotPath string
			var gotPayload map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				gotPath = request.URL.Path
				if err := json.NewDecoder(request.Body).Decode(&gotPayload); err != nil {
					t.Errorf("decode request: %v", err)
				}
				writer.Header().Set("Content-Type", "application/json")
				if _, err := fmt.Fprint(writer, test.responseBody); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			defer server.Close()
			providers, err := NewProviders([]config.Model{{
				Name: "test", Provider: "openai", Endpoint: server.URL + "/v1", Protocol: test.protocol,
			}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := providers[0].Complete(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			if result != test.wantResult || gotPath != test.wantPath {
				t.Fatalf("result/path = %q %q, want %q %q", result, gotPath, test.wantResult, test.wantPath)
			}
			decoded := fmt.Sprint(gotPayload)
			if !strings.Contains(decoded, test.wantPayload) {
				t.Fatalf("payload %s does not contain %q", decoded, test.wantPayload)
			}
		})
	}
}

func TestValidateEndpoint(t *testing.T) {
	tests := []struct {
		name, endpoint string
		allow          bool
		wantError      bool
	}{
		{name: "https", endpoint: "https://models.example/v1"},
		{name: "localhost", endpoint: "http://localhost:8000/v1"},
		{name: "ipv4 loopback", endpoint: "http://127.0.0.1:8000/v1"},
		{name: "ipv6 loopback", endpoint: "http://[::1]:8000/v1"},
		{name: "allowed remote http", endpoint: "http://models.example/v1", allow: true},
		{name: "remote http", endpoint: "http://models.example/v1", wantError: true},
		{name: "credentials", endpoint: "https://secret@models.example/v1", wantError: true},
		{name: "wrong scheme", endpoint: "file:///tmp/model", wantError: true},
		{name: "relative", endpoint: "/v1", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateEndpoint(test.endpoint, test.allow)
			if (err != nil) != test.wantError {
				t.Fatalf("validateEndpoint() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}
