package ui

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/activity"
	"github.com/jsnjack/e/internal/ai"
	"github.com/jsnjack/e/internal/config"
	"github.com/jsnjack/e/internal/dispatch"
	"github.com/jsnjack/e/internal/document"
	"github.com/jsnjack/e/internal/logging"
	"github.com/jsnjack/e/internal/recent"
)

type window struct {
	app     *adw.Application
	options Options

	ctx    context.Context
	cancel context.CancelFunc

	win                 *adw.ApplicationWindow
	toastOverlay        *adw.ToastOverlay
	activityHub         *activity.Hub
	activity            *activitySurface
	tabs                *adw.TabView
	tabBar              *adw.TabBar
	title               *adw.WindowTitle
	searchBar           *gtk.SearchBar
	searchEntry         *gtk.SearchEntry
	replaceEntry        *gtk.Entry
	matchLabel          *gtk.Label
	content             *gtk.Paned
	mainStack           *gtk.Stack
	empty               *emptyState
	sidebar             *gtk.ScrolledWindow
	sidebarBody         *gtk.Box
	sidebarToggle       *gtk.Button
	sidebarWidth        int
	projectRoot         string
	projectTree         *projectTree
	pendingCtrlK        bool
	ctrlKTimer          glib.SourceHandle
	preferences         *adw.PreferencesDialog
	preferencesApplying bool
	// lastPalette is the newest picker dialog, for runtime tests.
	lastPalette *palette

	documents   map[*adw.TabPage]*editorDocument
	lspSessions map[string]*lspSession
	saveFile    func(*saveRequest) (int64, error)
	completer   *ai.Completer
	aiCancel    context.CancelFunc
	restored    bool

	// Session writes run in the background; sessionMu orders them and
	// sessionWritten drops a write that is older than one already on disk.
	sessionMu      sync.Mutex
	sessionSeq     uint64
	sessionWritten uint64
	sessionTimer   glib.SourceHandle
	sessionClosed  bool
	forceClose     bool

	// configMu and configWritten drop a config write that is older than one
	// already on disk, like the session writes above.
	configMu      sync.Mutex
	configSeq     uint64
	configWritten uint64

	// recent feeds the path picker; recentMu and recentWritten order its
	// background writes.
	recent        []recent.Entry
	recentLoaded  bool
	recentMu      sync.Mutex
	recentSeq     uint64
	recentWritten uint64
}

// applyConfig makes cfg current at once and writes it to disk in the
// background. It is for small settings changed outside Preferences.
func (w *window) applyConfig(cfg config.Config) {
	w.options.Config = cfg
	w.configSeq++
	sequence, path := w.configSeq, w.options.ConfigPath
	if path == "" {
		return
	}
	go func() {
		w.configMu.Lock()
		defer w.configMu.Unlock()
		if sequence <= w.configWritten {
			return
		}
		if err := config.Save(path, cfg); err != nil {
			slog.Warn("save config", "err", err)
			dispatch.Main(func() { w.toast("Settings weren’t saved: " + err.Error()) })
			return
		}
		w.configWritten = sequence
	}()
}

// sessionSaveDelaySeconds bounds how much unsaved text a crash can lose.
const sessionSaveDelaySeconds = 5

func newWindow(app *adw.Application, options Options) *window {
	ctx, cancel := context.WithCancel(context.Background())
	w := &window{
		app: app, options: options, ctx: ctx, cancel: cancel,
		documents:   make(map[*adw.TabPage]*editorDocument),
		lspSessions: make(map[string]*lspSession),
	}
	w.saveFile = func(request *saveRequest) (int64, error) {
		if request.force || request.expectedModTime == 0 {
			return document.Save(request.path, request.prepared, request.newline, request.bom, request.mode)
		}
		return document.SaveChecked(request.path, request.prepared, request.newline, request.bom, request.mode, request.expectedModTime)
	}
	w.build()
	w.registerActions()
	w.setCompleter(options.Completer)
	w.loadRecent()
	return w
}

