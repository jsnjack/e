package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/activity"
	"github.com/jsnjack/e/internal/ai"
	"github.com/jsnjack/e/internal/dispatch"
	"github.com/jsnjack/e/internal/document"
	"github.com/jsnjack/e/internal/editorconfig"
	"github.com/jsnjack/e/internal/logging"
	"github.com/jsnjack/e/internal/lsp"
	"github.com/jsnjack/e/internal/markdownpreview"
	"github.com/jsnjack/e/internal/source"
)

const (
	loadChunkSize = 64 * 1024
	largeFileSize = 2 * 1024 * 1024
)

var documentSequence atomic.Uint64

type editorDocument struct {
	window         *window
	page           *adw.TabPage
	view           *source.View
	buffer         *gtk.TextBuffer
	ghost          *inlineCompletionRenderer
	search         *searchState
	split          *gtk.Paned
	preview        *markdownpreview.View
	previewVisible bool
	previewTimer   *time.Timer
	previewGen     uint64
	previewBusy    bool
	previewPending bool

	id      string
	path    string
	newline document.Newline
	bom     bool
	mode    os.FileMode
	modTime int64
	props   editorconfig.Properties
	loading bool
	// pending is the session state of a document that is still loading, so
	// a session save during loading keeps its recovery copy.
	pending     *document.Snapshot
	closed      bool
	revision    uint64
	bulkEdit    bool
	diagnostics int

	completionTimer  *time.Timer
	completionCancel context.CancelFunc
	transformCancel  context.CancelFunc
	transformGen     uint64
	completionGen    uint64
	ghostText        string
	ghostOffset      int
	completionText   string
	// typedThrough is text being inserted that matches the start of the
	// visible suggestion; the changed handler then shows the rest.
	typedThrough string
	replaceStart int
	replaceEnd   int

	saveRunning bool
	saveQueue   []*saveRequest

	lspClient  *lsp.Client
	lspURI     string
	lspLang    string
	lspSession string
	lspReady   bool
	lspOpening bool
	lspVer     int
	watching   bool
	diskWarn   bool

	multiCursors []multiCursor
	multiTag     *gtk.TextTag
	// verticalX is the column, in buffer pixels, kept while adding cursors
	// above or below; -1 when no vertical group is active.
	verticalX           int
	brandTag            *gtk.TextTag
	brandStyleSignals   []glib.SignalHandle
	brandScanGen        uint64
	brandRefreshPending bool
	brandRefreshStart   int
	brandRefreshEnd     int
}

type saveRequest struct {
	path            string
	text            []byte
	prepared        []byte
	newline         document.Newline
	bom             bool
	mode            os.FileMode
	props           editorconfig.Properties
	expectedModTime int64
	revision        uint64
	cursor          int
	force           bool
	after           func(bool)
}

type lspSession struct {
	key    string
	client *lsp.Client
	cancel context.CancelFunc
	docs   map[*editorDocument]bool
	ready  bool
}

func (w *window) newDocument(snapshot *document.Snapshot) *editorDocument {
	view, err := source.New()
	if err != nil {
		slog.Error("create source editor", "err", err)
		fallback := gtk.NewTextView()
		view = &source.View{Widget: fallback, Buffer: fallback.Buffer()}
		w.toast("Syntax highlighting is unavailable")
	}
	doc := &editorDocument{
		window: w, view: view, buffer: view.Buffer,
		id:      fmt.Sprintf("%d-%d", time.Now().UnixNano(), documentSequence.Add(1)),
		newline: document.NewlineLF,
	}
	doc.configureView()
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(view.Widget)
	scroll.SetHExpand(true)
	scroll.SetVExpand(true)
	doc.split = gtk.NewPaned(gtk.OrientationHorizontal)
	doc.split.SetStartChild(scroll)
	doc.split.SetResizeStartChild(true)
	doc.split.SetResizeEndChild(true)
	doc.split.SetShrinkStartChild(false)
	doc.split.SetShrinkEndChild(false)
	doc.split.SetWideHandle(true)
	doc.page = w.tabs.Append(doc.split)
	w.documents[doc.page] = doc
	w.tabs.SetSelectedPage(doc.page)
	doc.search = newSearchState(doc)
	doc.connectSignals()

	if snapshot != nil {
		doc.id = snapshot.ID
		doc.path = snapshot.Path
		doc.newline = snapshot.Newline
		pending := *snapshot
		doc.pending = &pending
		if len(snapshot.Text) > 0 || snapshot.Modified || snapshot.Path == "" {
			savedModTime := snapshot.ModTime
			doc.insertChunks(snapshot.Text, snapshot.Modified, snapshot.Cursor, nil, func(complete bool) {
				if complete && doc.path != "" {
					doc.attachRestoredFile(savedModTime)
				}
			})
		} else {
			doc.load(snapshot.Path, snapshot.Cursor)
		}
	} else {
		doc.buffer.SetModified(false)
		doc.updatePresentation()
	}
	view.Widget.GrabFocus()
	return doc
}

