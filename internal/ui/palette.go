package ui

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/activity"
	"github.com/jsnjack/e/internal/dispatch"
	"github.com/jsnjack/e/internal/workspace"
)

const maxPaletteResults = 80

type paletteItem struct {
	id       string
	title    string
	detail   string
	shortcut string
	run      func()
	// key is the lowercased "title detail" text, built once per item so
	// filtering does not allocate per keystroke.
	key []rune
}

func withSearchKeys(items []paletteItem) []paletteItem {
	for i := range items {
		if items[i].key == nil {
			items[i].key = []rune(strings.ToLower(items[i].title + " " + items[i].detail))
		}
	}
	return items
}

func (w *window) showCommandPalette() {
	w.showPalette("Command Palette", "Type a command", func(deliver func([]paletteItem)) {
		deliver(w.commandPaletteItems())
	})
}

func (w *window) commandPaletteItems() []paletteItem {
	return []paletteItem{
		{id: "new", title: "File: New Text File", shortcut: "Ctrl+N", run: func() { w.newDocument(nil) }},
		{id: "open", title: "File: Open File…", shortcut: "Ctrl+O", run: w.chooseOpen},
		{id: "save", title: "File: Save", shortcut: "Ctrl+S", run: w.saveActive},
		{id: "save-as", title: "File: Save As…", shortcut: "Ctrl+Shift+S", run: w.saveActiveAs},
		{id: "close", title: "File: Close Tab", shortcut: "Ctrl+W", run: func() {
			if page := w.tabs.SelectedPage(); page != nil {
				w.tabs.ClosePage(page)
			}
		}},
		{id: "quick-open", title: "Go: Quick Open", shortcut: "Ctrl+P", run: w.showQuickOpen},
		{id: "sidebar", title: "View: Toggle Primary Side Bar", shortcut: "Ctrl+B", run: w.toggleSidebar},
		{id: "markdown-preview", title: "View: Open Markdown Preview", shortcut: "Ctrl+Shift+V", run: func() {
			if doc := w.active(); doc != nil {
				doc.toggleMarkdownPreview()
			}
		}},
		{id: "find", title: "Edit: Find", shortcut: "Ctrl+F", run: func() { w.showSearch(false) }},
		{id: "replace", title: "Edit: Replace", shortcut: "Ctrl+H", run: func() { w.showSearch(true) }},
		{id: "undo", title: "Edit: Undo", shortcut: "Ctrl+Z", run: func() {
			if doc := w.active(); doc != nil && doc.buffer.CanUndo() {
				doc.buffer.Undo()
			}
		}},
		{id: "redo", title: "Edit: Redo", shortcut: "Ctrl+Shift+Z", run: func() {
			if doc := w.active(); doc != nil && doc.buffer.CanRedo() {
				doc.buffer.Redo()
			}
		}},
		{id: "select-next", title: "Selection: Add Next Occurrence", shortcut: "Ctrl+D", run: func() {
			if doc := w.active(); doc != nil {
				doc.selectNextOccurrence()
			}
		}},
		{id: "select-all", title: "Selection: Select All Occurrences", shortcut: "Ctrl+Shift+L", run: func() {
			if doc := w.active(); doc != nil {
				doc.selectAllOccurrences()
			}
		}},
		{id: "next-tab", title: "View: Next Tab", shortcut: "Ctrl+Tab", run: func() { w.selectRelativeTab(1) }},
		{id: "previous-tab", title: "View: Previous Tab", shortcut: "Ctrl+Shift+Tab", run: func() { w.selectRelativeTab(-1) }},
		{id: "ai-complete", title: "AI: Trigger Inline Suggestion", shortcut: "Alt+\\", run: func() {
			if doc := w.active(); doc != nil {
				doc.requestCompletion(true)
			}
		}},
		{id: "ai-transform", title: "AI: Transform Selection…", run: w.showAITransform},
		{id: "lsp-complete", title: "Code: Trigger Language Completion", shortcut: "Ctrl+Space", run: func() {
			if doc := w.active(); doc != nil {
				doc.requestLSPCompletion()
			}
		}},
		{id: "preferences", title: "Preferences: Open Settings", shortcut: "Ctrl+,", run: w.openPreferences},
	}
}

func (w *window) showQuickOpen() {
	root := w.projectRoot
	if root == "" {
		doc := w.active()
		if doc == nil || doc.path == "" {
			w.toast("Open a folder to use Quick Open")
			return
		}
		root = projectRootFor(doc.path, pathExists)
	}
	operation := w.activityHub.Begin(activity.Workspace, "Indexing project files")
	w.showPalette("Quick Open", "Search files by name", func(deliver func([]paletteItem)) {
		go func() {
			files, err := workspace.Files(w.ctx, root, 100_000)
			if err != nil {
				operation.Fail(err)
				return
			}
			items := make([]paletteItem, 0, len(files))
			for _, relative := range files {
				relative := relative
				items = append(items, paletteItem{
					title: filepath.Base(relative), detail: filepath.Dir(relative),
					run: func() { w.openPath(filepath.Join(root, relative)) },
				})
			}
			withSearchKeys(items)
			dispatch.Main(func() {
				deliver(items)
				operation.Finish(activity.Plural(len(items), "file", "files"), "", false)
			})
		}()
	})
}

