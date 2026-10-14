package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingWriteCloser struct {
	started chan struct{}
	release chan struct{}
}

func (w *blockingWriteCloser) Write(body []byte) (int, error) {
	select {
	case w.started <- struct{}{}:
	default:
	}
	<-w.release
	return len(body), nil
}

func (*blockingWriteCloser) Close() error { return nil }

func TestReadFrame(t *testing.T) {
	tests := []struct {
		name string
		wire string
		want string
	}{
		{"standard", "Content-Length: 7\r\n\r\n{\"x\":1}", "{\"x\":1}"},
		{"extra header", "Content-Type: application/vscode-jsonrpc\r\nContent-Length: 2\r\n\r\n{}", "{}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readFrame(bufio.NewReader(bytes.NewBufferString(tc.wire)))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadFrameRejectsMalformedInput(t *testing.T) {
	tests := []string{
		"\r\n{}",
		"Content-Length: nope\r\n\r\n{}",
		"Content-Length: 4\r\n\r\n{}",
	}
	for _, wire := range tests {
		if _, err := readFrame(bufio.NewReader(bytes.NewBufferString(wire))); err == nil {
			t.Errorf("readFrame accepted %q", wire)
		}
	}
}

func TestDecodeCompletions(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "array", raw: `[{"label":"one"},{"label":"two","insertText":"2"}]`, want: []string{"one", "2"}},
		{name: "completion list", raw: `{"items":[{"label":"item"}]}`, want: []string{"item"}},
		{name: "null", raw: `null`},
		{
			name: "sorted",
			raw: `[{"label":"zeta"},{"label":"beta","sortText":"0002"},{"label":"alpha","sortText":"0002"},` +
				`{"label":"pick","preselect":true,"sortText":"9"},{"label":"first","sortText":"0001","filterText":"f"}]`,
			want: []string{"pick", "first", "beta", "alpha", "zeta"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items, err := decodeCompletions(json.RawMessage(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != len(test.want) {
				t.Fatalf("items = %+v, want %v", items, test.want)
			}
			for i := range items {
				if items[i].InsertText != test.want[i] {
					t.Fatalf("item %d insert text = %q, want %q", i, items[i].InsertText, test.want[i])
				}
			}
		})
	}
	if _, err := decodeCompletions(json.RawMessage(`{"items":`)); err == nil {
		t.Fatal("malformed completion response returned no error")
	}
}

func TestNotificationDoesNotWaitForBlockedServerPipe(t *testing.T) {
	writer := &blockingWriteCloser{started: make(chan struct{}, 1), release: make(chan struct{})}
	client := runningClient(writer)
	go client.writeLoop()

	returned := make(chan error, 1)
	go func() {
		returned <- client.Change("file:///test.go", 2, []Change{{Text: "x"}})
	}()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("notification blocked on the language-server pipe")
	}
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive the queued notification")
	}
	close(writer.release)
	close(client.done)
}

// gatedWriter blocks every write until release closes, then records it.
type gatedWriter struct {
	release chan struct{}
	mu      sync.Mutex
	buf     bytes.Buffer
}

func (w *gatedWriter) Write(body []byte) (int, error) {
	<-w.release
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(body)
}

func (*gatedWriter) Close() error { return nil }

func (w *gatedWriter) frames(t *testing.T) []map[string]any {
	t.Helper()
	w.mu.Lock()
	reader := bufio.NewReader(bytes.NewReader(w.buf.Bytes()))
	w.mu.Unlock()
	var out []map[string]any
	for {
		body, err := readFrame(reader)
		if err != nil {
			// EOF or a frame still being written: return the complete ones.
			return out
		}
		var message map[string]any
		if err := json.Unmarshal(body, &message); err != nil {
			t.Fatal(err)
		}
		out = append(out, message)
	}
}

func runningClient(stdin io.WriteCloser) *Client {
	client := New("fake", nil, ".", nil)
	client.stdin = stdin
	client.running = true
	return client
}