func (d *editorDocument) configureView() {
	cfg := d.window.options.Config.Editor
	d.view.SetOptions(cfg.LineNumbers, true, cfg.InsertSpaces, cfg.TabWidth, -1)
	d.view.Widget.SetMonospace(true)
	d.view.Widget.SetLeftMargin(12)
	d.view.Widget.SetRightMargin(12)
	d.view.Widget.SetTopMargin(12)
	d.view.Widget.SetBottomMargin(24)
	d.view.Widget.SetWrapMode(gtk.WrapWordChar)
	if !cfg.WordWrap {
		d.view.Widget.SetWrapMode(gtk.WrapNone)
	}
	d.view.Widget.AddCSSClass("editor")
	d.buffer.SetEnableUndo(true)
	d.view.EnableSpelling(cfg.SpellCheck)

	d.ghost = newInlineCompletionRenderer(d.view.Widget, d.view, d.tabWidth, d.dismissCompletion)
	d.configureBranding()

	d.multiTag = gtk.NewTextTag("e-multiple-selection")
	d.multiTag.SetObjectProperty("background", "#3584e455")
	d.buffer.TagTable().Add(d.multiTag)
	d.connectPointerSelection()
}

func (d *editorDocument) connectSignals() {
	d.buffer.ConnectModifiedChanged(d.updatePresentation)
	d.buffer.ConnectChanged(func() {
		if d.loading || d.closed {
			return
		}
		d.revision++
		d.window.scheduleSessionSave()
		if d.transformCancel != nil {
			d.transformCancel()
			d.transformCancel = nil
		}
		if d.bulkEdit {
			return
		}
		if len(d.multiCursors) > 0 {
			d.clearMultipleSelections()
		}
		if !d.continueCompletion() {
			d.dismissCompletion()
			d.scheduleCompletion()
		}
		d.scheduleMarkdownPreview()
		if d.search != nil && d.search.query != "" && !d.search.replacing {
			d.search.scheduleRefresh()
		}
	})
	d.buffer.ConnectMarkSet(func(_ *gtk.TextIter, mark *gtk.TextMark) {
		// Signal parameters and GetInsert can be separate Go wrappers around the
		// same native GtkTextMark. The built-in insertion mark has the stable
		// GTK name "insert"; comparing wrapper pointers leaves stale overlays
		// behind after keyboard navigation.
		if mark != nil && mark.Name() == "insert" {
			d.dismissCompletion()
		}
	})
	// Typing moves the caret by mark gravity, which emits no mark-set, so the
	// status bar position follows cursor-position instead.
	d.buffer.NotifyProperty("cursor-position", func() {
		if d.window.active() == d {
			d.window.updateTitle(d)
		}
	})
	d.buffer.ConnectInsertText(func(location *gtk.TextIter, text string, _ int) {
		d.typedThrough = ""
		if d.ghostText != "" && !d.loading && !d.bulkEdit && text != "" && location.Offset() == d.ghostOffset &&
			d.replaceStart == d.replaceEnd && strings.HasPrefix(d.completionText, text) {
			d.typedThrough = text
		}
		if !d.loading {
			d.scheduleBrandRange(location.Offset()-1, location.Offset()+utf8.RuneCountInString(text)+1)
		}
		if d.loading || !d.lspReady {
			return
		}
		d.lspVer++
		position := d.lspPosition(location)
		if err := d.lspClient.Change(d.lspURI, d.lspVer, []lsp.Change{{Range: &lsp.Range{Start: position, End: position}, Text: text}}); err != nil {
			slog.Debug("LSP insert change", "err", err)
		}
	})
	d.buffer.ConnectDeleteRange(func(start, end *gtk.TextIter) {
		if !d.loading {
			d.scheduleBrandRange(start.Offset()-1, start.Offset()+2)
		}
		if d.loading || !d.lspReady {
			return
		}
		d.lspVer++
		change := lsp.Change{Range: &lsp.Range{Start: d.lspPosition(start), End: d.lspPosition(end)}, Text: ""}
		if err := d.lspClient.Change(d.lspURI, d.lspVer, []lsp.Change{change}); err != nil {
			slog.Debug("LSP delete change", "err", err)
		}
	})

	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(d.keyPressed)
	d.view.Widget.AddController(keys)
}

func (d *editorDocument) keyPressed(keyval, _ uint, state gdk.ModifierType) bool {
	// Ctrl+Shift+Up/Down (VS Code on Linux) and Shift+Alt+Up/Down add cursors.
	modifiers := state & (gdk.ShiftMask | gdk.AltMask | gdk.ControlMask | gdk.SuperMask)
	if modifiers == gdk.ShiftMask|gdk.AltMask || modifiers == gdk.ShiftMask|gdk.ControlMask {
		switch keyval {
		case gdk.KEY_Up:
			d.addCursorVertically(-1)
			return true
		case gdk.KEY_Down:
			d.addCursorVertically(1)
			return true
		}
	}
	if len(d.multiCursors) > 1 && d.editMultiple(keyval, state) {
		return true
	}
	switch keyval {
	case gdk.KEY_Escape:
		if d.ghostText != "" {
			d.dismissCompletion()
			return true
		}
		d.clearMultipleSelections()
	case gdk.KEY_Tab:
		if d.ghostText != "" {
			d.acceptCompletion(false)
			return true
		}
	case gdk.KEY_y:
		// GtkTextView binds only Ctrl+Shift+Z for redo.
		if state&(gdk.ControlMask|gdk.ShiftMask|gdk.AltMask) == gdk.ControlMask {
			if d.buffer.CanRedo() {
				d.buffer.Redo()
			}
			return true
		}
	case gdk.KEY_Right:
		if d.ghostText != "" && state&gdk.ControlMask != 0 {
			d.acceptCompletion(true)
			return true
		}
	}
	return false
}

func (d *editorDocument) title() string {
	if d.path == "" {
		return "Untitled"
	}
	return filepath.Base(d.path)
}

