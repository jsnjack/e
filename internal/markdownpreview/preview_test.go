package markdownpreview

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func TestRenderMarkdownSafely(t *testing.T) {
	document, err := Render([]byte("# Hello\n\n- one\n- two\n\n[remote](https://example.com)\n\n<script>alert(1)</script>"), "A & B")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"<h1>Hello</h1>", "<li>one</li>", "<title>A &amp; B</title>"} {
		if !strings.Contains(document, expected) {
			t.Errorf("rendered document does not contain %q", expected)
		}
	}
	if strings.Contains(document, "<script>") {
		t.Fatal("raw script was rendered")
	}
	if strings.Contains(document, `href="https://example.com"`) {
		t.Fatal("preview retained a navigable remote link")
	}
}

func TestWebKitRuntime(t *testing.T) {
	if os.Getenv("E_PREVIEW_RUNTIME_TEST") != "1" {
		t.Skip("set E_PREVIEW_RUNTIME_TEST=1 on a graphical test display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	gtk.Init()
	view, err := New()
	if err != nil {
		t.Fatal(err)
	}
	document, err := Render([]byte("# Preview"), "Preview")
	if err != nil {
		t.Fatal(err)
	}
	view.Load(document, "")
}