func (w *window) build() {
	logging.Trace("ui: build window shell")
	loadCSS()
	w.win = adw.NewApplicationWindow(&w.app.Application)
	w.win.AddCSSClass("e-window")
	w.win.SetTitle("E")
	w.win.SetDefaultSize(1100, 760)
	w.win.Connect("notify::is-active", func() {
		logging.Trace("ui: window focus changed", "active", w.win.IsActive(), "sensitive", w.win.IsSensitive(),
			"preferences", w.preferences != nil)
		if !w.win.IsActive() {
			w.clearPendingChord()
			preserveWindowContrast(&w.win.Widget)
			return
		}
		// A commit or `git add` in another program moves the diff base.
		for _, doc := range w.documents {
			doc.refreshDiffBase()
		}
	})
	logging.Trace("ui: application window ready")

	header := adw.NewHeaderBar()
	w.title = adw.NewWindowTitle("E", "A fast, focused editor")
	header.SetTitleWidget(w.title)
	w.activityHub = activity.NewHub()
	w.activity = newActivitySurface(w.activityHub)

	open := gtk.NewButtonFromIconName("document-open-symbolic")
	open.SetTooltipText("Open File")
	open.ConnectClicked(w.chooseOpen)
	header.PackStart(open)

	newButton := gtk.NewButtonFromIconName("document-new-symbolic")
	newButton.SetTooltipText("New Document")
	newButton.ConnectClicked(func() { w.newDocument(nil) })
	header.PackStart(newButton)

	w.sidebarToggle = gtk.NewButtonFromIconName("sidebar-show-symbolic")
	w.sidebarToggle.SetTooltipText("Show or hide project files")
	w.sidebarToggle.SetVisible(false)
	w.sidebarToggle.ConnectClicked(w.toggleSidebar)
	header.PackStart(w.sidebarToggle)

	menu := gio.NewMenu()
	menu.Append("Command Palette…", "win.command-palette")
	menu.Append("Quick Open…", "win.quick-open")
	menu.Append("Open Path…", "win.open-path")
	menu.Append("Open Folder…", "win.open-folder")
	menu.Append("Save As…", "win.save-as")
	menu.Append("Find and Replace", "win.replace")
	menu.Append("Markdown Preview", "win.markdown-preview")
	menu.Append("Show Changes in gdiff", "win.show-changes")
	menu.Append("Transform Selection with AI…", "win.ai-transform")
	menu.Append("Preferences", "win.preferences")
	menu.Append("Keyboard Shortcuts", "win.shortcuts")
	menu.Append("About E", "win.about")
	menuButton := gtk.NewMenuButton()
	menuButton.SetIconName("open-menu-symbolic")
	menuButton.SetMenuModel(menu)
	header.PackEnd(menuButton)

	searchButton := gtk.NewButtonFromIconName("edit-find-symbolic")
	searchButton.SetTooltipText("Find")
	searchButton.ConnectClicked(func() { w.showSearch(false) })
	header.PackEnd(searchButton)

	save := gtk.NewButtonFromIconName("document-save-symbolic")
	save.SetTooltipText("Save")
	save.ConnectClicked(w.saveActive)
	header.PackEnd(save)
	logging.Trace("ui: header ready")

	w.tabs = adw.NewTabView()
	w.tabBar = adw.NewTabBar()
	w.tabBar.SetView(w.tabs)
	w.tabBar.SetAutohide(true)
	w.tabs.ConnectClosePage(w.closePage)
	w.tabs.Connect("notify::selected-page", w.selectedPageChanged)
	w.tabs.NotifyProperty("n-pages", w.updateEmptyState)
	w.empty = newEmptyState()
	w.mainStack = gtk.NewStack()
	w.mainStack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	w.mainStack.SetTransitionDuration(160)
	w.mainStack.AddNamed(w.tabs, "tabs")
	w.mainStack.AddNamed(w.empty.root, "empty")
	logging.Trace("ui: tabs ready")

	w.searchBar = gtk.NewSearchBar()
	w.searchBar.SetShowCloseButton(true)
	w.searchEntry = gtk.NewSearchEntry()
	w.searchEntry.SetPlaceholderText("Find")
	w.replaceEntry = gtk.NewEntry()
	w.replaceEntry.SetPlaceholderText("Replace with")
	w.matchLabel = gtk.NewLabel("")
	w.matchLabel.AddCSSClass("dim-label")
	previous := gtk.NewButtonFromIconName("go-up-symbolic")
	previous.SetTooltipText("Previous Match")
	next := gtk.NewButtonFromIconName("go-down-symbolic")
	next.SetTooltipText("Next Match")
	replace := gtk.NewButtonWithLabel("Replace")
	replaceAll := gtk.NewButtonWithLabel("Replace All")
	row := gtk.NewBox(gtk.OrientationHorizontal, 6)
	row.SetMarginStart(12)
	row.SetMarginEnd(12)
	row.SetMarginTop(6)
	row.SetMarginBottom(6)
	w.searchEntry.SetHExpand(true)
	w.replaceEntry.SetHExpand(true)
	row.Append(w.searchEntry)
	row.Append(w.replaceEntry)
	row.Append(w.matchLabel)
	row.Append(previous)
	row.Append(next)
	row.Append(replace)
	row.Append(replaceAll)
	w.searchBar.SetChild(row)
	w.searchBar.ConnectEntry(w.searchEntry)
	w.searchBar.SetKeyCaptureWidget(&w.win.Widget)
	w.replaceEntry.SetVisible(false)
	w.searchEntry.ConnectSearchChanged(func() {
		if doc := w.active(); doc != nil {
			doc.search.update(w.searchEntry.Text())
		}
	})
	w.searchEntry.ConnectNextMatch(func() { w.navigateMatch(1) })
	w.searchEntry.ConnectPreviousMatch(func() { w.navigateMatch(-1) })
	w.searchEntry.ConnectStopSearch(func() { w.searchBar.SetSearchMode(false) })
	previous.ConnectClicked(func() { w.navigateMatch(-1) })
	next.ConnectClicked(func() { w.navigateMatch(1) })
	replace.ConnectClicked(func() {
		if doc := w.active(); doc != nil {
			doc.search.replaceCurrent(w.replaceEntry.Text())
		}
	})
	replaceAll.ConnectClicked(func() {
		if doc := w.active(); doc != nil {
			doc.search.replaceAll(w.replaceEntry.Text())
		}
	})
	logging.Trace("ui: search ready")

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(header)
	toolbar.AddTopBar(w.tabBar)
	toolbar.AddTopBar(w.searchBar)
	w.sidebarBody = gtk.NewBox(gtk.OrientationVertical, 0)
	w.sidebar = gtk.NewScrolledWindow()
	w.sidebar.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	w.sidebar.SetMinContentWidth(160)
	w.sidebar.SetChild(w.sidebarBody)
	w.sidebar.SetVisible(false)
	w.sidebarWidth = 260
	w.content = gtk.NewPaned(gtk.OrientationHorizontal)
	w.content.AddCSSClass("editor-content")
	w.content.SetStartChild(w.sidebar)
	w.content.SetEndChild(w.mainStack)
	w.content.SetResizeStartChild(false)
	w.content.SetResizeEndChild(true)
	w.content.SetShrinkStartChild(true)
	w.content.SetShrinkEndChild(false)
	w.content.SetWideHandle(true)
	w.content.SetPosition(w.sidebarWidth)
	toolbar.SetContent(w.content)
	w.updateEmptyState()
	w.activity.mount(toolbar)
	w.activity.subscribe(w.ctx)
	w.toastOverlay = adw.NewToastOverlay()
	w.toastOverlay.SetChild(toolbar)
	w.win.SetContent(w.toastOverlay)
	w.win.ConnectCloseRequest(func() bool {
		if w.forceClose {
			return false
		}
		if err := w.persistSession(true); err != nil {
			w.confirmCloseWithoutSession(err)
			return true
		}
		w.sessionClosed = true
		return false
	})
	logging.Trace("ui: window shell ready")
}

