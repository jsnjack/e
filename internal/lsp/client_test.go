package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

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

func TestFileURI(t *testing.T) {
	uri := FileURI("folder/file name.go")
	if !strings.HasPrefix(uri, "file://") || !strings.Contains(uri, "file%20name.go") {
		t.Fatalf("unexpected file URI %q", uri)
	}
}
