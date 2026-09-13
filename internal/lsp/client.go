// Package lsp implements the dependency-free subset of the Language Server
// Protocol needed for completion and diagnostics.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Position identifies a UTF-16 line and character offset.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range identifies a document region.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Change is one incremental document update.
type Change struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

// Diagnostic is a language-server problem marker.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"`
}

// Completion is a language-server completion item.
type Completion struct {
	Label      string
	Detail     string
	InsertText string
	TextEdit   *struct {
		Range   Range  `json:"range"`
		NewText string `json:"newText"`
	}
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Client owns one language-server subprocess.
type Client struct {
	command string
	args    []string
	rootURI string

	cmd         *exec.Cmd
	stdin       io.WriteCloser
	mu          sync.Mutex
	nextID      atomic.Int64
	pendMu      sync.Mutex
	pending     map[int64]chan response
	diagnostics func(string, []Diagnostic)
	done        chan struct{}
}

// New constructs a client. Start must be called before document operations.
func New(command string, args []string, root string, diagnostics func(string, []Diagnostic)) *Client {
	rootURI := FileURI(root)
	return &Client{command: command, args: append([]string(nil), args...), rootURI: rootURI, pending: make(map[int64]chan response), diagnostics: diagnostics, done: make(chan struct{})}
}

// Start launches and initializes the language server.
func (c *Client) Start(ctx context.Context) error {
	if strings.TrimSpace(c.command) == "" {
		return fmt.Errorf("language-server command is empty")
	}
	c.cmd = exec.CommandContext(ctx, c.command, c.args...)
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open language-server stdout: %w", err)
	}
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open language-server stdin: %w", err)
	}
	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open language-server stderr: %w", err)
	}
	c.stdin = stdin
	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start language server %s: %w", c.command, err)
	}
	go c.readLoop(stdout)
	go c.logStderr(stderr)
	var initialized json.RawMessage
	if err := c.call(ctx, "initialize", map[string]any{
		"processId": osProcessID(), "rootUri": c.rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization": map[string]any{"didSave": true},
				"completion":      map[string]any{"completionItem": map[string]any{"snippetSupport": false}},
			},
		},
	}, &initialized); err != nil {
		return fmt.Errorf("initialize language server: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		return fmt.Errorf("notify initialized: %w", err)
	}
	return nil
}

// Open notifies the server of an opened document.
func (c *Client) Open(uri, language string, version int, text string) error {
	return c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": uri, "languageId": language, "version": version, "text": text,
	}})
}

// Change notifies the server of incremental document changes.
func (c *Client) Change(uri string, version int, changes []Change) error {
	return c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": changes,
	})
}

// Save notifies the server that a document was saved.
func (c *Client) Save(uri string) error {
	return c.notify("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}})
}

// CloseDocument notifies the server that a document closed.
func (c *Client) CloseDocument(uri string) error {
	return c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
}

// Completions requests semantic completion at position.
func (c *Client) Completions(ctx context.Context, uri string, position Position) ([]Completion, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": position,
	}, &raw); err != nil {
		return nil, err
	}
	return decodeCompletions(raw)
}

func decodeCompletions(raw json.RawMessage) ([]Completion, error) {
	var wire []struct {
		Label      string `json:"label"`
		Detail     string `json:"detail"`
		InsertText string `json:"insertText"`
		TextEdit   *struct {
			Range   Range  `json:"range"`
			NewText string `json:"newText"`
		} `json:"textEdit"`
	}
	if len(raw) > 0 && raw[0] == '{' {
		var list struct {
			Items json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decode completion list: %w", err)
		}
		raw = list.Items
	}
	if string(raw) == "null" || len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("decode completions: %w", err)
	}
	out := make([]Completion, 0, len(wire))
	for _, item := range wire {
		insert := item.InsertText
		if insert == "" {
			insert = item.Label
		}
		out = append(out, Completion{Label: item.Label, Detail: item.Detail, InsertText: insert, TextEdit: item.TextEdit})
	}
	return out, nil
}

// Shutdown asks the server to exit and waits for the subprocess.
func (c *Client) Shutdown(ctx context.Context) error {
	if c.cmd == nil {
		return nil
	}
	var ignored json.RawMessage
	callErr := c.call(ctx, "shutdown", nil, &ignored)
	notifyErr := c.notify("exit", nil)
	waitErr := c.cmd.Wait()
	return errorsJoin(callErr, notifyErr, waitErr)
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan response, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()
	defer func() {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
	}()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("language server exited")
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("language server error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if out != nil {
			if raw, ok := out.(*json.RawMessage); ok {
				*raw = append((*raw)[:0], resp.Result...)
				return nil
			}
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("decode %s response: %w", method, err)
			}
		}
		return nil
	}
}

func (c *Client) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) write(message any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode language-server message: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return fmt.Errorf("language server is not running")
	}
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return fmt.Errorf("write language-server header: %w", err)
	}
	if _, err := c.stdin.Write(body); err != nil {
		return fmt.Errorf("write language-server body: %w", err)
	}
	return nil
}

func (c *Client) readLoop(reader io.Reader) {
	defer close(c.done)
	buffered := bufio.NewReader(reader)
	for {
		body, err := readFrame(buffered)
		if err != nil {
			if err != io.EOF {
				slog.Debug("language server read stopped", "command", c.command, "err", err)
			}
			return
		}
		var message response
		if err := json.Unmarshal(body, &message); err != nil {
			slog.Debug("decode language-server message", "err", err)
			continue
		}
		if len(message.ID) > 0 {
			id, err := strconv.ParseInt(strings.Trim(string(message.ID), "\""), 10, 64)
			if err != nil {
				continue
			}
			c.pendMu.Lock()
			ch := c.pending[id]
			c.pendMu.Unlock()
			if ch != nil {
				ch <- message
			}
			continue
		}
		if message.Method == "textDocument/publishDiagnostics" && c.diagnostics != nil {
			var params struct {
				URI         string       `json:"uri"`
				Diagnostics []Diagnostic `json:"diagnostics"`
			}
			if err := json.Unmarshal(message.Params, &params); err == nil {
				c.diagnostics(params.URI, params.Diagnostics)
			}
		}
	}
}

func (c *Client) logStderr(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		slog.Debug("language server", "command", c.command, "message", scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		slog.Debug("read language-server stderr", "command", c.command, "err", err)
	}
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil || length < 0 {
				return nil, fmt.Errorf("invalid Content-Length %q", value)
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("language-server frame has no Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, fmt.Errorf("read language-server frame: %w", err)
	}
	return body, nil
}

// FileURI converts a local path to the URI form required by LSP.
func FileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

func osProcessID() int { return os.Getpid() }

func errorsJoin(errs ...error) error {
	var present []error
	for _, err := range errs {
		if err != nil {
			present = append(present, err)
		}
	}
	if len(present) == 0 {
		return nil
	}
	return fmt.Errorf("stop language server: %w", errors.Join(present...))
}
