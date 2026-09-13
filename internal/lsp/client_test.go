package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
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
	client := &Client{
		stdin: writer, outgoing: make(chan outbound, 2), done: make(chan struct{}),
	}
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

func TestFileURI(t *testing.T) {
	uri := FileURI("folder/file name.go")
	if !strings.HasPrefix(uri, "file://") || !strings.Contains(uri, "file%20name.go") {
		t.Fatalf("unexpected file URI %q", uri)
	}
}