func (w *window) registerActions() {
	logging.Trace("ui: register actions")
	w.action("new", func() { w.newDocument(nil) }, "<Control>n")
	w.action("open", w.chooseOpen, "<Control>o")
	w.action("open-folder", w.chooseFolder)
	w.action("open-path", w.showPathPicker, "<Control><Alt>o")
	w.action("show-changes", w.showChangesInGdiff, "<Control><Alt>d")
	w.action("command-palette", w.showCommandPalette, "<Control><Shift>p")
	w.action("quick-open", w.showQuickOpen, "<Control>p")
	w.action("save", func() {
		// Application accelerators can run before the window key handler, so
		// finish the Ctrl+K Ctrl+S chord here as well.
		if w.pendingCtrlK {
			w.clearPendingChord()
			w.showShortcuts()
			return
		}
		w.saveActive()
	}, "<Control>s")
	w.action("save-as", w.saveActiveAs, "<Control><Shift>s")
	w.action("find", func() { w.showSearch(false) }, "<Control>f")
	w.action("replace", func() { w.showSearch(true) }, "<Control>h")
	w.action("markdown-preview", func() {
		if doc := w.active(); doc != nil {
			doc.toggleMarkdownPreview()
		}
	}, "<Control><Shift>v")
	w.action("ai-transform", w.showAITransform)
	// Undo and redo have no application accelerators: the source view binds
	// Ctrl+Z and Ctrl+Shift+Z itself, and global accelerators would steal
	// them from the search, replace, and palette entries.
	w.action("undo", func() {
		if doc := w.active(); doc != nil && doc.buffer.CanUndo() {
			doc.buffer.Undo()
		}
	})
	w.action("redo", func() {
		if doc := w.active(); doc != nil && doc.buffer.CanRedo() {
			doc.buffer.Redo()
		}
	})
	w.action("toggle-sidebar", w.toggleSidebar, "<Control>b")
	w.action("show-sidebar", func() {
		w.showSidebar()
		w.sidebar.GrabFocus()
	}, "<Control><Shift>e")
	w.action("close-tab", func() {
		if page := w.tabs.SelectedPage(); page != nil {
			w.tabs.ClosePage(page)
		}
	}, "<Control>w")
	w.action("complete", func() {
		logging.Trace("ui: completion action activated")
		if doc := w.active(); doc != nil {
			doc.requestCompletion(true)
		}
	}, "<Alt>backslash")
	w.action("lsp-complete", func() {
		if doc := w.active(); doc != nil {
			doc.requestLSPCompletion()
		}
	}, "<Control>space")
	w.action("select-next", func() {
		if doc := w.active(); doc != nil {
			doc.selectNextOccurrence()
		}
	}, "<Control>d")
	w.action("select-all-occurrences", func() {
		if doc := w.active(); doc != nil {
			doc.selectAllOccurrences()
		}
	}, "<Control><Shift>l", "<Control>F2", "<Alt>Return")
	w.action("next-tab", func() { w.selectRelativeTab(1) }, "<Control>Tab")
	w.action("previous-tab", func() { w.selectRelativeTab(-1) }, "<Control><Shift>Tab")
	w.action("find-next", func() { w.navigateMatch(1) }, "F3")
	w.action("find-previous", func() { w.navigateMatch(-1) }, "<Shift>F3")
	w.action("preferences", w.openPreferences, "<Control>comma")
	w.action("shortcuts", w.showShortcuts)
	w.action("about", w.showAbout)
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(w.keyPressed)
	w.win.AddController(keys)
	logging.Trace("ui: actions ready")
}

