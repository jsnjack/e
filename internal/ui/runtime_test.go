package ui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/activity"
	"github.com/jsnjack/e/internal/ai"
	"github.com/jsnjack/e/internal/config"
)

type runtimeProvider struct{}

func (runtimeProvider) Complete(_ context.Context, request ai.Request) (string, error) {
	if request.Instruction != "" {
		return "transformed", nil
	}
	return "completion", nil
}
func (runtimeProvider) Healthy(context.Context) error { return nil }
func (runtimeProvider) Label() string                 { return "runtime" }

func TestEditorRuntimeSmoke(t *testing.T) {
	if os.Getenv("E_UI_RUNTIME_TEST") != "1" {
		t.Skip("set E_UI_RUNTIME_TEST=1 on a graphical test display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	gtk.Init()

	app := adw.NewApplication("com.jsnjack.e.RuntimeTest", gio.ApplicationNonUnique)
	if err := app.Register(context.Background()); err != nil {
		t.Fatalf("register test application: %v", err)
	}
	root := t.TempDir()
	cfg := config.Default()
	cfg.AI.Enabled = false
	cfg.Editor.SpellCheck = false
	w := newWindow(app, Options{
		Config: cfg, ConfigPath: filepath.Join(root, "config", "config.toml"),
		Paths:     config.Paths{StateDir: filepath.Join(root, "state"), CacheDir: filepath.Join(root, "cache")},
		Completer: ai.NewCompleter([]ai.Provider{runtimeProvider{}}),
	})
	defer func() {
		w.stop()
		w.win.Destroy()
	}()
	w.present()
	waitForUI(t, func() bool { return w.win.Width() > 0 && w.win.Height() > 0 })

	doc := w.newDocument(nil)
	if w.tabs.NPages() != 1 || w.active() != doc {
		t.Fatalf("initial document was not selected")
	}
	if w.activity.document.Text() != "New · Plain Text" ||
		w.activity.cursor.Text() != "Ln 1, Col 1" || !w.activity.bar.Visible() {
		t.Fatal("persistent document status bar was not initialized")
	}
	assertWindowActions(t, w)
	if !w.keyPressed(gdk.KEY_k, 0, gdk.ControlMask) || !w.keyPressed(gdk.KEY_s, 0, gdk.ControlMask) {
		t.Fatal("Ctrl+K Ctrl+S shortcut chord was not handled")
	}

	w.openPreferences()
	preferences := w.preferences
	if preferences == nil {
		t.Fatal("preferences dialog was not created")
	}
	w.openPreferences()
	if w.preferences != preferences {
		t.Fatal("opening preferences twice stacked modal dialogs")
	}
	waitForUI(t, func() bool { return preferences.Parent() != nil })
	if !preferences.Close() {
		t.Fatal("preferences dialog refused to close")
	}
	waitForUI(t, func() bool {
		return preferences.Parent() == nil && w.preferences == nil && !w.preferencesApplying
	})
	if !w.content.IsSensitive() {
		t.Fatal("closing preferences left the editor insensitive")
	}

	newCommand := paletteCommand(w.commandPaletteItems(), "new")
	if newCommand == nil {
		t.Fatal("New Text File command is missing")
	}
	newCommand.run()
	if w.tabs.NPages() != 2 || w.active() == doc {
		t.Fatalf("New Text File did not create and select a tab")
	}
	w.tabs.ClosePage(w.tabs.SelectedPage())
	w.selectRelativeTab(1)
	if w.active() != doc {
		t.Fatal("relative tab selection changed a single-tab view")
	}

	w.showSearch(false)
	if !w.searchBar.SearchMode() || w.replaceEntry.Visible() {
		t.Fatal("Find did not open search-only mode")
	}
	w.showSearch(true)
	if !w.replaceEntry.Visible() {
		t.Fatal("Replace did not reveal the replacement field")
	}
	w.searchBar.SetSearchMode(false)
	if w.tabs.NPages() != 1 || w.active() != doc {
		t.Fatalf("closing the new tab did not restore the original tab")
	}
	newAction := w.win.LookupAction("new")
	newAction.Activate(nil)
	if w.tabs.NPages() != 2 {
		t.Fatal("New Text File action did not create a tab")
	}
	w.tabs.ClosePage(w.tabs.SelectedPage())

	setBufferText(doc, "one two one")
	doc.search.matches = findMatches("one two one", "one")
	doc.search.current = 0
	doc.search.replaceCurrent("ONE")
	if got := bufferText(doc.buffer); got != "ONE two one" {
		t.Fatalf("replace current = %q", got)
	}
	doc.search.matches = findMatches(bufferText(doc.buffer), "one")
	doc.search.current = 0
	doc.search.replaceAll("1")
	if got := bufferText(doc.buffer); got != "1 two 1" {
		t.Fatalf("replace all = %q", got)
	}

	setBufferText(doc, "cat dog cat")
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(1))
	doc.selectNextOccurrence()
	if len(doc.multiCursors) != 2 {
		t.Fatalf("select next occurrence count = %d", len(doc.multiCursors))
	}
	doc.replaceMultiple("fox")
	if got := bufferText(doc.buffer); got != "fox dog fox" {
		t.Fatalf("occurrence edit = %q", got)
	}
	doc.clearMultipleSelections()

	setBufferText(doc, "a\nb\nc")
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(0))
	if handled := doc.keyPressed(gdk.KEY_Down, 0, gdk.ShiftMask|gdk.AltMask); !handled {
		t.Fatal("Shift+Alt+Down was not handled")
	}
	if len(doc.multiCursors) != 2 {
		t.Fatalf("vertical cursor count = %d", len(doc.multiCursors))
	}
	if handled := doc.keyPressed(gdk.KEY_Down, 0, gdk.ControlMask); handled {
		t.Fatal("Ctrl+Down was incorrectly handled as a multiple-cursor command")
	}
	doc.replaceMultiple("x")
	if got := bufferText(doc.buffer); got != "xa\nxb\nc" {
		t.Fatalf("multiple-cursor edit = %q", got)
	}
	if handled := doc.keyPressed(gdk.KEY_Escape, 0, 0); !handled || len(doc.multiCursors) != 0 {
		t.Fatal("Escape did not leave multiple-cursor mode")
	}

	setBufferText(doc, "E EDGE E")
	doc.applyBrandRange(0, doc.buffer.CharCount())
	if !doc.buffer.IterAtOffset(0).HasTag(doc.brandTag) || doc.buffer.IterAtOffset(2).HasTag(doc.brandTag) ||
		!doc.buffer.IterAtOffset(7).HasTag(doc.brandTag) {
		t.Fatal("standalone E branding was not applied selectively")
	}

	setBufferText(doc, "left right")
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(5))
	doc.showCompletion(" ghost", 5)
	if got := bufferText(doc.buffer); got != "left right" {
		t.Fatalf("injected completion changed document buffer to %q", got)
	}
	if !doc.ghost.area.Visible() || doc.ghost.spaceStart == nil || doc.ghostText != " ghost" {
		t.Fatal("mid-line completion was not shown in reserved space")
	}

	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	doc.showCompletion(" ghost\nnext", doc.buffer.CharCount())
	if got := bufferText(doc.buffer); got != "left right" {
		t.Fatalf("line-end completion changed document buffer to %q", got)
	}
	if !doc.ghost.area.Visible() || doc.ghost.spaceStart == nil || doc.ghostText != " ghost\nnext" {
		t.Fatalf("safe completion was not rendered exactly: text=%q visible=%v", doc.ghostText, doc.ghost.area.Visible())
	}

	doc.buffer.PlaceCursor(doc.buffer.StartIter())
	if doc.ghost.area.Visible() || doc.ghostText != "" || doc.ghost.spaceStart != nil {
		t.Fatal("moving the caret did not dismiss the inline completion")
	}
	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	doc.showCompletion(" ghost", doc.buffer.CharCount())
	doc.dismissCompletion()
	if doc.ghost.area.Visible() || doc.ghost.spaceStart != nil || bufferText(doc.buffer) != "left right" {
		t.Fatal("dismissing injected completion did not restore a clean view")
	}

	setBufferText(doc, "typed")
	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	doc.showCompletion(" suggestion", doc.buffer.CharCount())
	doc.buffer.Insert(doc.buffer.IterAtMark(doc.buffer.GetInsert()), "!")
	if got := bufferText(doc.buffer); got != "typed!" {
		t.Fatalf("typing with a completion visible changed real text to %q", got)
	}
	if doc.ghost.area.Visible() || doc.ghostText != "" {
		t.Fatal("typing did not immediately remove the ghost completion")
	}

	setBufferText(doc, "prefix ")
	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	doc.requestCompletion(true)
	waitForUI(t, func() bool { return w.activity.serving.Text() == "runtime" })
	if doc.ghostText != "completion" || !doc.ghost.area.Visible() {
		t.Fatalf("generated completion was not visible: text=%q visible=%v", doc.ghostText, doc.ghost.area.Visible())
	}
	if len(w.activity.history) == 0 || w.activity.model.Text() != "AI: runtime" {
		t.Fatal("AI completion was not reported by the persistent status bar")
	}
	if handled := doc.keyPressed(gdk.KEY_Tab, 0, 0); !handled {
		t.Fatal("Tab did not accept the inline completion")
	}
	if got := bufferText(doc.buffer); got != "prefix completion" {
		t.Fatalf("inline completion acceptance = %q", got)
	}
	fallback := w.activityHub.Begin(activity.AI, "Generating suggestion")
	fallback.Finish("offline fallback", "local-gemma", true)
	waitForUI(t, func() bool { return w.activity.serving.Text() == "local-gemma" })
	if !w.activity.servingChip.Visible() || !w.activity.fallback.Visible() ||
		w.activity.model.Text() != "AI: local-gemma" {
		t.Fatal("fallback model was not made visible in the activity surface")
	}
	w.win.LookupAction("undo").Activate(nil)
	if got := bufferText(doc.buffer); got != "prefix " {
		t.Fatalf("undo completion = %q", got)
	}
	w.win.LookupAction("redo").Activate(nil)
	if got := bufferText(doc.buffer); got != "prefix completion" {
		t.Fatalf("redo completion = %q", got)
	}

	doc.path = filepath.Join(root, "preview.md")
	setBufferText(doc, "# Preview")
	doc.toggleMarkdownPreview()
	if !doc.previewVisible || doc.preview == nil {
		t.Fatal("Markdown preview did not open")
	}
	doc.toggleMarkdownPreview()
	if doc.previewVisible {
		t.Fatal("Markdown preview did not close")
	}

	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, "sample.txt")
	if err := os.WriteFile(path, []byte("opened"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.setProjectRoot(project)
	if !w.sidebar.Visible() {
		t.Fatal("project sidebar was not shown")
	}
	w.toggleSidebar()
	if w.sidebar.Visible() {
		t.Fatal("project sidebar was not hidden")
	}
	w.toggleSidebar()

	w.openPath(path)
	waitForUI(t, func() bool {
		opened, _ := w.documentForPath(path)
		return opened != nil && !opened.loading
	})
	opened, page := w.documentForPath(path)
	if got := bufferText(opened.buffer); got != "opened" {
		t.Fatalf("opened document = %q", got)
	}
	setBufferText(opened, "saved")
	saved := false
	opened.saveThen(path, func(ok bool) { saved = ok })
	waitForUI(t, func() bool { return saved })
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "saved" {
		t.Fatalf("saved document = %q, %v", data, err)
	}
	w.tabs.ClosePage(page)
	w.openPath(path)
	waitForUI(t, func() bool {
		reopened, _ := w.documentForPath(path)
		return reopened != nil && !reopened.loading
	})
	if reopened, _ := w.documentForPath(path); reopened == nil || bufferText(reopened.buffer) != "saved" {
		t.Fatal("closed document could not be reopened")
	}
}

func assertWindowActions(t *testing.T, w *window) {
	t.Helper()
	for _, name := range []string{
		"new", "open", "command-palette", "quick-open", "save", "save-as", "find", "replace",
		"markdown-preview", "ai-transform", "undo", "redo", "toggle-sidebar", "show-sidebar", "close-tab",
		"complete", "lsp-complete", "select-next", "select-all-occurrences", "next-tab", "previous-tab",
		"find-next", "find-previous", "preferences", "shortcuts", "about",
	} {
		if action := w.win.LookupAction(name); action == nil {
			t.Errorf("window action %q is missing", name)
		}
	}
	for action, shortcut := range map[string]string{
		"new": "<Control>n", "command-palette": "<Control><Shift>p", "quick-open": "<Control>p",
		"open": "<Control>o", "save": "<Control>s", "save-as": "<Control><Shift>s",
		"find": "<Control>f", "replace": "<Control>h", "markdown-preview": "<Control><Shift>v",
		"undo": "<Control>z", "redo": "<Control><Shift>z", "toggle-sidebar": "<Control>b",
		"show-sidebar": "<Control><Shift>e", "close-tab": "<Control>w", "complete": "<Alt>backslash",
		"lsp-complete": "<Control>space", "select-next": "<Control>d",
		"select-all-occurrences": "<Control><Shift>l", "next-tab": "<Control>Tab",
		"previous-tab": "<Control><Shift>Tab", "find-next": "F3", "find-previous": "<Shift>F3",
		"preferences": "<Control>comma",
	} {
		if got := w.app.AccelsForAction("win." + action); !containsAccelerator(got, shortcut) {
			t.Errorf("action %q accelerators = %v, want %q", action, got, shortcut)
		}
	}
	for _, alias := range []struct{ action, shortcut string }{
		{action: "redo", shortcut: "<Control>y"},
		{action: "select-all-occurrences", shortcut: "<Control>F2"},
		{action: "select-all-occurrences", shortcut: "<Alt>Return"},
	} {
		if got := w.app.AccelsForAction("win." + alias.action); !containsAccelerator(got, alias.shortcut) {
			t.Errorf("action %q accelerators = %v, want alias %q", alias.action, got, alias.shortcut)
		}
	}
}

func paletteCommand(items []paletteItem, id string) *paletteItem {
	for i := range items {
		if items[i].id == id {
			return &items[i]
		}
	}
	return nil
}

func setBufferText(doc *editorDocument, text string) {
	doc.loading = true
	doc.buffer.SetText(text)
	doc.loading = false
}

func bufferText(buffer *gtk.TextBuffer) string {
	start, end := buffer.Bounds()
	return buffer.Text(start, end, true)
}

func waitForUI(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	context := glib.MainContextDefault()
	for !condition() && time.Now().Before(deadline) {
		for context.Pending() {
			context.Iteration(false)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !condition() {
		t.Fatal("timed out waiting for UI operation")
	}
}

func containsAccelerator(values []string, wanted string) bool {
	wantedKey, wantedModifiers, wantedOK := gtk.AcceleratorParse(wanted)
	if !wantedOK {
		return false
	}
	for _, value := range values {
		key, modifiers, ok := gtk.AcceleratorParse(value)
		if ok && key == wantedKey && modifiers == wantedModifiers {
			return true
		}
	}
	return false
}
