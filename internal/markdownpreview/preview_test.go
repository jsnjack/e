package markdownpreview

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
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

func TestRenderIsShellAroundBody(t *testing.T) {
	source := []byte("# Title\n\ntext *em*")
	body, err := renderBody(source)
	if err != nil {
		t.Fatal(err)
	}
	document, err := Render(source, "T")
	if err != nil {
		t.Fatal(err)
	}
	if document != shell("T", body) {
		t.Fatal("Render differs from shell around renderBody")
	}
	if strings.Contains(body, "<html") || strings.Contains(body, "<body") {
		t.Fatalf("body fragment contains document markup: %q", body)
	}
}

func TestRenderBodySafely(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		forbidden []string
	}{
		{name: "raw script", source: "<script>alert(1)</script>", forbidden: []string{"<script"}},
		{name: "inline handler", source: `<img src=x onerror="alert(1)">`, forbidden: []string{"onerror"}},
		{name: "remote link", source: "[x](https://example.com)", forbidden: []string{"href="}},
		{name: "javascript link", source: "[x](javascript:alert(1))", forbidden: []string{"href="}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := renderBody([]byte(test.source))
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(body, forbidden) {
					t.Errorf("body %q contains %q", body, forbidden)
				}
			}
		})
	}
}

func TestUpdateScriptEncoding(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		source string
	}{
		{name: "script end tag", title: "</script>", source: "`</script><script>alert(1)</script>`"},
		{name: "line separators", title: "a b", source: "one two three"},
		{name: "quotes and backslashes", title: `"'\`, source: "`\"'\\` and \"';alert(1);//"},
		{name: "template literal", title: "${x}", source: "`${alert(1)}`"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page, err := RenderPage([]byte(test.source), test.title)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"<", ">", " ", " ", "`"} {
				if strings.Contains(page.script, forbidden) {
					t.Errorf("script contains raw %q: %q", forbidden, page.script)
				}
			}
			lines := strings.Split(strings.TrimSuffix(page.script, "\n"), "\n")
			if len(lines) != 2 {
				t.Fatalf("script has %d statements, want 2: %q", len(lines), page.script)
			}
			assertLiteral(t, lines[0], "document.title = ", test.title)
			assertLiteral(t, lines[1], "document.body.innerHTML = ", page.body)
		})
	}
}

// assertLiteral checks that statement assigns exactly want as one JSON
// string literal, so the value cannot end the literal or the statement.
func assertLiteral(t *testing.T, statement, prefix, want string) {
	t.Helper()
	literal, ok := strings.CutPrefix(statement, prefix)
	if !ok {
		t.Fatalf("statement %q lacks prefix %q", statement, prefix)
	}
	literal, ok = strings.CutSuffix(literal, ";")
	if !ok {
		t.Fatalf("statement %q lacks terminator", statement)
	}
	var got string
	if err := json.Unmarshal([]byte(literal), &got); err != nil {
		t.Fatalf("literal %q is not one JSON string: %v", literal, err)
	}
	if got != want {
		t.Fatalf("literal decodes to %q, want %q", got, want)
	}
}

func TestWebKitRuntime(t *testing.T) {
	if os.Getenv("E_PREVIEW_RUNTIME_TEST") != "1" {
		t.Skip("set E_PREVIEW_RUNTIME_TEST=1 on a graphical test display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	gtk.Init()

	// GTK must stay on this locked thread, so the cases run in sequence
	// rather than as t.Run sub-tests, which use their own goroutines.
	checkFullLoad(t)
	checkInPlaceUpdate(t)
	checkUpdateDuringFirstLoad(t)
	checkScriptBreakout(t)
}

func checkFullLoad(t *testing.T) {
	view, err := New()
	if err != nil {
		t.Fatal(err)
	}
	document, err := Render([]byte("# Preview"), "Preview")
	if err != nil {
		t.Fatal(err)
	}
	view.Load(document, "")
	waitFor(t, view, "full load", func() bool { return title(view) == "Preview" })
}

func checkInPlaceUpdate(t *testing.T) {
	view, window := presentView(t)
	defer window.Destroy()
	view.Show(renderPage(t, "First"), "")
	waitFor(t, view, "shell load", func() bool { return view.loaded })
	view.evaluate("window.scrollTo(0, 600);")
	probe(t, view, "scroll", "String(window.scrollY)", "600")

	view.Show(renderPage(t, "Second"), "")
	if !view.loaded {
		t.Fatal("same-base update started a full load")
	}
	probe(t, view, "in-place update", "document.querySelector('h1').textContent + '|' + window.scrollY", "Second|600")
}

func checkUpdateDuringFirstLoad(t *testing.T) {
	view, window := presentView(t)
	defer window.Destroy()
	view.Show(renderPage(t, "First"), "")
	view.Show(renderPage(t, "Second"), "")
	waitFor(t, view, "shell load", func() bool { return view.loaded })
	probe(t, view, "update during first load", "document.querySelector('h1').textContent", "Second")
}

func checkScriptBreakout(t *testing.T) {
	view, window := presentView(t)
	defer window.Destroy()
	view.Show(renderPage(t, "Start"), "")
	waitFor(t, view, "shell load", func() bool { return view.loaded })
	page, err := RenderPage([]byte("# End\n\n`</script>\u2028${x}\"';`\n\n<script>document.title='pwned'</script>"), "T")
	if err != nil {
		t.Fatal(err)
	}
	view.Show(page, "")
	probe(t, view, "hostile update", "document.querySelector('h1').textContent + '|' + document.scripts.length", "End|0")
}

func presentView(t *testing.T) (*View, *gtk.Window) {
	t.Helper()
	view, err := New()
	if err != nil {
		t.Fatal(err)
	}
	window := gtk.NewWindow()
	window.SetDefaultSize(480, 320)
	window.SetChild(view.Widget)
	window.Present()
	return view, window
}

func renderPage(t *testing.T, heading string) Page {
	t.Helper()
	source := "# " + heading + "\n\n" + strings.Repeat("paragraph\n\n", 300)
	page, err := RenderPage([]byte(source), heading)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// probe evaluates expression and reads its value back through document.title,
// because the bridge evaluates scripts without a result callback.
func probe(t *testing.T, view *View, what, expression, want string) {
	t.Helper()
	view.evaluate("document.title = " + expression + ";")
	waitFor(t, view, what, func() bool { return title(view) == want })
}

func title(view *View) string {
	value, _ := coreglib.BaseObject(view.Widget).ObjectProperty("title").(string)
	return value
}

func waitFor(t *testing.T, view *View, what string, done func() bool) {
	t.Helper()
	context := glib.MainContextDefault()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; title is %q", what, title(view))
		}
		context.Iteration(false)
		time.Sleep(5 * time.Millisecond)
	}
}
