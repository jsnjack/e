package ui

import "testing"

func TestMarkdownPath(t *testing.T) {
	for _, path := range []string{"README.md", "notes.MARKDOWN", "doc.mdown", "old.mkd"} {
		if !isMarkdownPath(path) {
			t.Errorf("%q should be Markdown", path)
		}
	}
	if isMarkdownPath("main.go") {
		t.Fatal("main.go should not be Markdown")
	}
}