func (w *window) action(name string, fn func(), accelerators ...string) {
	logging.Trace("ui: register action", "name", name)
	action := gio.NewSimpleAction(name, nil)
	action.ConnectActivate(func(*glib.Variant) { fn() })
	w.win.AddAction(action)
	var valid []string
	for _, accelerator := range accelerators {
		if _, _, ok := gtk.AcceleratorParse(accelerator); ok {
			valid = append(valid, accelerator)
		}
	}
	if len(valid) > 0 {
		w.app.SetAccelsForAction("win."+name, valid)
	}
}

func (w *window) keyPressed(keyval, _ uint, state gdk.ModifierType) bool {
	lower := gdk.KeyvalToLower(keyval)
	ctrl := state&gdk.ControlMask != 0
	shift := state&gdk.ShiftMask != 0
	alt := state&gdk.AltMask != 0
	super := state&gdk.SuperMask != 0
	if alt && !ctrl && !super && lower == gdk.KEY_backslash {
		logging.Trace("ui: completion shortcut pressed", "keyval", keyval, "shift", shift)
		if doc := w.active(); doc != nil {
			doc.requestCompletion(true)
		}
		return true
	}
	if w.pendingCtrlK {
		w.clearPendingChord()
		if ctrl && !shift && !alt && !super && lower == gdk.KEY_s {
			w.showShortcuts()
			return true
		}
		if ctrl && !shift && !alt && !super && lower == gdk.KEY_o {
			w.chooseFolder()
			return true
		}
	}
	if ctrl && !shift && !alt && !super && lower == gdk.KEY_k {
		w.pendingCtrlK = true
		w.ctrlKTimer = glib.TimeoutAdd(1500, func() bool {
			w.pendingCtrlK = false
			w.ctrlKTimer = 0
			return false
		})
		return true
	}
	return false
}

func (w *window) clearPendingChord() {
	w.pendingCtrlK = false
	if w.ctrlKTimer != 0 {
		glib.SourceRemove(w.ctrlKTimer)
		w.ctrlKTimer = 0
	}
}

// preserveWindowContrast removes GTK's inherited backdrop state. Source-view
// syntax tags are otherwise attenuated when focus briefly moves to a portal or
// screenshot tool, making a document look disabled even though it remains
// interactive.
func preserveWindowContrast(widget *gtk.Widget) {
	if widget == nil {
		return
	}
	widget.UnsetStateFlags(gtk.StateFlagBackdrop)
	for child := widget.FirstChild(); child != nil; child = gtk.BaseWidget(child).NextSibling() {
		preserveWindowContrast(gtk.BaseWidget(child))
	}
}

func (w *window) selectedPageChanged() {
	doc := w.active()
	w.updateTitle(doc)
	if doc != nil && w.searchBar.SearchMode() {
		doc.search.update(w.searchEntry.Text())
	}
}

func (w *window) toggleSidebar() {
	if w.projectRoot == "" {
		w.showSidebar()
		return
	}
	if w.sidebar.Visible() {
		if width := w.content.Position(); width >= 160 {
			w.sidebarWidth = width
		}
		w.sidebar.SetVisible(false)
		return
	}
	w.showSidebar()
}

func (w *window) showSidebar() {
	if w.projectRoot == "" {
		// Without an open folder, use the active file's project.
		doc := w.active()
		if doc == nil || doc.path == "" {
			w.toast("Open a folder to show project files")
			return
		}
		w.setProjectRoot(projectRootFor(doc.path, pathExists))
		return
	}
	w.sidebar.SetVisible(true)
	w.content.SetPosition(max(w.sidebarWidth, 160))
}

