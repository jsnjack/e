package ui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	"github.com/jsnjack/e/internal/document"
	"github.com/jsnjack/e/internal/gtktest"
	"github.com/jsnjack/e/internal/lsp"
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
	w.content.SetStateFlags(gtk.StateFlagBackdrop, false)
	if w.content.StateFlags()&gtk.StateFlagBackdrop == 0 {
		t.Fatal("runtime could not establish the backdrop-state test precondition")
	}
	preserveWindowContrast(&w.win.Widget)
	if w.content.StateFlags()&gtk.StateFlagBackdrop != 0 {
		t.Fatal("inactive-window contrast preservation left content in backdrop state")
	}

	doc := w.newDocument(nil)
	if w.tabs.NPages() != 1 || w.active() != doc {
		t.Fatalf("initial document was not selected")
	}
	loadComplete := false
	doc.insertChunks(bytes.Repeat([]byte("load\n"), 20_000), false, 0, nil, func(ok bool) { loadComplete = ok })
	if doc.view.Widget.Editable() {
		t.Fatal("document remained editable while chunked loading was active")
	}
	waitForUI(t, func() bool { return loadComplete })
	if !doc.view.Widget.Editable() {
		t.Fatal("document did not become editable after loading")
	}
	if w.activity.document.Text() != "New · Plain Text" ||
		w.activity.cursor.Text() != "Ln 1, Col 1" || !w.activity.bar.Visible() {
		t.Fatal("persistent document status bar was not initialized")
	}
	assertWindowActions(t, w)
	if !w.keyPressed(gdk.KEY_k, 0, gdk.ControlMask) || !w.keyPressed(gdk.KEY_s, 0, gdk.ControlMask) {
		t.Fatal("Ctrl+K Ctrl+S shortcut chord was not handled")
	}
	if !w.keyPressed(gdk.KEY_k, 0, gdk.ControlMask) {
		t.Fatal("Ctrl+K shortcut prefix was not handled")
	}
	waitForUI(t, func() bool { return !w.pendingCtrlK })

	doc.path = filepath.Join(root, "issues.go")
	doc.lspURI = lsp.FileURI(doc.path)
	w.setDiagnostics(doc.lspURI, []lsp.Diagnostic{{Message: "one"}, {Message: "two"}})
	waitForUI(t, func() bool { return strings.Contains(w.title.Subtitle(), "2 diagnostics") })
	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	if !strings.Contains(w.title.Subtitle(), "2 diagnostics") {
		t.Fatal("cursor update discarded the diagnostics count")
	}
	w.setDiagnostics(doc.lspURI, nil)
	waitForUI(t, func() bool { return w.title.Subtitle() == doc.path })
	doc.path, doc.lspURI = "", ""
	doc.updatePresentation()

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
	// Selecting a new tab with search open refreshes its search state.
	if w.newDocument(nil).search == nil {
		t.Fatal("a tab opened during search has no search state")
	}
	w.tabs.ClosePage(w.tabs.SelectedPage())
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
	doc.search.query = "one"
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
	doc.search.matches = findMatches("1 two 1", "1")
	doc.search.current = 0
	doc.search.update("two")
	if len(doc.search.matches) != 0 {
		t.Fatal("changing the search query left stale replace ranges active")
	}
	doc.search.update("")

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
	setBufferText(doc, "cat dog cat")
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(1))
	doc.selectNextOccurrence()
	doc.buffer.SetText("changed elsewhere")
	if len(doc.multiCursors) != 0 {
		t.Fatal("an external buffer edit left stale multiple cursors active")
	}

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

	checkEditingFixes(t, doc)
	checkCompletionDoesNotRedrawText(t, doc)
	checkCompletionLineEnds(t, doc)
	checkCompletionLongLine(t, doc)
	checkCompletionResize(t, doc)

	setBufferText(doc, "left right")
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(5))
	doc.showCompletion(" ghost", 5)
	if got := bufferText(doc.buffer); got != "left right" {
		t.Fatalf("mid-line completion changed document buffer to %q", got)
	}
	if doc.ghost.visible() || doc.ghostText != "" {
		t.Fatal("mid-line completion was shown")
	}

	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	doc.showCompletion(" ghost\nnext", doc.buffer.CharCount())
	if got := bufferText(doc.buffer); got != "left right" {
		t.Fatalf("line-end completion changed document buffer to %q", got)
	}
	if doc.ghostText != " ghost\nnext" || doc.ghost.text != " ghost  +1 line" {
		t.Fatalf("multiline completion shown as %q for %q", doc.ghost.text, doc.ghostText)
	}

	doc.buffer.PlaceCursor(doc.buffer.StartIter())
	if doc.ghost.visible() || doc.ghostText != "" {
		t.Fatal("moving the caret did not dismiss the inline completion")
	}
	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	doc.showCompletion(" ghost", doc.buffer.CharCount())
	doc.dismissCompletion()
	if doc.ghost.visible() || bufferText(doc.buffer) != "left right" {
		t.Fatal("dismissing a completion did not restore a clean view")
	}

	setBufferText(doc, "typed")
	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	doc.showCompletion(" suggestion", doc.buffer.CharCount())
	doc.buffer.Insert(doc.buffer.IterAtMark(doc.buffer.GetInsert()), "!")
	if got := bufferText(doc.buffer); got != "typed!" {
		t.Fatalf("typing with a completion visible changed real text to %q", got)
	}
	if doc.ghost.visible() || doc.ghostText != "" {
		t.Fatal("typing did not immediately remove the ghost completion")
	}

	setBufferText(doc, "prefix ")
	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	if !w.keyPressed(gdk.KEY_backslash, 0, gdk.AltMask) {
		t.Fatal("Alt+backslash did not trigger manual completion")
	}
	// Wait for the suggestion itself: earlier automatic requests may already
	// have set the status bar to this model.
	waitForUI(t, func() bool {
		return doc.ghostText == "completion" && w.activity.label.Text() == "Suggestion ready"
	})
	if doc.ghostText != "completion" || !doc.ghost.visible() {
		t.Fatalf("generated completion was not visible: text=%q visible=%v", doc.ghostText, doc.ghost.visible())
	}
	if len(w.activity.history) == 0 || w.activity.model.Text() != "AI: runtime" ||
		w.activity.label.Text() != "Suggestion ready" {
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

	setBufferText(doc, "fmt.Pr")
	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	doc.showCompletionEdit("intf", "Printf", 6, 4, 6)
	doc.acceptCompletion(false)
	if got := bufferText(doc.buffer); got != "fmt.Printf" {
		t.Fatalf("LSP replacement completion = %q", got)
	}
	setBufferText(doc, "😀x")
	if offset, ok := doc.offsetForLSPPosition(lsp.Position{Line: 0, Character: 2}); !ok || offset != 1 {
		t.Fatalf("UTF-16 LSP offset = %d, %v, want 1, true", offset, ok)
	}

	originalSave := w.saveFile
	started := make(chan string, 2)
	release := make(chan struct{}, 2)
	w.saveFile = func(request *saveRequest) (int64, error) {
		started <- string(request.prepared)
		<-release
		return time.Now().UnixNano(), nil
	}
	queuedPath := filepath.Join(root, "queued.txt")
	doc.path = queuedPath
	doc.modTime = 1
	doc.buffer.SetText("first")
	doc.save(queuedPath)
	if got := waitForString(t, started); got != "first" {
		t.Fatalf("first queued save = %q", got)
	}
	doc.buffer.SetText("second")
	doc.save(queuedPath)
	select {
	case got := <-started:
		t.Fatalf("second save started before first completed: %q", got)
	default:
	}
	release <- struct{}{}
	if got := waitForString(t, started); got != "second" {
		t.Fatalf("second queued save = %q", got)
	}
	release <- struct{}{}
	waitForUI(t, func() bool { return !doc.saveRunning && len(doc.saveQueue) == 0 })
	w.saveFile = originalSave

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
		"new", "open", "open-folder", "show-changes", "command-palette", "quick-open", "save", "save-as", "find", "replace",
		"markdown-preview", "ai-transform", "undo", "redo", "toggle-sidebar", "show-sidebar", "close-tab",
		"complete", "lsp-complete", "select-next", "select-all-occurrences", "next-tab", "previous-tab",
		"find-next", "find-previous", "preferences", "shortcuts", "about",
		"find-in-folder", "next-search-result", "previous-search-result",
	} {
		if action := w.win.LookupAction(name); action == nil {
			t.Errorf("window action %q is missing", name)
		}
	}
	for action, shortcut := range map[string]string{
		"new": "<Control>n", "command-palette": "<Control><Shift>p", "quick-open": "<Control>p",
		"open": "<Control>o", "show-changes": "<Control><Alt>d", "save": "<Control>s", "save-as": "<Control><Shift>s",
		"find": "<Control>f", "replace": "<Control>h", "markdown-preview": "<Control><Shift>v",
		"toggle-sidebar": "<Control>b",
		"show-sidebar":   "<Control><Shift>e", "close-tab": "<Control>w", "complete": "<Alt>backslash",
		"lsp-complete": "<Control>space", "select-next": "<Control>d",
		"select-all-occurrences": "<Control><Shift>l", "next-tab": "<Control>Tab",
		"previous-tab": "<Control><Shift>Tab", "find-next": "F3", "find-previous": "<Shift>F3",
		"preferences": "<Control>comma", "find-in-folder": "<Control><Shift>f",
		"next-search-result": "F4", "previous-search-result": "<Shift>F4",
	} {
		if got := w.app.AccelsForAction("win." + action); !containsAccelerator(got, shortcut) {
			t.Errorf("action %q accelerators = %v, want %q", action, got, shortcut)
		}
	}
	// Undo and redo belong to the focused widget, so entries keep their own.
	for _, action := range []string{"undo", "redo"} {
		if got := w.app.AccelsForAction("win." + action); len(got) != 0 {
			t.Errorf("action %q has global accelerators %v", action, got)
		}
	}
	for _, alias := range []struct{ action, shortcut string }{
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

// checkCompletionDoesNotRedrawText renders the real view with and without a
// suggestion. Only pixels right of the caret on the caret's line may change.
func checkCompletionDoesNotRedrawText(t *testing.T, doc *editorDocument) {
	t.Helper()
	view := doc.view.Widget
	view.SetCursorVisible(false)
	defer view.SetCursorVisible(true)
	setBufferText(doc, "Font rendering is a very\nsecond line stays put")
	cursor := doc.buffer.IterAtOffset(len("Font rendering is a very"))
	doc.buffer.PlaceCursor(cursor)
	waitForUI(t, func() bool { return view.Width() > 0 })
	before, stride := renderView(t, view)
	doc.showCompletion(" useful test", cursor.Offset())
	if !doc.ghost.visible() {
		t.Fatal("pixel regression requires a visible inline completion")
	}
	after, _ := renderView(t, view)
	caret, _ := view.CursorLocations(cursor)
	caretX, caretY := view.BufferToWindowCoords(gtk.TextWindowWidget, caret.X(), caret.Y())
	changed := false
	for index := range before {
		if before[index] == after[index] {
			continue
		}
		x, y := (index%stride)/4, index/stride
		if x < caretX || y < caretY || y >= caretY+caret.Height() {
			t.Fatalf("suggestion changed pixel (%d, %d) outside caret line right of x=%d", x, y, caretX)
		}
		changed = true
	}
	if !changed {
		t.Fatal("suggestion did not paint any pixels")
	}
	doc.dismissCompletion()
	if cleared, _ := renderView(t, view); !bytes.Equal(before, cleared) {
		t.Fatal("dismissing the suggestion left pixels behind")
	}
}

func checkEditingFixes(t *testing.T, doc *editorDocument) {
	t.Helper()
	// Ctrl+D right after a word selects that word, not the next one too.
	setBufferText(doc, "foo bar foo")
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(3))
	doc.selectNextOccurrence()
	if len(doc.multiCursors) != 2 || doc.multiCursors[0] != (multiCursor{0, 3}) || doc.multiCursors[1] != (multiCursor{8, 11}) {
		t.Fatalf("Ctrl+D at a word end = %v", doc.multiCursors)
	}
	doc.clearMultipleSelections()

	// Carets moved onto the same offset merge instead of typing twice.
	setBufferText(doc, "abc\nabc\nabc")
	doc.buffer.PlaceCursor(doc.buffer.StartIter())
	doc.addCursorVertically(1)
	doc.addCursorVertically(1)
	doc.moveMultiple(gdk.KEY_Home, false, true)
	doc.replaceMultiple("x")
	if got := bufferText(doc.buffer); got != "xabc\nabc\nabc" {
		t.Fatalf("merged carets typed %q", got)
	}
	doc.clearMultipleSelections()

	checkMultiCursorEditing(t, doc)

	// Typing the predicted text keeps the rest of the suggestion visible.
	setBufferText(doc, "typed")
	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	doc.showCompletion(" suggestion here", doc.buffer.CharCount())
	doc.buffer.InsertInteractiveAtCursor(" su", true)
	if doc.ghostText != "ggestion here" || !doc.ghost.visible() {
		t.Fatalf("type-through left ghost %q", doc.ghostText)
	}
	if !doc.keyPressed(gdk.KEY_Right, 0, gdk.ControlMask) || doc.ghostText != " here" {
		t.Fatalf("Ctrl+Right partial accept left ghost %q", doc.ghostText)
	}
	if got := bufferText(doc.buffer); got != "typed suggestion" {
		t.Fatalf("partial accept text = %q", got)
	}
	doc.dismissCompletion()

	// Replace moves past its own replacement even when it contains the query.
	setBufferText(doc, "a a")
	doc.search.update("a")
	waitForUI(t, func() bool { return len(doc.search.matches) == 2 })
	// The first press selects a match; each later one replaces and moves on.
	for range 3 {
		doc.search.replaceCurrent("ab")
		waitForUI(t, func() bool { return len(doc.search.matches) > 0 })
	}
	if got := bufferText(doc.buffer); got != "ab ab" {
		t.Fatalf("repeated Replace = %q", got)
	}
	doc.search.update("")

	// Save cleanup edits the buffer in place and keeps it in step with disk.
	text := "a  \nb\t\nc"
	setBufferText(doc, text)
	doc.applySaveCleanup([]byte(text), document.PrepareSave([]byte(text), true, true))
	if got := bufferText(doc.buffer); got != "a\nb\nc\n" {
		t.Fatalf("save cleanup = %q", got)
	}
}

