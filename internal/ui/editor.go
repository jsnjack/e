package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf16"
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

	id       string
	path     string
	newline  document.Newline
	mode     os.FileMode
	modTime  int64
	props    editorconfig.Properties
	loading  bool
	closed   bool
	revision uint64
	bulkEdit bool

	completionTimer  *time.Timer
	completionCancel context.CancelFunc
	completionGen    uint64
	ghostText        string
	ghostOffset      int

	lspClient *lsp.Client
	lspURI    string
	lspLang   string
	lspCancel context.CancelFunc
	lspReady  bool
	lspVer    int
	watching  bool
	diskWarn  bool

	multiCursors        []multiCursor
	multiTag            *gtk.TextTag
	carets              []*gtk.Label
	brandTag            *gtk.TextTag
	brandScanGen        uint64
	brandRefreshPending bool
	brandRefreshStart   int
	brandRefreshEnd     int
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
		newline: document.NewlineLF, mode: 0o644,
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
		if len(snapshot.Text) > 0 || snapshot.Modified || snapshot.Path == "" {
			doc.insertChunks(snapshot.Text, snapshot.Modified, snapshot.Cursor, nil, nil)
		} else {
			doc.load(snapshot.Path)
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

	d.ghost = newInlineCompletionRenderer(d.view.Widget, d.buffer)
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
		if d.bulkEdit {
			return
		}
		d.dismissCompletion()
		d.scheduleCompletion()
		d.scheduleMarkdownPreview()
		if d.search != nil && d.search.query != "" {
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
			d.window.updateTitle(d)
		}
	})
	d.buffer.ConnectInsertText(func(location *gtk.TextIter, text string, _ int) {
		if !d.loading {
			d.scheduleBrandRange(location.Offset()-1, location.Offset()+utf8.RuneCountInString(text)+1)
		}
		if d.loading || d.bulkEdit || !d.lspReady {
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
		if d.loading || d.bulkEdit || !d.lspReady {
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
	if state&(gdk.ShiftMask|gdk.AltMask) == gdk.ShiftMask|gdk.AltMask && state&(gdk.ControlMask|gdk.SuperMask) == 0 {
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

func (d *editorDocument) load(path string) {
	d.loading = true
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
				d.page.SetLoading(false)
				operation.Fail(err)
				slog.Warn("open document", "path", path, "err", err)
				return
			}
			d.path, d.newline, d.mode, d.modTime, d.props = file.Path, file.Newline, file.Mode, file.ModTime, props
			d.diskWarn = false
			d.insertChunks(file.Text, false, 0, func(current, total int) {
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
	d.page.SetLoading(true)
	d.view.SetHighlighting(false)
	d.buffer.BeginIrreversibleAction()
	d.buffer.SetText("")
	offset := 0
	glib.IdleAdd(func() bool {
		if d.closed {
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
			iter := d.buffer.IterAtOffset(d.buffer.CharCount())
			d.buffer.Insert(iter, string(data[offset:end]))
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
		d.page.SetLoading(false)
		d.updatePresentation()
		d.scheduleMarkdownPreview()
		if done != nil {
			done(true)
		}
		return false
	})
}

func (d *editorDocument) applyEditorConfig() {
	tabWidth := d.window.options.Config.Editor.TabWidth
	indentWidth := -1
	spaces := d.window.options.Config.Editor.InsertSpaces
	if d.props.TabWidth > 0 {
		tabWidth = d.props.TabWidth
	}
	if d.props.IndentSize > 0 {
		indentWidth = d.props.IndentSize
	}
	if d.props.IndentStyle != "" {
		spaces = d.props.IndentStyle == "space"
	}
	d.view.SetOptions(d.window.options.Config.Editor.LineNumbers, true, spaces, tabWidth, indentWidth)
	d.newline = newlineFromConfig(d.props.EndOfLine, d.newline)
}

func (d *editorDocument) save(path string) { d.saveThen(path, nil) }

func (d *editorDocument) saveThen(path string, after func(bool)) {
	start, end := d.buffer.Bounds()
	text := []byte(d.buffer.Text(start, end, true))
	prepared := document.PrepareSave(text, boolOr(d.props.TrimTrailingWhitespace, false), boolOr(d.props.InsertFinalNewline, false))
	saveRevision := d.revision
	cursorOffset := d.buffer.IterAtMark(d.buffer.GetInsert()).Offset()
	newline, mode := d.newline, d.mode
	d.page.SetLoading(true)
	operation := d.window.activityHub.Begin(activity.File, "Saving "+filepath.Base(path))
	go func() {
		modTime, err := document.Save(path, prepared, newline, mode)
		dispatch.Main(func() {
			if d.closed {
				operation.Cancel()
				if after != nil {
					after(false)
				}
				return
			}
			d.page.SetLoading(false)
			if err != nil {
				slog.Warn("save document", "path", path, "err", err)
				operation.Fail(err)
				if after != nil {
					after(false)
				}
				return
			}
			d.path, d.modTime = path, modTime
			d.diskWarn = false
			savedCurrentRevision := d.revision == saveRevision
			if savedCurrentRevision {
				if !bytes.Equal(text, prepared) {
					d.loading = true
					d.buffer.BeginUserAction()
					d.buffer.SetText(string(prepared))
					d.buffer.PlaceCursor(d.buffer.IterAtOffset(min(cursorOffset, d.buffer.CharCount())))
					d.buffer.EndUserAction()
					d.loading = false
				}
				d.buffer.SetModified(false)
			}
			d.view.SetLanguage(path, "")
			d.updatePresentation()
			operation.Finish(humanByteCount(len(prepared)), "", false)
			if d.lspClient == nil {
				d.startLSP()
			} else if err := d.lspClient.Save(d.lspURI); err != nil {
				slog.Debug("LSP save", "err", err)
			}
			d.startExternalWatch()
			if after != nil {
				after(savedCurrentRevision)
			}
		})
	}()
}

func (d *editorDocument) snapshot() document.Snapshot {
	start, end := d.buffer.Bounds()
	cursor := d.buffer.IterAtMark(d.buffer.GetInsert()).Offset()
	return document.Snapshot{
		ID: d.id, Path: d.path, Title: d.title(), Text: []byte(d.buffer.Text(start, end, true)),
		Modified: d.buffer.Modified(), Cursor: cursor, Newline: d.newline,
	}
}

func (d *editorDocument) scheduleCompletion() {
	if d.window.completer == nil || !d.window.options.Config.AI.Enabled || len(d.multiCursors) > 1 {
		return
	}
	if d.completionTimer != nil {
		d.completionTimer.Stop()
	}
	if d.search != nil && d.search.timer != nil {
		d.search.timer.Stop()
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
	if d.window.completer == nil || d.closed || d.loading {
		return
	}
	if _, _, selected := d.buffer.SelectionBounds(); selected {
		return
	}
	cursor := d.buffer.IterAtMark(d.buffer.GetInsert())
	offset := cursor.Offset()
	if !manual && offset == 0 {
		return
	}
	maxPrefix := d.window.options.Config.AI.PrefixSize
	maxSuffix := d.window.options.Config.AI.SuffixSize
	start := d.buffer.IterAtOffset(max(0, offset-maxPrefix))
	end := d.buffer.IterAtOffset(min(d.buffer.CharCount(), offset+maxSuffix))
	req := ai.Request{
		Prefix: d.buffer.Text(start, cursor, true), Suffix: d.buffer.Text(cursor, end, true),
		Filename: d.path, Language: languageForPath(d.path),
	}
	if strings.TrimSpace(req.Prefix) == "" && !manual {
		return
	}
	if d.completionCancel != nil {
		d.completionCancel()
	}
	d.completionGen++
	gen := d.completionGen
	ctx, cancel := context.WithCancel(d.window.ctx)
	d.completionCancel = cancel
	completer := d.window.completer
	operation := d.window.activityHub.Begin(activity.AI, "Generating suggestion")
	go func() {
		completion, err := completer.CompleteDetailed(ctx, req)
		if err != nil {
			operation.Fail(err)
			if !errors.Is(err, context.Canceled) {
				slog.Debug("AI completion", "err", err)
			}
			return
		}
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
	if text == "" {
		return
	}
	logging.Trace("ui: show completion", "active", d.window.win.IsActive(),
		"sensitive", d.window.content.IsSensitive(), "preferences", d.window.preferences != nil,
		"characters", utf8.RuneCountInString(text), "lines", strings.Count(text, "\n")+1)
	if !d.ghost.show(text, d.buffer.IterAtOffset(offset)) {
		d.ghostText = ""
		return
	}
	d.ghostText, d.ghostOffset = text, offset
	presentation := "inline"
	if d.ghost.preview {
		presentation = "below-line"
	}
	logging.Trace("ui: completion visible", "presentation", presentation, "offset", offset)
}

func (d *editorDocument) dismissCompletion() {
	d.completionGen++
	if d.completionCancel != nil {
		d.completionCancel()
		d.completionCancel = nil
	}
	d.ghostText = ""
	if d.ghost != nil {
		d.ghost.hide()
	}
}

func (d *editorDocument) acceptCompletion(oneWord bool) {
	text := d.ghostText
	if oneWord {
		text = completionWord(text)
	}
	if text == "" {
		return
	}
	d.dismissCompletion()
	d.buffer.BeginUserAction()
	d.buffer.Insert(d.buffer.IterAtMark(d.buffer.GetInsert()), text)
	d.buffer.EndUserAction()
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
	client := lsp.New(command, args, root, d.setDiagnostics)
	d.lspClient = client
	d.lspURI, d.lspLang = lsp.FileURI(d.path), language
	ctx, cancel := context.WithCancel(d.window.ctx)
	d.lspCancel = cancel
	start, end := d.buffer.Bounds()
	text := d.buffer.Text(start, end, true)
	go func() {
		if err := client.Start(ctx); err != nil {
			slog.Debug("start LSP", "language", language, "err", err)
			return
		}
		if err := client.Open(d.lspURI, language, 1, text); err != nil {
			slog.Debug("open LSP document", "language", language, "err", err)
			return
		}
		dispatch.Main(func() {
			if !d.closed {
				d.lspVer = 1
				d.lspReady = true
			}
		})
	}()
}

func (d *editorDocument) requestLSPCompletion() {
	if !d.lspReady {
		d.window.toast("No language server is available for this file")
		return
	}
	iter := d.buffer.IterAtMark(d.buffer.GetInsert())
	position := d.lspPosition(iter)
	offset := iter.Offset()
	ctx, cancel := context.WithTimeout(d.window.ctx, 2*time.Second)
	go func() {
		defer cancel()
		items, err := d.lspClient.Completions(ctx, d.lspURI, position)
		if err != nil {
			slog.Debug("LSP completion", "err", err)
			return
		}
		if len(items) == 0 {
			return
		}
		text := items[0].InsertText
		dispatch.Main(func() {
			if !d.closed && d.buffer.IterAtMark(d.buffer.GetInsert()).Offset() == offset {
				d.showCompletion(text, offset)
			}
		})
	}()
}

func (d *editorDocument) setDiagnostics(uri string, diagnostics []lsp.Diagnostic) {
	dispatch.Main(func() {
		if uri == d.lspURI && !d.closed && len(diagnostics) > 0 && d.window.active() == d {
			d.window.title.SetSubtitle(fmt.Sprintf("%s · %d diagnostics", d.path, len(diagnostics)))
		}
	})
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
		go func() {
			changed, err := document.ChangedOnDisk(path, modTime)
			dispatch.Main(func() {
				if d.closed || path != d.path || modTime != d.modTime {
					return
				}
				if err != nil {
					slog.Debug("check external document change", "path", path, "err", err)
					return
				}
				if changed && !d.diskWarn {
					d.diskWarn = true
					d.window.toast(filepath.Base(path) + " changed on disk")
				}
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
	text := d.buffer.Text(lineStart, iter, true)
	return lsp.Position{Line: iter.Line(), Character: len(utf16.Encode([]rune(text)))}
}

func (d *editorDocument) close() {
	if d.closed {
		return
	}
	d.closed = true
	d.dismissCompletion()
	if d.completionTimer != nil {
		d.completionTimer.Stop()
	}
	if d.previewTimer != nil {
		d.previewTimer.Stop()
	}
	if d.lspReady {
		if err := d.lspClient.CloseDocument(d.lspURI); err != nil {
			slog.Debug("close LSP document", "err", err)
		}
	}
	if d.lspCancel != nil {
		d.lspCancel()
	}
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