// projectRootFor returns the nearest ancestor of file that holds a .git entry,
// or the file's directory when there is none.
func projectRootFor(file string, exists func(string) bool) string {
	dir := filepath.Dir(file)
	for current := dir; ; {
		if exists(filepath.Join(current, ".git")) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return dir
		}
		current = parent
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (w *window) selectRelativeTab(direction int) {
	if w.tabs.NPages() < 2 || w.tabs.SelectedPage() == nil {
		return
	}
	position := w.tabs.PagePosition(w.tabs.SelectedPage())
	position = (position + direction + w.tabs.NPages()) % w.tabs.NPages()
	w.tabs.SetSelectedPage(w.tabs.NthPage(position))
}

func (w *window) present() { w.win.Present() }

func (w *window) stop() {
	if !w.sessionClosed {
		if err := w.persistSession(true); err != nil {
			slog.Warn("save session", "err", err)
		}
	}
	w.cancel()
	if w.activity != nil {
		w.activity.destroy()
	}
	for _, doc := range w.documents {
		doc.close()
	}
}

func (w *window) setCompleter(completer *ai.Completer) {
	if w.aiCancel != nil {
		w.aiCancel()
		w.aiCancel = nil
	}
	w.completer = completer
	if completer != nil && w.options.Config.AI.Enabled && w.options.Config.AI.Consented {
		ctx, cancel := context.WithCancel(w.ctx)
		w.aiCancel = cancel
		logging.Trace("ui: start autocomplete probes")
		completer.Start(ctx)
	}
}

func (w *window) restoreOrNew() {
	w.restoreSession()
	if len(w.documents) == 0 {
		w.newDocument(nil)
	}
}

// restoreSession reopens the previous tabs once per window. Opening files
// from the command line must restore first, or the next session save would
// delete the old recovery copies.
func (w *window) restoreSession() {
	logging.Trace("ui: restore session")
	if w.restored {
		return
	}
	w.restored = true
	snapshots, err := document.LoadSession(w.options.Paths.StateDir)
	if err != nil {
		slog.Warn("restore session", "err", err)
		w.toast("Couldn’t restore the previous session")
	}
	var activePage *adw.TabPage
	for i := range snapshots {
		snapshot := snapshots[i]
		doc := w.newDocument(&snapshot)
		if snapshot.Active {
			activePage = doc.page
		}
	}
	if activePage != nil {
		w.tabs.SetSelectedPage(activePage)
	}
	logging.Trace("ui: session ready", "documents", len(w.documents))
}

func (w *window) active() *editorDocument {
	page := w.tabs.SelectedPage()
	if page == nil {
		return nil
	}
	return w.documentForPage(page)
}

func (w *window) documentForPage(page *adw.TabPage) *editorDocument {
	if doc := w.documents[page]; doc != nil {
		return doc
	}
	for stored, doc := range w.documents {
		if sameTabPage(stored, page) {
			return doc
		}
	}
	return nil
}

func sameTabPage(first, second *adw.TabPage) bool {
	if first == nil || second == nil {
		return false
	}
	return coreglib.BaseObject(first).Native() == coreglib.BaseObject(second).Native()
}

func (w *window) pageAttached(page *adw.TabPage) bool {
	for position := 0; position < w.tabs.NPages(); position++ {
		if sameTabPage(w.tabs.NthPage(position), page) {
			return true
		}
	}
	return false
}

func (w *window) documentForPath(path string) (*editorDocument, *adw.TabPage) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil
	}
	for stored, doc := range w.documents {
		if doc.path == abs {
			if w.pageAttached(doc.page) {
				return doc, doc.page
			}
			doc.close()
			delete(w.documents, stored)
		}
	}
	return nil, nil
}

func (w *window) openPath(path string) {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		w.setProjectRoot(path)
		return
	}
	if err == nil {
		w.rememberPath(path, false)
	}
	if doc, page := w.documentForPath(path); doc != nil {
		w.tabs.SetSelectedPage(page)
		doc.view.Widget.GrabFocus()
		return
	}
	doc := w.newDocument(nil)
	doc.load(path, 0)
}

func (w *window) chooseOpen() {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Open File")
	if folder := w.dialogFolder(); folder != "" {
		dialog.SetInitialFolder(gio.NewFileForPath(folder))
	}
	dialog.OpenMultipleTextFiles(w.ctx, &w.win.Window, func(result gio.AsyncResulter) {
		_, files, err := dialog.OpenMultipleTextFilesFinish(result)
		if err != nil || files == nil {
			return
		}
		for index := uint(0); index < files.NItems(); index++ {
			item := files.Item(index)
			if item == nil {
				continue
			}
			if file, ok := item.CastType(gio.GTypeFile).(*gio.File); ok {
				w.openPath(file.Path())
			}
		}
	})
}

func (w *window) chooseFolder() {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Open Folder")
	if folder := w.dialogFolder(); folder != "" {
		dialog.SetInitialFolder(gio.NewFileForPath(folder))
	}
	dialog.SelectFolder(w.ctx, &w.win.Window, func(result gio.AsyncResulter) {
		folder, err := dialog.SelectFolderFinish(result)
		if err != nil || folder == nil {
			return
		}
		w.setProjectRoot(folder.Path())
	})
}

// dialogFolder is where file dialogs start: the active file's folder, else
// the project folder.
func (w *window) dialogFolder() string {
	if doc := w.active(); doc != nil && doc.path != "" {
		return filepath.Dir(doc.path)
	}
	return w.projectRoot
}