func TestNotifyNeverDropsAndKeepsOrder(t *testing.T) {
	writer := &gatedWriter{release: make(chan struct{})}
	client := runningClient(writer)
	go client.writeLoop()
	const count = 1000
	for version := 1; version <= count; version++ {
		if err := client.Change("file:///test.go", version, []Change{{Text: "x"}}); err != nil {
			t.Fatalf("change %d: %v", version, err)
		}
	}
	close(writer.release)
	deadline := time.Now().Add(5 * time.Second)
	var frames []map[string]any
	for time.Now().Before(deadline) {
		if frames = writer.frames(t); len(frames) == count {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(client.done)
	if len(frames) != count {
		t.Fatalf("wrote %d of %d notifications", len(frames), count)
	}
	for i, frame := range frames {
		document := frame["params"].(map[string]any)["textDocument"].(map[string]any)
		if got := int(document["version"].(float64)); got != i+1 {
			t.Fatalf("frame %d has version %d", i, got)
		}
	}
}

func TestServerRequestsAreAnswered(t *testing.T) {
	tests := []struct {
		name    string
		request string
		want    string
	}{
		{
			name:    "configuration",
			request: `{"jsonrpc":"2.0","id":3,"method":"workspace/configuration","params":{"items":[{"section":"gopls"},{"section":"go"}]}}`,
			want:    `{"id":3,"jsonrpc":"2.0","result":[null,null]}`,
		},
		{
			name:    "register capability with string id",
			request: `{"jsonrpc":"2.0","id":"abc-1","method":"client/registerCapability","params":{"registrations":[]}}`,
			want:    `{"id":"abc-1","jsonrpc":"2.0","result":null}`,
		},
		{
			name:    "unregister capability",
			request: `{"jsonrpc":"2.0","id":4,"method":"client/unregisterCapability","params":{}}`,
			want:    `{"id":4,"jsonrpc":"2.0","result":null}`,
		},
		{
			name:    "progress create",
			request: `{"jsonrpc":"2.0","id":5,"method":"window/workDoneProgress/create","params":{"token":"t"}}`,
			want:    `{"id":5,"jsonrpc":"2.0","result":null}`,
		},
		{
			name:    "show message request",
			request: `{"jsonrpc":"2.0","id":6,"method":"window/showMessageRequest","params":{"type":1,"message":"m"}}`,
			want:    `{"id":6,"jsonrpc":"2.0","result":null}`,
		},
		{
			name:    "unknown method",
			request: `{"jsonrpc":"2.0","id":7,"method":"workspace/applyEdit","params":{}}`,
			want:    `{"error":{"code":-32601,"message":"method not found"},"id":7,"jsonrpc":"2.0"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &gatedWriter{release: make(chan struct{})}
			close(writer.release)
			client := runningClient(writer)
			go client.writeLoop()
			defer close(client.done)
			// A pending call whose id collides with the server request must
			// stay pending: the request is not its response.
			pending := make(chan response, 1)
			client.pending[3] = pending
			client.handle([]byte(test.request))
			select {
			case got := <-pending:
				t.Fatalf("server request completed pending call: %+v", got)
			default:
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if frames := writer.frames(t); len(frames) == 1 {
					got, err := json.Marshal(frames[0])
					if err != nil {
						t.Fatal(err)
					}
					if string(got) != test.want {
						t.Fatalf("reply = %s, want %s", got, test.want)
					}
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Fatal("no reply written")
		})
	}
}

func TestResponseCompletesPendingCall(t *testing.T) {
	client := runningClient(&gatedWriter{release: make(chan struct{})})
	pending := make(chan response, 1)
	client.pending[3] = pending
	client.handle([]byte(`{"jsonrpc":"2.0","id":3,"result":{"ok":true}}`))
	select {
	case got := <-pending:
		if string(got.Result) != `{"ok":true}` {
			t.Fatalf("result = %s", got.Result)
		}
	default:
		t.Fatal("response did not complete the pending call")
	}
}

func TestFileURI(t *testing.T) {
	uri := FileURI("folder/file name.go")
	if !strings.HasPrefix(uri, "file://") || !strings.Contains(uri, "file%20name.go") {
		t.Fatalf("unexpected file URI %q", uri)
	}
}