func checkMultiCursorEditing(t *testing.T, doc *editorDocument) {
	t.Helper()
	key := func(keyval uint, state gdk.ModifierType) {
		t.Helper()
		if !doc.keyPressed(keyval, 0, state) {
			t.Fatalf("key %d with state %d was not handled", keyval, state)
		}
	}
	// Ctrl+Shift+Down keeps the column across a shorter line.
	setBufferText(doc, "abcdef\nab\nabcdef")
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(4))
	key(gdk.KEY_Down, gdk.ControlMask|gdk.ShiftMask)
	key(gdk.KEY_Down, gdk.ControlMask|gdk.ShiftMask)
	if len(doc.multiCursors) != 3 || doc.multiCursors[1].head != 9 || doc.multiCursors[2].head != 14 {
		t.Fatalf("vertical cursors = %v", doc.multiCursors)
	}
	key(gdk.KEY_u, gdk.ControlMask)
	if len(doc.multiCursors) != 2 {
		t.Fatalf("Ctrl+U left %d cursors", len(doc.multiCursors))
	}
	key(gdk.KEY_Escape, 0)
	if len(doc.multiCursors) != 0 {
		t.Fatal("Escape did not leave multi-cursor mode")
	}

	// Enter keeps each line's indentation; Tab with selections indents lines.
	setBufferText(doc, "  one\n\ttwo")
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(5))
	key(gdk.KEY_Down, gdk.ControlMask|gdk.ShiftMask)
	if len(doc.multiCursors) != 2 {
		t.Fatalf("cursors = %v", doc.multiCursors)
	}
	doc.multiCursors[1] = multiCursor{anchor: 10, head: 10}
	key(gdk.KEY_Return, 0)
	if got := bufferText(doc.buffer); got != "  one\n  \n\ttwo\n\t" {
		t.Fatalf("multi-cursor Enter = %q", got)
	}
	key(gdk.KEY_Escape, 0)
	setBufferText(doc, "a\nb")
	doc.multiCursors = []multiCursor{{0, 1}, {2, 3}}
	doc.renderMultipleSelections()
	key(gdk.KEY_Tab, 0)
	spaces, width := doc.indentation()
	unit := "\t"
	if spaces {
		unit = strings.Repeat(" ", width)
	}
	if got := bufferText(doc.buffer); got != unit+"a\n"+unit+"b" {
		t.Fatalf("multi-cursor Tab = %q", got)
	}
	key(gdk.KEY_ISO_Left_Tab, gdk.ShiftMask)
	if got := bufferText(doc.buffer); got != "a\nb" {
		t.Fatalf("multi-cursor Shift+Tab = %q", got)
	}
	key(gdk.KEY_Escape, 0)

	// Shift+Alt+drag makes one cursor per line. A drag happens on laid-out
	// text, so let GTK lay it out first.
	setBufferText(doc, "abc\nabc\nabc")
	waitFrames(t, doc.view.Widget, 2)
	first := doc.view.Widget.IterLocation(doc.buffer.IterAtOffset(1))
	last := doc.view.Widget.IterLocation(doc.buffer.IterAtOffset(10))
	doc.selectColumn(first.X(), first.Y(), last.X(), last.Y())
	if len(doc.multiCursors) != 3 || doc.multiCursors[2] != (multiCursor{9, 10}) {
		t.Fatalf("column selection = %v", doc.multiCursors)
	}
	doc.replaceMultiple("X")
	if got := bufferText(doc.buffer); got != "aXc\naXc\naXc" {
		t.Fatalf("column edit = %q", got)
	}

	// Secondary carets are drawn by the view, at the caret positions.
	view := doc.view.Widget
	view.SetCursorVisible(false)
	defer view.SetCursorVisible(true)
	withCarets, stride := renderView(t, view)
	doc.clearMultipleSelections()
	without, _ := renderView(t, view)
	caret := view.IterLocation(doc.buffer.IterAtOffset(2))
	caretX, caretY := view.BufferToWindowCoords(gtk.TextWindowWidget, caret.X(), caret.Y())
	drawn := false
	for index := range withCarets {
		if withCarets[index] != without[index] {
			x, y := (index%stride)/4, index/stride
			if x >= caretX-1 && x <= caretX+1 && y >= caretY && y < caretY+caret.Height() {
				drawn = true
			}
		}
	}
	if !drawn {
		t.Fatal("secondary caret was not drawn at its position")
	}
}