func (w *window) showPalette(title, placeholder string, load func(func([]paletteItem))) {
	dialog := adw.NewDialog()
	closed := false
	dialog.ConnectClosed(func() { closed = true })
	dialog.SetTitle(title)
	dialog.SetContentWidth(680)
	dialog.SetContentHeight(520)
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.SetMarginTop(12)
	box.SetMarginBottom(12)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	entry := gtk.NewSearchEntry()
	entry.SetPlaceholderText(placeholder)
	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionSingle)
	list.AddCSSClass("boxed-list")
	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	scroll.SetChild(list)
	box.Append(entry)
	box.Append(scroll)
	var items []paletteItem
	var visibleItems []paletteItem
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		if paletteDismissKey(keyval) {
			dialog.Close()
			return true
		}
		direction := 0
		switch keyval {
		case gdk.KEY_Up:
			direction = -1
		case gdk.KEY_Down:
			direction = 1
		default:
			return false
		}
		current := 0
		if row := list.SelectedRow(); row != nil {
			current = row.Index()
		}
		if row := list.RowAtIndex(nextPaletteIndex(current, len(visibleItems), direction)); row != nil {
			list.SelectRow(row)
		}
		return true
	})
	box.AddController(keys)
	dialog.SetChild(box)

	refresh := func() {
		list.RemoveAll()
		visibleItems = filterPaletteItems(items, entry.Text(), maxPaletteResults)
		for _, item := range visibleItems {
			row := paletteRow(item)
			list.Append(row)
		}
		if row := list.RowAtIndex(0); row != nil {
			list.SelectRow(row)
		}
	}
	activate := func(row *gtk.ListBoxRow) {
		if item, ok := paletteItemAt(visibleItems, row.Index()); ok {
			dialog.Close()
			item.run()
		}
	}
	entry.ConnectSearchChanged(refresh)
	entry.ConnectActivate(func() {
		if row := list.SelectedRow(); row != nil {
			activate(row)
		}
	})
	list.ConnectRowActivated(activate)
	load(func(loaded []paletteItem) {
		if closed {
			return
		}
		items = withSearchKeys(loaded)
		refresh()
	})
	dialog.Present(w.win)
	entry.GrabFocus()
}

func paletteDismissKey(keyval uint) bool { return keyval == gdk.KEY_Escape }

func paletteItemAt(items []paletteItem, index int) (paletteItem, bool) {
	if index < 0 || index >= len(items) {
		return paletteItem{}, false
	}
	return items[index], true
}

func nextPaletteIndex(current, count, direction int) int {
	if count <= 0 {
		return -1
	}
	return min(max(current+direction, 0), count-1)
}

func filterPaletteItems(items []paletteItem, query string, limit int) []paletteItem {
	type match struct {
		item  paletteItem
		score int
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return items[:min(len(items), max(limit, 0))]
	}
	queryRunes := []rune(strings.ToLower(query))
	matches := make([]match, 0, min(len(items), 1024))
	for _, item := range items {
		key := item.key
		if key == nil {
			key = []rune(strings.ToLower(item.title + " " + item.detail))
		}
		if score := fuzzyScoreRunes(queryRunes, key); score >= 0 {
			matches = append(matches, match{item: item, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	filtered := make([]paletteItem, len(matches))
	for i := range matches {
		filtered[i] = matches[i].item
	}
	return filtered
}

func paletteRow(item paletteItem) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	content := gtk.NewBox(gtk.OrientationHorizontal, 12)
	content.SetMarginTop(8)
	content.SetMarginBottom(8)
	content.SetMarginStart(10)
	content.SetMarginEnd(10)
	labels := gtk.NewBox(gtk.OrientationVertical, 2)
	title := gtk.NewLabel(item.title)
	title.SetXAlign(0)
	title.SetHExpand(true)
	labels.Append(title)
	if item.detail != "" && item.detail != "." {
		detail := gtk.NewLabel(item.detail)
		detail.SetXAlign(0)
		detail.AddCSSClass("dim-label")
		detail.AddCSSClass("caption")
		labels.Append(detail)
	}
	content.Append(labels)
	if item.shortcut != "" {
		shortcut := gtk.NewLabel(item.shortcut)
		shortcut.AddCSSClass("dim-label")
		content.Append(shortcut)
	}
	row.SetChild(content)
	return row
}

// fuzzyScore rewards ordered, consecutive, and word-boundary matches.
func fuzzyScore(query, candidate string) int {
	return fuzzyScoreRunes([]rune(strings.ToLower(query)), []rune(strings.ToLower(candidate)))
}

func fuzzyScoreRunes(queryRunes, candidateRunes []rune) int {
	if len(queryRunes) == 0 {
		return 0
	}
	score, previous, searchFrom := 0, -2, 0
	for _, wanted := range queryRunes {
		position := -1
		for index := searchFrom; index < len(candidateRunes); index++ {
			if candidateRunes[index] == wanted {
				position = index
				break
			}
		}
		if position == -1 {
			return -1
		}
		score += 5
		if position == previous+1 {
			score += 8
		}
		if position == 0 || strings.ContainsRune(" /_-:.", candidateRunes[position-1]) {
			score += 6
		}
		previous, searchFrom = position, position+1
	}
	return score - len(candidateRunes)/8
}