func (d *editorDocument) updatePresentation() {
	title := d.title()
	if d.buffer.Modified() {
		title += " •"
	}
	d.page.SetTitle(title)
	d.page.SetTooltip(d.path)
	if d.window.active() == d {
		d.window.updateTitle(d)
	}
}

func (d *editorDocument) load(path string, cursor int) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if d.pending == nil {
		d.pending = &document.Snapshot{ID: d.id, Path: path, Title: filepath.Base(path), Cursor: cursor, Newline: d.newline}
	}
	d.loading = true
	d.view.Widget.SetEditable(false)
	d.page.SetLoading(true)
	d.page.SetTitle(filepath.Base(path))
	operation := d.window.activityHub.Begin(activity.File, "Opening "+filepath.Base(path))
	go func() {
		file, err := document.Open(path)
		var props editorconfig.Properties
		if err == nil {
			props, err = editorconfig.Resolve(file.Path)
		}
		dispatch.Main(func() {
			if d.closed {
				operation.Cancel()
				return
			}
			if err != nil {
				d.loading = false
				d.pending = nil
				d.view.Widget.SetEditable(true)
				d.page.SetLoading(false)
				if errors.Is(err, fs.ErrNotExist) {
					// A missing path opens as a new, empty file with that name.
					d.path, d.modTime = path, 0
					d.view.SetLanguage(path, "")
					d.updatePresentation()
					operation.Finish("new file", "", false)
					return
				}
				operation.Fail(err)
				slog.Warn("open document", "path", path, "err", err)
				d.window.toast(fmt.Sprintf("Couldn’t open “%s”", filepath.Base(path)))
				if d.buffer.CharCount() > 0 {
					// A failed reload keeps the previous text and stops retrying.
					d.diskWarn = true
					return
				}
				// Never keep a path whose content was not loaded: saving would
				// replace the real file with an empty buffer.
				d.path = ""
				d.window.tabs.ClosePage(d.page)
				return
			}
			d.path, d.newline, d.bom, d.mode, d.modTime, d.props = file.Path, file.Newline, file.BOM, file.Mode, file.ModTime, props
			d.diskWarn = false
			d.insertChunks(file.Text, false, cursor, func(current, total int) {
				operation.Progress(int64(current), int64(total))
			}, func(complete bool) {
				if !complete {
					operation.Cancel()
					return
				}
				operation.Finish(humanByteCount(len(file.Text)), "", false)
				d.startLSP()
				d.startExternalWatch()
			})
		})
	}()
}

func (d *editorDocument) insertChunks(data []byte, modified bool, cursor int, progress func(int, int), done func(bool)) {
	d.loading = true
	d.view.Widget.SetEditable(false)
	d.page.SetLoading(true)
	d.view.SetHighlighting(false)
	d.buffer.BeginIrreversibleAction()
	d.buffer.SetText("")
	offset := 0
	glib.IdleAdd(func() bool {
		if d.closed {
			d.buffer.EndIrreversibleAction()
			if done != nil {
				done(false)
			}
			return false
		}
		if offset < len(data) {
			end := min(offset+loadChunkSize, len(data))
			for end > offset && end < len(data) && !utf8.RuneStart(data[end]) {
				end--
			}
			d.buffer.Insert(d.buffer.EndIter(), string(data[offset:end]))
			offset = end
			if progress != nil {
				progress(offset, len(data))
			}
			return true
		}
		d.buffer.EndIrreversibleAction()
		d.applyEditorConfig()
		d.view.SetLanguage(d.path, "")
		d.view.SetHighlighting(true)
		d.scanBranding()
		d.buffer.SetModified(modified)
		d.buffer.PlaceCursor(d.buffer.IterAtOffset(min(cursor, d.buffer.CharCount())))
		d.loading = false
		d.pending = nil
		d.view.Widget.SetEditable(true)
		d.page.SetLoading(false)
		d.updatePresentation()
		d.scheduleMarkdownPreview()
		if done != nil {
			done(true)
		}
		return false
	})
}

// attachRestoredFile reads the metadata of a file whose unsaved text came from
// the session. A savedModTime that differs from the disk makes the next save
// ask before overwriting changes made while E was closed.
func (d *editorDocument) attachRestoredFile(savedModTime int64) {
	path := d.path
	go func() {
		info, statErr := os.Stat(path)
		bom := false
		if statErr == nil {
			if file, err := os.Open(path); err == nil {
				head := make([]byte, 3)
				n, _ := io.ReadFull(file, head)
				bom = bytes.Equal(head[:n], []byte{0xef, 0xbb, 0xbf})
				if err := file.Close(); err != nil {
					logging.Trace("close restored file", "path", path, "err", err)
				}
			}
		}
		props, propsErr := editorconfig.Resolve(path)
		dispatch.Main(func() {
			if d.closed || d.path != path {
				return
			}
			if statErr == nil {
				d.mode = info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
				d.bom = bom
				d.modTime = savedModTime
				if d.modTime == 0 {
					d.modTime = info.ModTime().UnixNano()
				}
			}
			if propsErr == nil {
				d.props = props
				d.applyEditorConfig()
			}
			d.startLSP()
			d.startExternalWatch()
		})
	}()
}

func (d *editorDocument) textSnapshotAsync(valid func() bool, done func(string)) {
	total := d.buffer.CharCount()
	offset := 0
	var text strings.Builder
	text.Grow(total)
	glib.IdleAdd(func() bool {
		if d.closed || !valid() {
			return false
		}
		if offset < total {
			end := min(offset+loadChunkSize, total)
			text.WriteString(d.buffer.Text(d.buffer.IterAtOffset(offset), d.buffer.IterAtOffset(end), true))
			offset = end
			return true
		}
		done(text.String())
		return false
	})
}