func (w *window) saveActive() {
	if doc := w.active(); doc != nil {
		if doc.path == "" {
			w.saveDocumentAs(doc)
		} else {
			doc.save(doc.path)
		}
	}
}

func (w *window) saveActiveAs() {
	if doc := w.active(); doc != nil {
		w.saveDocumentAs(doc)
	}
}

func (w *window) saveDocumentAs(doc *editorDocument) {
	w.saveDocumentAsThen(doc, nil)
}

func (w *window) saveDocumentAsThen(doc *editorDocument, after func(bool)) {
	dialog := gtk.NewFileDialog()
	dialog.SetTitle("Save Document")
	if doc.path != "" {
		dialog.SetInitialFile(gio.NewFileForPath(doc.path))
	} else {
		if folder := w.dialogFolder(); folder != "" {
			dialog.SetInitialFolder(gio.NewFileForPath(folder))
		}
		dialog.SetInitialName(doc.title())
	}
	dialog.SaveTextFile(w.ctx, &w.win.Window, func(result gio.AsyncResulter) {
		_, _, file, err := dialog.SaveTextFileFinish(result)
		if err != nil || file == nil {
			if after != nil {
				after(false)
			}
			return
		}
		doc.saveThen(file.Path(), after)
	})
}

func (w *window) closePage(page *adw.TabPage) bool {
	doc := w.documentForPage(page)
	if doc == nil {
		return false
	}
	finish := func(close bool) {
		if close {
			doc.close()
			delete(w.documents, doc.page)
		}
		w.tabs.ClosePageFinish(page, close)
		if close {
			w.scheduleSessionSave()
		}
	}
	if doc.loading {
		if doc.pending != nil && doc.pending.Modified {
			// Recovered text is not in the buffer yet, so it cannot be saved.
			w.toast("Wait for the document to finish loading")
			finish(false)
			return true
		}
		finish(true)
		return true
	}
	if !doc.buffer.Modified() {
		finish(true)
		return true
	}
	dialog := adw.NewAlertDialog("Save changes?", fmt.Sprintf("Changes to “%s” will be lost if you close it.", doc.title()))
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("discard", "Discard")
	dialog.AddResponse("save", "Save")
	dialog.SetCloseResponse("cancel")
	dialog.SetDefaultResponse("save")
	dialog.SetResponseAppearance("discard", adw.ResponseDestructive)
	dialog.SetResponseAppearance("save", adw.ResponseSuggested)
	dialog.ConnectResponse(func(response string) {
		switch response {
		case "discard":
			finish(true)
		case "save":
			if doc.path == "" {
				w.saveDocumentAsThen(doc, finish)
			} else {
				doc.saveThen(doc.path, func(ok bool) { finish(ok) })
			}
		default:
			finish(false)
		}
	})
	dialog.Present(w.win)
	return true
}

func (w *window) showSearch(replace bool) {
	w.replaceEntry.SetVisible(replace)
	w.searchBar.SetSearchMode(true)
	w.searchEntry.GrabFocus()
}

func (w *window) navigateMatch(direction int) {
	if doc := w.active(); doc != nil {
		doc.search.navigate(direction)
	}
}

func (w *window) toast(message string) {
	toast := adw.NewToast(message)
	toast.SetTimeout(3)
	w.toastOverlay.AddToast(toast)
}

func (w *window) updateTitle(doc *editorDocument) {
	w.activity.updateDocument(doc)
	if doc == nil {
		w.title.SetTitle("E")
		w.title.SetSubtitle(w.projectRoot)
		return
	}
	w.title.SetTitle(doc.title())
	subtitle := doc.path
	if doc.diagnostics > 0 {
		subtitle = fmt.Sprintf("%s · %d diagnostics", subtitle, doc.diagnostics)
	}
	w.title.SetSubtitle(subtitle)
}

// persistSession saves the open tabs and recovery copies. Snapshots are taken
// on the GTK thread. The write runs in the background unless wait is set; only
// a waiting call reports its error.
func (w *window) persistSession(wait bool) error {
	if w.sessionTimer != 0 {
		glib.SourceRemove(w.sessionTimer)
		w.sessionTimer = 0
	}
	snapshots := make([]document.Snapshot, 0, len(w.documents))
	for index := 0; index < w.tabs.NPages(); index++ {
		page := w.tabs.NthPage(index)
		if doc := w.documentForPage(page); doc != nil {
			snapshot := doc.snapshot()
			snapshot.Active = sameTabPage(page, w.tabs.SelectedPage())
			snapshots = append(snapshots, snapshot)
		}
	}
	w.sessionSeq++
	sequence, stateDir := w.sessionSeq, w.options.Paths.StateDir
	write := func() error {
		w.sessionMu.Lock()
		defer w.sessionMu.Unlock()
		if sequence <= w.sessionWritten {
			return nil
		}
		err := document.SaveSession(stateDir, snapshots)
		if err == nil {
			w.sessionWritten = sequence
		}
		return err
	}
	if wait {
		return write()
	}
	go func() {
		if err := write(); err != nil {
			slog.Warn("save session", "err", err)
		}
	}()
	return nil
}

