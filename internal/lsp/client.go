// Package lsp implements the dependency-free subset of the Language Server
// Protocol needed for completion and diagnostics.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	SortText   string
	FilterText string
	Preselect  bool
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

const (
	defaultInitializeTimeout = 20 * time.Second
	defaultShutdownTimeout   = 2 * time.Second
	defaultExitTimeout       = 2 * time.Second
)

// timeouts bounds the lifecycle steps; tests shorten them per client.
type timeouts struct {
	initialize time.Duration
	shutdown   time.Duration
	exit       time.Duration
}

// Client owns one language-server subprocess.
type Client struct {
	command string
	args    []string
	rootURI string

	// stateMu orders Start against Close so a closed client never launches.
	stateMu sync.Mutex
	cmd     *exec.Cmd
	closed  bool

	stdin io.WriteCloser
	// queueMu guards an unbounded FIFO of outgoing messages. Requests,
	// notifications, and replies share it so the server sees them in order.
	queueMu     sync.Mutex
	queue       []outbound
	running     bool
	queueSignal chan struct{}

	nextID      atomic.Int64
	pendMu      sync.Mutex
	pending     map[int64]chan response
	diagnostics func(string, []Diagnostic)
	done        chan struct{}
	closeOnce   sync.Once
	waitOnce    sync.Once
	exited      chan struct{}
	timeouts    timeouts
}

type outbound struct {
	message any
	result  chan error
}

// New constructs a client. Start must be called before document operations.
func New(command string, args []string, root string, diagnostics func(string, []Diagnostic)) *Client {
	rootURI := FileURI(root)
	return &Client{
		command: command, args: append([]string(nil), args...), rootURI: rootURI,
		pending: make(map[int64]chan response), diagnostics: diagnostics,
		done: make(chan struct{}), queueSignal: make(chan struct{}, 1), exited: make(chan struct{}),
		timeouts: timeouts{defaultInitializeTimeout, defaultShutdownTimeout, defaultExitTimeout},
	}
}

// Start launches and initializes the language server. The process lives until
// ctx is cancelled or Close is called; initialize alone is bounded by a 20 s
// timeout. A failed initialize closes the client and reaps the process.
func (c *Client) Start(ctx context.Context) error {
	if strings.TrimSpace(c.command) == "" {
		return fmt.Errorf("language-server command is empty")
	}
	stdout, stderr, err := c.launch(ctx)
	if err != nil {
		return err
	}
	go c.writeLoop()
	go c.readLoop(stdout)
	go c.logStderr(stderr)
	initCtx, cancel := context.WithTimeout(ctx, c.timeouts.initialize)
	defer cancel()
	var initialized json.RawMessage
	if err := c.call(initCtx, "initialize", map[string]any{
		"processId": osProcessID(), "rootUri": c.rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization": map[string]any{"didSave": true},
				"completion":      map[string]any{"completionItem": map[string]any{"snippetSupport": false}},
			},
		},
	}, &initialized); err != nil {
		c.Close()
		return fmt.Errorf("initialize language server: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.Close()
		return fmt.Errorf("notify initialized: %w", err)
	}
	return nil
}

func (c *Client) launch(ctx context.Context) (io.Reader, io.Reader, error) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.closed {
		return nil, nil, fmt.Errorf("language-server client is closed")
	}
	if c.cmd != nil {
		return nil, nil, fmt.Errorf("language server already started")
	}
	cmd := exec.CommandContext(ctx, c.command, c.args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("open language-server stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("open language-server stdin: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("open language-server stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start language server %s: %w", c.command, err)
	}
	c.cmd = cmd
	c.stdin = stdin
	c.queueMu.Lock()
	c.running = true
	c.queueMu.Unlock()
	return stdout, stderr, nil
}

// Close stops the language server in the background and returns immediately.
// It is safe to call any number of times, concurrently, and before or during
// Start. The first call marks the client closed so a later Start fails. If the
// process is running, a goroutine sends shutdown (2 s timeout) and exit, waits
// up to 2 s for the process to exit, kills it otherwise, and reaps it.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.stateMu.Lock()
		c.closed = true
		cmd := c.cmd
		c.stateMu.Unlock()
		if cmd == nil {
			return
		}
		go c.stop(cmd)
	})
}

func (c *Client) stop(cmd *exec.Cmd) {
	select {
	case <-c.done:
	default:
		ctx, cancel := context.WithTimeout(context.Background(), c.timeouts.shutdown)
		var ignored json.RawMessage
		if err := c.call(ctx, "shutdown", nil, &ignored); err != nil {
			slog.Debug("shut down language server", "command", c.command, "err", err)
		}
		cancel()
		if err := c.notify("exit", nil); err != nil {
			slog.Debug("notify language-server exit", "command", c.command, "err", err)
		}
	}
	select {
	case <-c.done:
	case <-time.After(c.timeouts.exit):
		if err := cmd.Process.Kill(); err != nil {
			slog.Debug("kill language server", "command", c.command, "err", err)
		}
	}
	c.reap(cmd)
}