// indentation returns whether Tab inserts spaces, and the indent width in
// columns, after EditorConfig overrides.
func (d *editorDocument) indentation() (spaces bool, width int) {
	spaces = d.window.options.Config.Editor.InsertSpaces
	if d.props.IndentStyle != "" {
		spaces = d.props.IndentStyle == "space"
	}
	width = d.tabWidth()
	if d.props.IndentSize > 0 {
		width = d.props.IndentSize
	}
	return spaces, width
}

func (d *editorDocument) applyEditorConfig() {
	indentWidth := -1
	if d.props.IndentSize > 0 {
		indentWidth = d.props.IndentSize
	}
	spaces, _ := d.indentation()
	d.view.SetOptions(d.window.options.Config.Editor.LineNumbers, true, spaces, d.tabWidth(), indentWidth)
	d.newline = newlineFromConfig(d.props.EndOfLine, d.newline)
	d.bom = bomFromConfig(d.props.Charset, d.bom)
}

// bomFromConfig applies an EditorConfig charset to the file's BOM; other
// charsets and an unset value keep what the file had.
func bomFromConfig(charset string, current bool) bool {
	switch charset {
	case "utf-8-bom":
		return true
	case "utf-8":
		return false
	}
	return current
}

func (d *editorDocument) save(path string) { d.saveThen(path, nil) }

func (d *editorDocument) saveThen(path string, after func(bool)) {
	if d.loading {
		// The buffer holds only part of the file until loading finishes.
		d.window.toast("Wait for the document to finish loading")
		if after != nil {
			after(false)
		}
		return
	}
	if path != d.path {
		go func() {
			props, err := editorconfig.Resolve(path)
			dispatch.Main(func() {
				if d.closed {
					if after != nil {
						after(false)
					}
					return
				}
				if err != nil {
					slog.Debug("resolve EditorConfig before Save As", "path", path, "err", err)
					props = editorconfig.Properties{}
				}
				d.queueSave(path, props, after)
			})
		}()
		return
	}
	d.queueSave(path, d.props, after)
}

func (d *editorDocument) queueSave(path string, props editorconfig.Properties, after func(bool)) {
	start, end := d.buffer.Bounds()
	text := []byte(d.buffer.Text(start, end, true))
	newline := newlineFromConfig(props.EndOfLine, d.newline)
	bom := bomFromConfig(props.Charset, d.bom)
	expectedModTime := int64(0)
	if path == d.path {
		expectedModTime = d.modTime
	}
	d.saveQueue = append(d.saveQueue, &saveRequest{
		path: path, text: text,
		newline: newline, bom: bom, mode: d.mode, props: props, expectedModTime: expectedModTime,
		revision: d.revision, cursor: d.buffer.IterAtMark(d.buffer.GetInsert()).Offset(),
		after: after,
	})
	d.runNextSave()
}

func (d *editorDocument) runNextSave() {
	if d.saveRunning || len(d.saveQueue) == 0 || d.closed {
		return
	}
	request := d.saveQueue[0]
	d.saveQueue = d.saveQueue[1:]
	d.saveRunning = true
	d.page.SetLoading(true)
	operation := d.window.activityHub.Begin(activity.File, "Saving "+filepath.Base(request.path))
	go func() {
		request.prepared = document.PrepareSave(request.text,
			boolOr(request.props.TrimTrailingWhitespace, false), boolOr(request.props.InsertFinalNewline, false))
		modTime, err := d.window.saveFile(request)
		dispatch.Main(func() {
			d.saveRunning = false
			if d.closed {
				operation.Cancel()
				if request.after != nil {
					request.after(false)
				}
				return
			}
			if errors.Is(err, document.ErrChangedOnDisk) {
				operation.Fail(err)
				d.confirmOverwrite(request)
				return
			}
			if err != nil {
				slog.Warn("save document", "path", request.path, "err", err)
				operation.Fail(err)
				if request.after != nil {
					request.after(false)
				}
				d.page.SetLoading(len(d.saveQueue) > 0)
				d.runNextSave()
				return
			}
			oldPath := d.path
			d.modTime = modTime
			if oldPath != request.path {
				d.applyPathChange(request.path, request.props)
			} else {
				d.path = request.path
			}
			d.diskWarn = false
			for _, queued := range d.saveQueue {
				if queued.path == request.path && !queued.force {
					queued.expectedModTime = modTime
				}
			}
			savedCurrentRevision := d.revision == request.revision
			if savedCurrentRevision {
				if !bytes.Equal(request.text, request.prepared) {
					d.applySaveCleanup(request.text, request.prepared)
				}
				d.buffer.SetModified(false)
			}
			d.view.SetLanguage(request.path, "")
			d.updatePresentation()
			operation.Finish(humanByteCount(len(request.prepared)), "", false)
			if d.lspClient == nil {
				d.startLSP()
			} else if oldPath == request.path && d.lspReady {
				if err := d.lspClient.Save(d.lspURI); err != nil {
					slog.Debug("LSP save", "err", err)
				}
			}
			d.startExternalWatch()
			if request.after != nil {
				request.after(savedCurrentRevision)
			}
			d.page.SetLoading(len(d.saveQueue) > 0)
			d.runNextSave()
		})
	}()
}