// scheduleSessionSave writes the session a few seconds after an edit, so a
// crash or logout loses at most that much unsaved text.
func (w *window) scheduleSessionSave() {
	if w.sessionTimer != 0 || w.sessionClosed {
		return
	}
	w.sessionTimer = glib.TimeoutSecondsAdd(sessionSaveDelaySeconds, func() bool {
		w.sessionTimer = 0
		if err := w.persistSession(false); err != nil {
			slog.Warn("save session", "err", err)
		}
		return false
	})
}

func (w *window) confirmCloseWithoutSession(err error) {
	slog.Warn("save session", "err", err)
	dialog := adw.NewAlertDialog("Couldn’t save the session",
		"Unsaved documents can’t be recovered after closing. "+err.Error())
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("close", "Close Anyway")
	dialog.SetCloseResponse("cancel")
	dialog.SetDefaultResponse("cancel")
	dialog.SetResponseAppearance("close", adw.ResponseDestructive)
	dialog.ConnectResponse(func(response string) {
		if response == "close" {
			w.forceClose = true
			w.win.Close()
		}
	})
	dialog.Present(w.win)
}

func loadCSS() {
	provider := gtk.NewCSSProvider()
	provider.LoadFromString(`
.editor { font-family: monospace; font-size: 11pt; }
.inline-completion { font-family: monospace; font-size: 11pt; }
.search-count { min-width: 5em; }
.project-tree { padding: 4px; }
.project-entry-row { border-radius: 6px; }
.project-entry-open { padding: 5px 7px; border-radius: 6px; }
.project-entry-menu { min-width: 28px; min-height: 28px; padding: 0; opacity: 0; }
.project-entry-row:hover .project-entry-menu,
.project-entry-menu:hover,
.project-entry-menu:focus { opacity: 1; }
.project-folder { padding: 5px 6px; }
.project-folder image,
.project-entry-open image { -gtk-icon-size: 18px; }
.project-entry-label { font-weight: 400; }
.project-folder .project-entry-label { font-weight: 500; }
.project-tree-footer {
  padding: 6px 8px 4px 10px;
  border-top: 1px solid alpha(@window_fg_color, .08);
  font-size: .9em;
}
.empty-state {
  background-color: @view_bg_color;
  background-image: radial-gradient(circle farthest-side at 50% 34%, alpha(@accent_bg_color, .13), alpha(@accent_bg_color, 0));
}
.empty-hero {
  padding: 22px;
  border-radius: 34px;
  background-image: linear-gradient(160deg, alpha(@accent_bg_color, .24), alpha(@accent_bg_color, .05));
  border: 1px solid alpha(@accent_color, .18);
  box-shadow: 0 18px 48px -12px alpha(@accent_bg_color, .45), inset 0 1px alpha(white, .10);
}
.empty-monogram {
  min-width: 96px;
  min-height: 96px;
  font-size: 64pt;
  font-weight: 800;
  color: @accent_color;
}
.empty-title { font-size: 24pt; font-weight: 800; letter-spacing: -1px; }
.empty-subtitle { font-family: monospace; font-size: .92em; color: alpha(@window_fg_color, .55); }
.empty-actions {
  padding: 6px;
  border-radius: 16px;
  background: alpha(@window_fg_color, .035);
  border: 1px solid alpha(@window_fg_color, .08);
}
.empty-action { padding: 9px 12px; border-radius: 10px; font-weight: 500; transition: background 120ms ease-out; }
.empty-action image { color: alpha(@window_fg_color, .6); transition: color 120ms ease-out; }
.empty-action:hover, .empty-action:focus-visible { background: alpha(@accent_bg_color, .12); }
.empty-action:hover image, .empty-action:focus-visible image { color: @accent_color; }
.empty-keycap {
  min-width: 14px;
  padding: 1px 6px;
  border-radius: 6px;
  font-family: monospace;
  font-size: .78em;
  font-weight: 600;
  color: alpha(@window_fg_color, .72);
  background: alpha(@window_fg_color, .06);
  border: 1px solid alpha(@window_fg_color, .12);
  box-shadow: inset 0 -1px alpha(@window_fg_color, .14);
}
.empty-keygap { font-size: .75em; color: alpha(@window_fg_color, .4); margin: 0 2px; }
.empty-tip { font-size: .85em; color: alpha(@window_fg_color, .42); }
.status-bar {
  min-height: 24px;
  padding: 2px 7px 2px 10px;
  background: @headerbar_bg_color;
  border-top: 1px solid alpha(@window_fg_color, .10);
  font-size: .88em;
}
.status-bar button { min-width: 24px; min-height: 22px; padding: 0 4px; }
.status-position, .status-model { padding: 0 4px; }
.e-window:backdrop,
.e-window:backdrop > contents,
.e-window:backdrop toastoverlay,
.e-window:backdrop toolbarview,
.e-window:backdrop toolbarview > .content,
.e-window:backdrop .editor-content,
.e-window:backdrop .editor,
.e-window:backdrop .project-tree,
.e-window:backdrop .status-bar,
.e-window:backdrop headerbar,
.e-window:backdrop headerbar windowhandle,
.e-window:backdrop tabbar,
.e-window:backdrop tabbar .box,
.e-window:backdrop tabbar .box > scrolledwindow {
  filter: none;
  opacity: 1;
	transition: none;
}
.e-window:backdrop .editor-content,
.e-window:backdrop .project-tree { color: @window_fg_color; }
.e-window:backdrop headerbar,
.e-window:backdrop tabbar { color: @headerbar_fg_color; }
.activity-detail,
.activity-log-detail { color: alpha(@window_fg_color, .62); font-size: .88em; }
.activity-chip {
  background: alpha(@accent_color, .14);
  color: @accent_color;
  border-radius: 999px;
  padding: 1px 7px;
  font-size: .82em;
  font-weight: 600;
}
.activity-progress trough,
.activity-progress progress { min-height: 3px; }
.activity-log-row { padding: 6px; border-radius: 6px; }
.activity-log-row:hover { background: alpha(@window_fg_color, .06); }
.activity-log-row image.error,
.status-bar image.error { color: @error_color; }
`)
	display := gdk.DisplayGetDefault()
	if display != nil {
		gtk.StyleContextAddProviderForDisplay(display, provider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
	}
}

func (w *window) showAbout() {
	about := adw.NewAboutDialog()
	about.SetApplicationName("E")
	about.SetApplicationIcon(appID)
	about.SetDeveloperName("Yauhen Shulitski")
	about.SetVersion(w.options.Version)
	about.SetComments("A fast native text editor with AI autocomplete.")
	about.SetLicenseType(gtk.LicenseMITX11)
	about.Present(w.win)
}

func (w *window) showShortcuts() {
	w.showPalette("Keyboard Shortcuts", "Search shortcuts", func(deliver func([]paletteItem)) {
		deliver(append(w.commandPaletteItems(), editingShortcutItems()...))
	})
}

// editingShortcutItems lists keys that act inside the editor and have no
// command. Choosing one only closes the list.
func editingShortcutItems() []paletteItem {
	hints := []struct{ title, shortcut string }{
		{"Multiple Cursors: Add Cursor Above / Below", "Ctrl+Shift+Up/Down"},
		{"Multiple Cursors: Add or Remove Cursor", "Alt+Click"},
		{"Multiple Cursors: Column Selection", "Shift+Alt+Drag"},
		{"Multiple Cursors: Remove Last Cursor", "Ctrl+U"},
		{"Multiple Cursors: Back to One Cursor", "Escape"},
		{"Suggestion: Accept", "Tab"},
		{"Suggestion: Accept Next Word", "Ctrl+Right"},
		{"Suggestion: Dismiss", "Escape"},
		{"Edit: Redo", "Ctrl+Shift+Z / Ctrl+Y"},
		{"Help: Keyboard Shortcuts", "Ctrl+K Ctrl+S"},
	}
	items := make([]paletteItem, len(hints))
	for i, hint := range hints {
		items[i] = paletteItem{title: hint.title, shortcut: hint.shortcut, run: func() {}}
	}
	return items
}

func (w *window) openPreferences() {
	// Keep one owned dialog. Repeated Ctrl+, activations previously stacked
	// modal layers, so closing the visible Preferences could leave the editor
	// shaded by an older dialog.
	if w.preferences != nil {
		logging.Trace("ui: preferences already open")
		return
	}
	if w.preferencesApplying {
		w.toast("Finishing the previous settings update")
		return
	}
	logging.Trace("ui: open preferences", "active", w.win.IsActive())
	dialog := w.buildPreferences()
	w.preferences = dialog
	dialog.ConnectClosed(func() {
		logging.Trace("ui: preferences closed", "active", w.win.IsActive())
		if w.preferences == dialog {
			w.preferences = nil
		}
		dispatch.Main(func() {
			// Run after AdwDialog has removed its modal host and shade.
			if w.preferences == nil {
				if doc := w.active(); doc != nil {
					doc.view.Widget.GrabFocus()
				}
			}
		})
		glib.TimeoutAdd(250, func() bool {
			// Ensure the first post-dialog frame cannot reuse a shaded snapshot.
			// This is cheap and only runs after Preferences closes.
			if w.preferences == nil {
				w.win.QueueDraw()
				w.content.QueueDraw()
			}
			return false
		})
	})
	dialog.Present(w.win)
}

func boolOr(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func newlineFromConfig(value string, fallback document.Newline) document.Newline {
	switch value {
	case "lf":
		return document.NewlineLF
	case "crlf":
		return document.NewlineCRLF
	case "cr":
		return document.NewlineCR
	default:
		return fallback
	}
}