func waitFrames(t *testing.T, widget gtk.Widgetter, count int) {
	t.Helper()
	frames := 0
	gtk.BaseWidget(widget).AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		frames++
		return frames < count
	})
	waitForUI(t, func() bool { return frames >= count })
}

func renderView(t *testing.T, widget *gtk.TextView) ([]byte, int) {
	t.Helper()
	// Let pending text changes allocate before snapshotting.
	waitFrames(t, widget, 2)
	pixels, stride, err := gtktest.Render(widget)
	if err != nil {
		t.Fatal(err)
	}
	return pixels, stride
}

func checkCompletionLineEnds(t *testing.T, doc *editorDocument) {
	t.Helper()
	for _, offset := range []int{5, 6, 13} {
		setBufferText(doc, "first\n\nsecond")
		doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(offset))
		doc.showCompletion(" suggestion", offset)
		if !doc.ghost.visible() {
			t.Fatalf("line-end completion at %d was not shown", offset)
		}
		doc.dismissCompletion()
	}
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(5))
	doc.showCompletion("\nnext\nmore\nlast", 5)
	if doc.ghost.text != "↵ next  +2 lines" || bufferText(doc.buffer) != "first\n\nsecond" {
		t.Fatalf("leading line break shown as %q", doc.ghost.text)
	}
	doc.dismissCompletion()
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(2))
	doc.showCompletion("st", 2)
	if doc.ghost.visible() {
		t.Fatal("mid-line completion was shown")
	}
}