// applySaveCleanup brings the buffer in line with the saved text as small
// edits, so undo, LSP, search, and branding see each change. PrepareSave only
// removes trailing spaces and tabs per line and may add a final newline.
func (d *editorDocument) applySaveCleanup(text, prepared []byte) {
	type cut struct{ start, end int }
	var cuts []cut
	offset := 0
	savedLines := bytes.Split(prepared, []byte("\n"))
	for index, line := range bytes.Split(text, []byte("\n")) {
		length := utf8.RuneCount(line)
		if index < len(savedLines) && len(savedLines[index]) < len(line) {
			cuts = append(cuts, cut{offset + utf8.RuneCount(savedLines[index]), offset + length})
		}
		offset += length + 1
	}
	d.buffer.BeginUserAction()
	defer d.buffer.EndUserAction()
	for index := len(cuts) - 1; index >= 0; index-- {
		d.buffer.Delete(d.buffer.IterAtOffset(cuts[index].start), d.buffer.IterAtOffset(cuts[index].end))
	}
	if len(prepared) > 0 && prepared[len(prepared)-1] == '\n' {
		if end := d.buffer.EndIter(); !end.BackwardChar() || end.Char() != '\n' {
			d.buffer.Insert(d.buffer.EndIter(), "\n")
		}
	}
}

func (d *editorDocument) changePath(path string) {
	d.applyPathChange(path, editorconfig.Properties{})
	go func() {
		props, err := editorconfig.Resolve(path)
		dispatch.Main(func() {
			if d.closed || d.path != path {
				return
			}
			if err != nil {
				slog.Debug("resolve EditorConfig after path change", "path", path, "err", err)
				return
			}
			d.props = props
			d.applyEditorConfig()
		})
	}()
}

func (d *editorDocument) applyPathChange(path string, props editorconfig.Properties) {
	d.stopLSP()
	d.path = path
	d.props = props
	d.applyEditorConfig()
	d.view.SetLanguage(path, "")
	d.updatePresentation()
	d.startLSP()
}

func (d *editorDocument) confirmOverwrite(request *saveRequest) {
	dialog := adw.NewAlertDialog(
		"File changed on disk",
		fmt.Sprintf("“%s” was changed by another program. Saving will overwrite those changes.", filepath.Base(request.path)),
	)
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("overwrite", "Save Anyway")
	dialog.SetCloseResponse("cancel")
	dialog.SetResponseAppearance("overwrite", adw.ResponseDestructive)
	dialog.ConnectResponse(func(response string) {
		if response == "overwrite" && !d.closed {
			request.force = true
			for _, queued := range d.saveQueue {
				if queued.path == request.path {
					queued.force = true
				}
			}
			d.saveQueue = append([]*saveRequest{request}, d.saveQueue...)
		} else if request.after != nil {
			request.after(false)
		}
		d.page.SetLoading(len(d.saveQueue) > 0)
		d.runNextSave()
	})
	dialog.Present(d.window.win)
}

func (d *editorDocument) snapshot() document.Snapshot {
	if d.loading && d.pending != nil {
		return *d.pending
	}
	snapshot := document.Snapshot{
		ID: d.id, Path: d.path, Title: d.title(), Modified: d.buffer.Modified(),
		Cursor: d.buffer.IterAtMark(d.buffer.GetInsert()).Offset(), Newline: d.newline, ModTime: d.modTime,
	}
	// SaveSession writes text only for unsaved or untitled documents.
	if snapshot.Modified || snapshot.Path == "" {
		start, end := d.buffer.Bounds()
		snapshot.Text = []byte(d.buffer.Text(start, end, true))
	}
	return snapshot
}

func (d *editorDocument) scheduleCompletion() {
	if d.window.completer == nil || !d.window.options.Config.AI.Enabled || !d.window.options.Config.AI.Consented || len(d.multiCursors) > 1 {
		return
	}
	if d.completionTimer != nil {
		d.completionTimer.Stop()
	}
	d.completionGen++
	gen := d.completionGen
	delay := time.Duration(d.window.options.Config.AI.DebounceMS) * time.Millisecond
	d.completionTimer = time.AfterFunc(delay, func() {
		dispatch.Main(func() {
			if !d.closed && gen == d.completionGen {
				d.requestCompletion(false)
			}
		})
	})
}

