package ui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/ai"
	"github.com/jsnjack/e/internal/config"
)

func TestFolderSearchRuntime(t *testing.T) {
	if os.Getenv("E_UI_RUNTIME_TEST") != "1" {
		t.Skip("set E_UI_RUNTIME_TEST=1 on a graphical test display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	gtk.Init()
	app := adw.NewApplication("com.jsnjack.e.FolderSearchRuntimeTest", gio.ApplicationNonUnique)
	if err := app.Register(context.Background()); err != nil {
		t.Fatalf("register test application: %v", err)
	}
	base := t.TempDir()
	cfg := config.Default()
	cfg.AI.Enabled = false
	cfg.Editor.SpellCheck = false
	w := newWindow(app, Options{
		Config: cfg, ConfigPath: filepath.Join(base, "config", "config.toml"),
		Paths:     config.Paths{StateDir: filepath.Join(base, "state"), CacheDir: filepath.Join(base, "cache")},
		Completer: ai.NewCompleter([]ai.Provider{runtimeProvider{}}),
	})
	defer func() {
		w.stop()
		w.win.Destroy()
	}()
	w.present()
	waitForUI(t, func() bool { return w.win.Width() > 0 && w.win.Height() > 0 })

	root := filepath.Join(base, "project")
	files := map[string]string{
		"a.txt":     "alpha\nbeta needle\n",
		"sub/b.txt": "needle Needle\n",
		"blob.bin":  "needle\x00",
	}
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a, b := filepath.Join(root, "a.txt"), filepath.Join(root, "sub", "b.txt")
	w.setProjectRoot(root)
	s := w.folderSearch

	// A one-line selection becomes the query.
	w.openPath(b)
	waitForUI(t, func() bool { doc := w.active(); return doc != nil && doc.path == b && !doc.loading })
	doc := w.active()
	doc.buffer.SelectRange(doc.buffer.IterAtOffset(0), doc.buffer.IterAtOffset(6))
	w.showFolderSearch()
	if s.entry.Text() != "needle" || w.sidebarStack.VisibleChildName() != "search" || !w.sidebar.Visible() {
		t.Fatalf("Find in Folder: query %q, view %q, sidebar %v", s.entry.Text(), w.sidebarStack.VisibleChildName(), w.sidebar.Visible())
	}
	// Two file headers and three matches; the binary file is skipped.
	waitForUI(t, func() bool { return len(s.rows) == 5 })
	if got := s.status.Label(); got != "3 results in 2 files" {
		t.Fatalf("status = %q", got)
	}
	saveScreenshot(t, w.toastOverlay, "folder-search.png")

	// Opening a match in a file that is not open yet selects it after load.
	s.activate(1)
	waitForUI(t, func() bool { doc := w.active(); return doc != nil && doc.path == a && !doc.loading })
	if got := selectedText(w.active()); got != "needle" {
		t.Fatalf("opened match selection = %q", got)
	}
	start, _, _ := w.active().buffer.SelectionBounds()
	if start.Line() != 1 || start.LineOffset() != 5 {
		t.Fatalf("opened match at %d:%d, want 1:5", start.Line(), start.LineOffset())
	}

	// F4 goes on to the next file and wraps; Shift+F4 goes back.
	s.navigate(1)
	if w.active().path != b || selectedText(w.active()) != "needle" {
		t.Fatalf("next result: %s %q", w.active().path, selectedText(w.active()))
	}
	s.navigate(1)
	if start, _, _ := w.active().buffer.SelectionBounds(); start.LineOffset() != 7 {
		t.Fatalf("second match in b.txt starts at %d", start.LineOffset())
	}
	s.navigate(1)
	if w.active().path != a {
		t.Fatal("next result did not wrap to the first file")
	}
	s.navigate(-1)
	if w.active().path != b {
		t.Fatal("previous result did not wrap to the last file")
	}

	// A file header folds its matches away.
	s.activate(0)
	if s.groups[0].expanded || s.groups[0].rows[0].Visible() {
		t.Fatal("the file header did not collapse its matches")
	}
	s.activate(0)
	if !s.groups[0].rows[0].Visible() {
		t.Fatal("the file header did not expand its matches")
	}

	// Match Case and Whole Word narrow the results; a bad expression says so.
	s.matchCase.SetActive(true)
	waitForUI(t, func() bool { return len(s.rows) == 4 })
	s.entry.SetText("Needle")
	s.search(false)
	waitForUI(t, func() bool { return s.status.Label() == "1 result in 1 file" })
	s.matchCase.SetActive(false)
	s.entry.SetText("needl")
	s.search(false)
	waitForUI(t, func() bool { return len(s.rows) == 5 })
	s.wholeWord.SetActive(true)
	waitForUI(t, func() bool { return s.status.Label() == "No results found." })
	s.wholeWord.SetActive(false)
	s.regex.SetActive(true)
	s.entry.SetText("(")
	s.search(false)
	if !strings.HasPrefix(s.status.Label(), "Invalid regular expression") || len(s.rows) != 0 {
		t.Fatalf("invalid expression: status %q, %d rows", s.status.Label(), len(s.rows))
	}
	s.regex.SetActive(false)

	// Another folder replaces the results while the view is showing.
	other := filepath.Join(base, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "c.txt"), []byte("(\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.setProjectRoot(other)
	waitForUI(t, func() bool { return s.status.Label() == "1 result in 1 file" })
	if s.groups[0].path != filepath.Join(other, "c.txt") {
		t.Fatalf("new folder result is in %s", s.groups[0].path)
	}

	// Ctrl+F starts with the word at the caret.
	w.openPath(a)
	doc = w.active()
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(2))
	w.showSearch(false)
	if got := w.searchEntry.Text(); got != "alpha" {
		t.Fatalf("Find query = %q, want the word at the caret", got)
	}
	w.searchBar.SetSearchMode(false)

	w.win.LookupAction("show-sidebar").Activate(nil)
	if w.sidebarStack.VisibleChildName() != "files" {
		t.Fatal("Ctrl+Shift+E did not show the Explorer view")
	}
}

func selectedText(doc *editorDocument) string {
	start, end, ok := doc.buffer.SelectionBounds()
	if !ok {
		return ""
	}
	return doc.buffer.Text(start, end, true)
}