func checkCompletionLongLine(t *testing.T, doc *editorDocument) {
	t.Helper()
	wrap := doc.view.Widget.WrapMode()
	doc.view.Widget.SetWrapMode(gtk.WrapNone)
	text := strings.Repeat("x", 64*1024)
	setBufferText(doc, text)
	doc.buffer.PlaceCursor(doc.buffer.EndIter())
	doc.view.Widget.ScrollToMark(doc.buffer.GetInsert(), 0, false, 0, 0)
	doc.showCompletion(" suggestion", doc.buffer.CharCount())
	if doc.ghost.text != " suggestion" {
		t.Fatalf("long unwrapped line shown as %q", doc.ghost.text)
	}
	doc.acceptCompletion(false)
	if bufferText(doc.buffer) != text+" suggestion" {
		t.Fatal("long-line completion did not accept the original suggestion")
	}
	doc.view.Widget.SetWrapMode(wrap)
}

func checkCompletionResize(t *testing.T, doc *editorDocument) {
	t.Helper()
	win := doc.window.win
	width, height := win.Width(), win.Height()
	wrap := doc.view.Widget.WrapMode()
	doc.view.Widget.SetWrapMode(gtk.WrapWordChar)
	text := strings.Repeat("wrapped paragraph ", 40) + "\nnext paragraph"
	setBufferText(doc, text)
	lineEnd := strings.Index(text, "\n")
	doc.buffer.PlaceCursor(doc.buffer.IterAtOffset(lineEnd))
	doc.showCompletion(" suggestion\nnext", lineEnd)
	if !doc.ghost.visible() {
		t.Fatal("wrapped paragraph completion was not shown")
	}
	viewWidth := doc.view.Widget.Width()
	win.SetSizeRequest(width+200, height)
	waitForUI(t, func() bool { return doc.view.Widget.Width() != viewWidth && !doc.ghost.visible() })
	if doc.ghostText != "" || doc.completionText != "" || doc.ghost.tick != 0 {
		t.Fatal("resizing left a completion behind")
	}
	if doc.keyPressed(gdk.KEY_Tab, 0, 0) || bufferText(doc.buffer) != text {
		t.Fatal("Tab accepted an invisible completion after resizing")
	}
	win.SetSizeRequest(-1, -1)
	win.SetDefaultSize(width, height)
	doc.view.Widget.SetWrapMode(wrap)
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

func waitForString(t *testing.T, values <-chan string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	mainContext := glib.MainContextDefault()
	for time.Now().Before(deadline) {
		select {
		case value := <-values:
			return value
		default:
		}
		for mainContext.Pending() {
			mainContext.Iteration(false)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for string value")
	return ""
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