func (d *editorDocument) requestCompletion(manual bool) {
	logging.Trace("ui: completion requested", "manual", manual, "configured", d.window.completer != nil,
		"automatic", d.window.options.Config.AI.Enabled, "consented", d.window.options.Config.AI.Consented)
	// Cheap checks run before the activity starts, so automatic requests that
	// send nothing do not flash in the status bar. Only manual ones report why.
	skip := func(reason string) {
		if manual {
			d.window.activityHub.Begin(activity.AI, "Generating suggestion").Fail(errors.New(reason))
		}
	}
	if d.closed {
		return
	}
	if d.window.completer == nil {
		skip("no AI autocomplete models are configured")
		return
	}
	if d.loading {
		skip("document is still loading")
		return
	}
	if _, _, selected := d.buffer.SelectionBounds(); selected {
		skip("move the caret to request a suggestion")
		return
	}
	cursor := d.buffer.IterAtMark(d.buffer.GetInsert())
	offset := cursor.Offset()
	if !manual && offset == 0 {
		return
	}
	// Suggestions are drawn only beside the caret at a line end.
	if !cursor.EndsLine() {
		skip("move the caret to the end of a line")
		return
	}
	maxPrefix := d.window.options.Config.AI.PrefixSize
	maxSuffix := d.window.options.Config.AI.SuffixSize
	start := d.buffer.IterAtOffset(max(0, offset-maxPrefix))
	end := d.buffer.IterAtOffset(min(d.buffer.CharCount(), offset+maxSuffix))
	req := ai.Request{
		Prefix: d.buffer.Text(start, cursor, true), Suffix: d.buffer.Text(cursor, end, true),
		Filename: filepath.Base(d.path), Language: languageForPath(d.path),
	}
	if strings.TrimSpace(req.Prefix) == "" && !manual {
		return
	}
	operation := d.window.activityHub.Begin(activity.AI, "Generating suggestion")
	if d.completionCancel != nil {
		d.completionCancel()
	}
	d.completionGen++
	gen := d.completionGen
	ctx, cancel := context.WithCancel(d.window.ctx)
	d.completionCancel = cancel
	completer := d.window.completer
	requestedAt := time.Now()
	go func() {
		completion, err := completer.CompleteDetailed(ctx, req)
		if errors.Is(err, context.Canceled) {
			// Typing cancelled it; that is not a failure.
			operation.Cancel()
			return
		}
		if err != nil {
			operation.Fail(err)
			slog.Debug("AI completion", "duration", time.Since(requestedAt), "err", err)
			return
		}
		logging.Trace("ui: completion returned", "model", completion.Model, "characters", utf8.RuneCountInString(completion.Text),
			"cached", completion.Cached, "fallback", completion.Fallback, "duration", time.Since(requestedAt))
		dispatch.Main(func() {
			if d.closed || gen != d.completionGen || d.buffer.IterAtMark(d.buffer.GetInsert()).Offset() != offset {
				operation.Cancel()
				return
			}
			d.showCompletion(completion.Text, offset)
			note := ""
			if completion.Cached {
				note = "cached"
			} else if completion.Text == "" {
				note = "no suggestion"
			}
			operation.Finish(note, completion.Model, completion.Fallback)
		})
	}()
}

func (d *editorDocument) showCompletion(text string, offset int) {
	text = inlineCompletionText(text)
	d.showCompletionEdit(text, text, offset, offset, offset)
}

func (d *editorDocument) showCompletionEdit(display, insertion string, offset, replaceStart, replaceEnd int) {
	display = inlineCompletionText(display)
	insertion = inlineCompletionText(insertion)
	if display == "" || replaceStart < 0 || replaceEnd < replaceStart || replaceEnd > d.buffer.CharCount() {
		return
	}
	logging.Trace("ui: show completion", "active", d.window.win.IsActive(),
		"sensitive", d.window.content.IsSensitive(), "preferences", d.window.preferences != nil,
		"characters", utf8.RuneCountInString(display), "lines", strings.Count(display, "\n")+1)
	if !d.ghost.show(display, d.buffer.IterAtOffset(offset)) {
		d.ghostText = ""
		return
	}
	d.ghostText, d.ghostOffset = display, offset
	d.completionText = insertion
	d.replaceStart, d.replaceEnd = replaceStart, replaceEnd
	logging.Trace("ui: completion visible", "offset", offset)
}

func (d *editorDocument) dismissCompletion() {
	d.completionGen++
	if d.completionCancel != nil {
		d.completionCancel()
		d.completionCancel = nil
	}
	d.ghostText = ""
	d.completionText = ""
	d.replaceStart, d.replaceEnd = 0, 0
	if d.ghost != nil {
		d.ghost.hide()
	}
}

// continueCompletion keeps a suggestion while the user types the text it
// predicted: the rest is shown at once, with no new request.
func (d *editorDocument) continueCompletion() bool {
	typed := d.typedThrough
	d.typedThrough = ""
	if typed == "" || d.ghostText == "" {
		return false
	}
	rest := strings.TrimPrefix(d.completionText, typed)
	offset := d.ghostOffset + utf8.RuneCountInString(typed)
	d.dismissCompletion()
	if rest == "" || d.buffer.IterAtMark(d.buffer.GetInsert()).Offset() != offset {
		// Fully typed or moved away: request a fresh suggestion as usual.
		return false
	}
	d.showCompletion(rest, offset)
	return d.ghostText != ""
}

func (d *editorDocument) acceptCompletion(oneWord bool) {
	text := d.completionText
	if oneWord && d.replaceEnd == d.replaceStart {
		// Insert the next word in place; continueCompletion keeps the rest
		// of the suggestion visible.
		word := completionWord(text)
		if word == "" {
			return
		}
		start := d.replaceStart
		d.buffer.BeginUserAction()
		d.buffer.Insert(d.buffer.IterAtOffset(start), word)
		d.buffer.EndUserAction()
		d.applyBrandRange(start-1, start+utf8.RuneCountInString(word)+1)
		return
	}
	if oneWord {
		if d.replaceEnd > d.replaceStart {
			existing := d.buffer.Text(d.buffer.IterAtOffset(d.replaceStart), d.buffer.IterAtOffset(d.replaceEnd), true)
			if strings.HasPrefix(text, existing) {
				text = existing + completionWord(strings.TrimPrefix(text, existing))
			}
		} else {
			text = completionWord(text)
		}
	}
	if text == "" {
		return
	}
	start, end := d.replaceStart, d.replaceEnd
	d.dismissCompletion()
	d.buffer.BeginUserAction()
	if end > start {
		d.buffer.Delete(d.buffer.IterAtOffset(start), d.buffer.IterAtOffset(end))
	}
	d.buffer.Insert(d.buffer.IterAtOffset(start), text)
	d.buffer.EndUserAction()
	// Style accepted text now so it does not change color a frame later.
	d.applyBrandRange(start-1, start+utf8.RuneCountInString(text)+1)
}