// reap waits for the subprocess exactly once, whichever of readLoop and stop
// gets there first.
func (c *Client) reap(cmd *exec.Cmd) {
	c.waitOnce.Do(func() {
		if err := cmd.Wait(); err != nil {
			slog.Debug("language server exited", "command", c.command, "err", err)
		}
		close(c.exited)
	})
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

// Completions requests semantic completion at position. Items come back with
// preselected items first, then ordered by sortText (label when absent).
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
		SortText   string `json:"sortText"`
		FilterText string `json:"filterText"`
		Preselect  bool   `json:"preselect"`
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
		out = append(out, Completion{
			Label: item.Label, Detail: item.Detail, InsertText: insert,
			SortText: item.SortText, FilterText: item.FilterText, Preselect: item.Preselect,
			TextEdit: item.TextEdit,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Preselect != out[j].Preselect {
			return out[i].Preselect
		}
		return sortKey(out[i]) < sortKey(out[j])
	})
	return out, nil
}

func sortKey(item Completion) string {
	if item.SortText != "" {
		return item.SortText
	}
	return item.Label
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
	if err := c.write(ctx, message(id, method, params)); err != nil {
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

// message builds a JSON-RPC request (id > 0) or notification, leaving out
// params when there are none.
func message(id int64, method string, params any) map[string]any {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if id > 0 {
		msg["id"] = id
	}
	if params != nil {
		msg["params"] = params
	}
	return msg
}

// notify queues a notification. It never blocks on the server pipe and never
// drops a message while the server is running.
func (c *Client) notify(method string, params any) error {
	select {
	case <-c.done:
		return fmt.Errorf("language server exited")
	default:
	}
	return c.enqueue(outbound{message: message(0, method, params)})
}

func (c *Client) enqueue(item outbound) error {
	c.queueMu.Lock()
	if !c.running {
		c.queueMu.Unlock()
		return fmt.Errorf("language server is not running")
	}
	c.queue = append(c.queue, item)
	c.queueMu.Unlock()
	select {
	case c.queueSignal <- struct{}{}:
	default:
	}
	return nil
}

func (c *Client) write(ctx context.Context, message any) error {
	select {
	case <-c.done:
		return fmt.Errorf("language server exited")
	default:
	}
	result := make(chan error, 1)
	if err := c.enqueue(outbound{message: message, result: result}); err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("language server exited")
	}
}

func (c *Client) writeLoop() {
	for {
		c.queueMu.Lock()
		batch := c.queue
		c.queue = nil
		c.queueMu.Unlock()
		if len(batch) == 0 {
			select {
			case <-c.done:
				return
			case <-c.queueSignal:
				continue
			}
		}
		for _, request := range batch {
			err := c.writeDirect(request.message)
			if request.result != nil {
				request.result <- err
			} else if err != nil {
				slog.Debug("write language-server notification", "command", c.command, "err", err)
			}
		}
	}
}

func (c *Client) writeDirect(message any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode language-server message: %w", err)
	}
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
	defer func() {
		close(c.done)
		c.stateMu.Lock()
		cmd := c.cmd
		c.stateMu.Unlock()
		if cmd != nil {
			c.reap(cmd)
		}
	}()
	buffered := bufio.NewReader(reader)
	for {
		body, err := readFrame(buffered)
		if err != nil {
			if err != io.EOF {
				slog.Debug("language server read stopped", "command", c.command, "err", err)
			}
			return
		}
		c.handle(body)
	}
}

func (c *Client) handle(body []byte) {
	var message response
	if err := json.Unmarshal(body, &message); err != nil {
		slog.Debug("decode language-server message", "err", err)
		return
	}
	hasID := len(message.ID) > 0 && string(message.ID) != "null"
	switch {
	case message.Method != "" && hasID:
		c.answerServerRequest(message)
	case hasID:
		id, err := strconv.ParseInt(strings.Trim(string(message.ID), "\""), 10, 64)
		if err != nil {
			return
		}
		c.pendMu.Lock()
		ch := c.pending[id]
		c.pendMu.Unlock()
		if ch != nil {
			select {
			case ch <- message:
			default:
			}
		}
	case message.Method == "textDocument/publishDiagnostics" && c.diagnostics != nil:
		var params struct {
			URI         string       `json:"uri"`
			Diagnostics []Diagnostic `json:"diagnostics"`
		}
		if err := json.Unmarshal(message.Params, &params); err == nil {
			c.diagnostics(params.URI, params.Diagnostics)
		}
	}
}

// answerServerRequest replies to a server-to-client request. Servers such as
// gopls block until these requests are answered.
func (c *Client) answerServerRequest(request response) {
	reply := map[string]any{"jsonrpc": "2.0", "id": request.ID}
	switch request.Method {
	case "workspace/configuration":
		var params struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			slog.Debug("decode workspace/configuration", "err", err)
		}
		reply["result"] = make([]any, len(params.Items))
	case "client/registerCapability", "client/unregisterCapability",
		"window/workDoneProgress/create", "window/showMessageRequest":
		reply["result"] = nil
	default:
		reply["error"] = map[string]any{"code": -32601, "message": "method not found"}
	}
	if err := c.enqueue(outbound{message: reply}); err != nil {
		slog.Debug("answer language-server request", "method", request.Method, "err", err)
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
