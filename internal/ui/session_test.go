package ui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/config"
	"github.com/jsnjack/e/internal/document"
)

// TestSessionRuntime checks in a real GTK session that each folder keeps its
// own tabs, that unsaved tabs are marked, and that stale tabs are dropped.
func TestSessionRuntime(t *testing.T) {
	if os.Getenv("E_UI_RUNTIME_TEST") != "1" {
		t.Skip("set E_UI_RUNTIME_TEST=1 on a graphical test display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	gtk.Init()
	app := adw.NewApplication("com.jsnjack.e.SessionRuntimeTest", gio.ApplicationNonUnique)
	if err := app.Register(context.Background()); err != nil {
		t.Fatalf("register test application: %v", err)
	}
	base := t.TempDir()
	cfg := config.Default()
	cfg.AI.Enabled = false
	cfg.Editor.SpellCheck = false
	options := Options{
		Config: cfg, ConfigPath: filepath.Join(base, "config", "config.toml"),
		Paths: config.Paths{StateDir: filepath.Join(base, "state"), CacheDir: filepath.Join(base, "cache")},
	}
	openWindow := func() *window {
		w := newWindow(app, options)
		t.Cleanup(func() { w.win.Destroy() })
		w.present()
		waitForUI(t, func() bool { return w.win.Width() > 0 && w.win.Height() > 0 })
		return w
	}
	folder := func(name string, files ...string) string {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	first, second, gone := folder("first", "a.txt"), folder("second", "b.txt"), folder("gone")
	firstFile, secondFile := filepath.Join(first, "a.txt"), filepath.Join(second, "b.txt")
	openFile := func(w *window, path string) *editorDocument {
		t.Helper()
		w.openPath(path)
		waitForUI(t, func() bool { doc, _ := w.documentForPath(path); return doc != nil && !doc.loading })
		doc, _ := w.documentForPath(path)
		return doc
	}
	titles := func(w *window) map[string]*editorDocument {
		out := make(map[string]*editorDocument)
		for _, doc := range w.documents {
			out[doc.title()] = doc
		}
		return out
	}

	w := openWindow()
	// A file opened in a window without a folder chooses the folder.
	saved := openFile(w, firstFile)
	if w.sessionFolder != first || w.projectRoot != "" {
		t.Fatalf("session folder %q, project %q after opening a file", w.sessionFolder, w.projectRoot)
	}
	draft := w.newDocument(nil)
	setBufferText(draft, "draft")
	if draft.page.IndicatorIcon() == nil || draft.page.NeedsAttention() {
		t.Fatal("an unsaved tab is not marked, or is marked as recovered")
	}
	if saved.page.IndicatorIcon() != nil {
		t.Fatal("a saved tab is marked as unsaved")
	}

	// Another project swaps the tabs without asking about the unsaved draft.
	w.setProjectRoot(second)
	if len(w.documents) != 0 || w.mainStack.VisibleChildName() != "empty" {
		t.Fatalf("switching projects kept %d tabs", len(w.documents))
	}
	openFile(w, secondFile)

	w.setProjectRoot(first)
	waitForUI(t, func() bool { return len(w.documents) == 2 && titles(w)["a.txt"] != nil && !titles(w)["a.txt"].loading })
	restored := titles(w)["Untitled"]
	if restored == nil || bufferText(restored.buffer) != "draft" || !restored.buffer.Modified() {
		t.Fatal("the unsaved draft did not come back with its folder")
	}
	if !restored.page.NeedsAttention() || restored.page.IndicatorTooltip() != "Unsaved changes from an earlier session" {
		t.Fatal("restored unsaved text is not marked as recovered")
	}

	w.closeSavedTabs()
	if len(w.documents) != 1 || titles(w)["Untitled"] == nil {
		t.Fatalf("Close Saved Tabs left %d tabs", len(w.documents))
	}
	restored.buffer.SetModified(false)
	if restored.page.NeedsAttention() || restored.page.IndicatorIcon() != nil {
		t.Fatal("a tab stayed marked after its text was saved")
	}
	restored.buffer.SetModified(true)

	// A restored tab whose file was deleted closes instead of reopening empty.
	missing := filepath.Join(gone, "missing.txt")
	if err := document.SaveSession(document.SessionDir(options.Paths.StateDir, gone),
		[]document.Snapshot{{ID: "missing", Path: missing, Title: "missing.txt"}}); err != nil {
		t.Fatal(err)
	}
	w.setProjectRoot(gone)
	waitForUI(t, func() bool { return len(w.documents) == 0 })
	if err := w.persistSession(true); err != nil {
		t.Fatal(err)
	}
	if got, err := document.LoadSession(document.SessionDir(options.Paths.StateDir, gone)); err != nil || len(got) != 0 {
		t.Fatalf("session of the folder with a deleted file = %+v, %v", got, err)
	}

	w.setProjectRoot(first)
	w.stop()

	// E started without arguments reopens the last project and its tabs.
	next := openWindow()
	defer next.stop()
	next.restoreOrNew()
	if next.projectRoot != first {
		t.Fatalf("restored project %q, want %q", next.projectRoot, first)
	}
	waitForUI(t, func() bool {
		doc := titles(next)["Untitled"]
		return len(next.documents) == 1 && doc != nil && !doc.loading
	})
	if got := bufferText(titles(next)["Untitled"].buffer); got != "draft" {
		t.Fatalf("restored draft = %q", got)
	}
}