func (d *editorDocument) tabWidth() int {
	if d.props.TabWidth > 0 {
		return d.props.TabWidth
	}
	return d.window.options.Config.Editor.TabWidth
}

func completionWord(text string) string {
	for index, r := range text {
		if index > 0 && (r == ' ' || r == '\t' || r == '\n' || strings.ContainsRune(".,;:()[]{}", r)) {
			return text[:index]
		}
	}
	return text
}

func (d *editorDocument) startLSP() {
	if d.path == "" || d.lspClient != nil || d.buffer.CharCount() > largeFileSize {
		return
	}
	language := languageForPath(d.path)
	command, args, ok := lsp.CommandFor(language, d.window.options.Config.LSP)
	if !ok {
		return
	}
	root := filepath.Dir(d.path)
	if d.window.projectRoot != "" {
		if relative, err := filepath.Rel(d.window.projectRoot, d.path); err == nil &&
			relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			root = d.window.projectRoot
		}
	}
	key := strings.Join(append([]string{root, language, command}, args...), "\x00")
	session := d.window.lspSessions[key]
	if session == nil {
		ctx, cancel := context.WithCancel(d.window.ctx)
		session = &lspSession{
			key: key, client: lsp.New(command, args, root, d.window.setDiagnostics),
			cancel: cancel, docs: make(map[*editorDocument]bool),
		}
		d.window.lspSessions[key] = session
		go func() {
			err := session.client.Start(ctx)
			dispatch.Main(func() {
				current := d.window.lspSessions[key]
				if current != session {
					return
				}
				if err != nil {
					slog.Debug("start LSP", "language", language, "err", err)
					for doc := range session.docs {
						doc.lspClient = nil
						doc.lspSession = ""
						doc.lspOpening = false
					}
					delete(d.window.lspSessions, key)
					return
				}
				session.ready = true
				for doc := range session.docs {
					doc.openLSPDocument()
				}
			})
		}()
	}
	session.docs[d] = true
	d.lspClient = session.client
	d.lspSession = key
	d.lspURI, d.lspLang = lsp.FileURI(d.path), language
	if session.ready {
		d.openLSPDocument()
	}
}

func (d *editorDocument) openLSPDocument() {
	if d.closed || d.lspClient == nil || d.lspReady || d.lspOpening {
		return
	}
	d.lspOpening = true
	client, sessionKey, revision := d.lspClient, d.lspSession, d.revision
	d.textSnapshotAsync(func() bool {
		return !d.closed && d.lspClient == client && d.lspSession == sessionKey
	}, func(text string) {
		d.lspOpening = false
		if d.revision != revision {
			d.openLSPDocument()
			return
		}
		if err := client.Open(d.lspURI, d.lspLang, 1, text); err != nil {
			slog.Debug("open LSP document", "language", d.lspLang, "err", err)
			return
		}
		d.lspVer = 1
		d.lspReady = true
	})
}

func (d *editorDocument) requestLSPCompletion() {
	if !d.lspReady {
		d.window.toast("No language server is available for this file")
		return
	}
	iter := d.buffer.IterAtMark(d.buffer.GetInsert())
	position := d.lspPosition(iter)
	offset := iter.Offset()
	word := d.wordBeforeCaret(iter)
	// A new request replaces any pending AI or LSP suggestion request.
	d.dismissCompletion()
	gen := d.completionGen
	ctx, cancel := context.WithTimeout(d.window.ctx, 2*time.Second)
	d.completionCancel = cancel
	client, uri := d.lspClient, d.lspURI
	go func() {
		defer cancel()
		items, err := client.Completions(ctx, uri, position)
		if err != nil {
			slog.Debug("LSP completion", "err", err)
			return
		}
		item, ok := bestCompletion(items, word)
		if !ok {
			return
		}
		dispatch.Main(func() {
			if d.closed || gen != d.completionGen || d.buffer.IterAtMark(d.buffer.GetInsert()).Offset() != offset {
				return
			}
			if !d.buffer.IterAtOffset(offset).EndsLine() {
				d.window.toast("Completions show only at the end of a line")
				return
			}
			insertion := item.InsertText
			if insertion == "" {
				insertion = item.Label
			}
			display := insertion
			start, end := offset, offset
			if word != "" && strings.HasPrefix(insertion, word) {
				// The server returned the whole word; replace what is typed.
				start = offset - utf8.RuneCountInString(word)
				display = strings.TrimPrefix(insertion, word)
			}
			if item.TextEdit != nil {
				var ok bool
				start, ok = d.offsetForLSPPosition(item.TextEdit.Range.Start)
				if !ok {
					return
				}
				end, ok = d.offsetForLSPPosition(item.TextEdit.Range.End)
				if !ok || end != offset || start > end {
					return
				}
				insertion = item.TextEdit.NewText
				existing := d.buffer.Text(d.buffer.IterAtOffset(start), d.buffer.IterAtOffset(end), true)
				display = insertion
				if strings.HasPrefix(insertion, existing) {
					display = strings.TrimPrefix(insertion, existing)
				}
			}
			d.showCompletionEdit(display, insertion, offset, start, end)
		})
	}()
}

// wordBeforeCaret returns the identifier characters right before iter.
func (d *editorDocument) wordBeforeCaret(iter *gtk.TextIter) string {
	start := iter.Copy()
	for {
		previous := start.Copy()
		if !previous.BackwardChar() || !isIdentifierRune(rune(previous.Char())) {
			break
		}
		start = previous
	}
	return d.buffer.Text(start, iter, true)
}

// bestCompletion picks the first item, in server order, whose filter text
// starts with the typed word, ignoring case. Servers often return items that
// do not match what is typed.
func bestCompletion(items []lsp.Completion, word string) (lsp.Completion, bool) {
	lowered := strings.ToLower(word)
	for _, item := range items {
		key := item.FilterText
		if key == "" {
			key = item.Label
		}
		if strings.HasPrefix(strings.ToLower(key), lowered) {
			return item, true
		}
	}
	return lsp.Completion{}, false
}

func (d *editorDocument) offsetForLSPPosition(position lsp.Position) (int, bool) {
	start, ok := d.buffer.IterAtLine(position.Line)
	if !ok || position.Character < 0 {
		return 0, false
	}
	end := start.Copy()
	end.ForwardToLineEnd()
	line := d.buffer.Text(start, end, true)
	units := 0
	runes := 0
	for _, value := range line {
		if units == position.Character {
			return start.Offset() + runes, true
		}
		width := 1
		if value > 0xffff {
			width = 2
		}
		if units+width > position.Character {
			return 0, false
		}
		units += width
		runes++
	}
	if units == position.Character {
		return start.Offset() + runes, true
	}
	return 0, false
}

func (w *window) setDiagnostics(uri string, diagnostics []lsp.Diagnostic) {
	dispatch.Main(func() {
		for _, doc := range w.documents {
			if uri == doc.lspURI && !doc.closed {
				doc.diagnostics = len(diagnostics)
				if w.active() == doc {
					w.updateTitle(doc)
				}
			}
		}
	})
}

func (d *editorDocument) stopLSP() {
	if d.lspClient == nil {
		return
	}
	if d.lspReady {
		if err := d.lspClient.CloseDocument(d.lspURI); err != nil {
			slog.Debug("close LSP document", "err", err)
		}
	}
	session := d.window.lspSessions[d.lspSession]
	if session != nil {
		delete(session.docs, d)
		if len(session.docs) == 0 {
			// Close asks the server to exit and reaps the process; cancel is
			// the hard stop if it does not.
			session.client.Close()
			session.cancel()
			delete(d.window.lspSessions, session.key)
		}
	}
	d.lspClient = nil
	d.lspURI = ""
	d.lspSession = ""
	d.lspReady = false
	d.lspOpening = false
	d.lspVer = 0
	d.diagnostics = 0
}

func (d *editorDocument) startExternalWatch() {
	if d.watching || d.path == "" {
		return
	}
	d.watching = true
	glib.TimeoutAdd(2000, func() bool {
		if d.closed {
			return false
		}
		path, modTime := d.path, d.modTime
		if d.diskWarn || d.loading || d.saveRunning {
			return true
		}
		go func() {
			changed, err := document.ChangedOnDisk(path, modTime)
			exists := pathExists(path)
			dispatch.Main(func() {
				if d.closed || path != d.path || modTime != d.modTime || d.diskWarn || d.loading || d.saveRunning {
					return
				}
				if err != nil {
					slog.Debug("check external document change", "path", path, "err", err)
					return
				}
				if !changed {
					return
				}
				if exists && !d.buffer.Modified() {
					// Nothing unsaved can be lost, so follow the file on disk.
					d.stopLSP()
					d.load(path, d.buffer.IterAtMark(d.buffer.GetInsert()).Offset())
					return
				}
				d.diskWarn = true
				d.window.toast(filepath.Base(path) + " changed on disk")
			})
		}()
		return true
	})
}

func (d *editorDocument) lspPosition(iter *gtk.TextIter) lsp.Position {
	lineStart, ok := d.buffer.IterAtLine(iter.Line())
	if !ok {
		return lsp.Position{Line: iter.Line(), Character: iter.LineOffset()}
	}
	// UTF-16 length is the character count plus one per rune outside the
	// BMP, which are exactly the 4-byte UTF-8 sequences.
	units := iter.LineOffset()
	for _, b := range []byte(d.buffer.Text(lineStart, iter, true)) {
		if b >= 0xf0 {
			units++
		}
	}
	return lsp.Position{Line: iter.Line(), Character: units}
}

func (d *editorDocument) close() {
	if d.closed {
		return
	}
	d.closed = true
	for _, request := range d.saveQueue {
		if request.after != nil {
			request.after(false)
		}
	}
	d.saveQueue = nil
	d.dismissCompletion()
	if d.completionTimer != nil {
		d.completionTimer.Stop()
	}
	if d.previewTimer != nil {
		d.previewTimer.Stop()
	}
	d.stopLSP()
	d.closeBranding()
	if d.ghost != nil {
		d.ghost.close()
	}
	d.view.Close()
}

func languageForPath(path string) string {
	extension := strings.ToLower(filepath.Ext(path))
	return map[string]string{
		".go": "go", ".py": "python", ".rs": "rust", ".c": "c", ".h": "c",
		".cc": "cpp", ".cpp": "cpp", ".hpp": "cpp", ".js": "javascript", ".jsx": "javascript",
		".ts": "typescript", ".tsx": "typescript", ".json": "json", ".md": "markdown",
		".html": "html", ".css": "css", ".sh": "shellscript", ".yaml": "yaml", ".yml": "yaml",
	}[extension]
}
