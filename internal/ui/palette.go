package ui

import (
	"sort"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

const maxPaletteResults = 80

type paletteItem struct {
	id       string
	title    string
	detail   string
	shortcut string
	icon     string
	// complete is the entry text that Tab puts in the path picker.
	complete string
	// path is the absolute file or folder a row opens, for removing
	// duplicates when picker sources are merged.
	path string
	// boost is added to the match score, so recent paths rank higher.
	boost int
	run   func()
	// stay keeps the palette open after run, for steps such as entering a
	// folder in the path picker.
	stay bool
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
		{id: "open-folder", title: "File: Open Folder…", shortcut: "Ctrl+K Ctrl+O", run: w.chooseFolder},
		{id: "save", title: "File: Save", shortcut: "Ctrl+S", run: w.saveActive},
		{id: "save-as", title: "File: Save As…", shortcut: "Ctrl+Shift+S", run: w.saveActiveAs},
		{id: "close", title: "File: Close Tab", shortcut: "Ctrl+W", run: func() {
			if page := w.tabs.SelectedPage(); page != nil {
				w.tabs.ClosePage(page)
			}
		}},
		{id: "quick-open", title: "Go: Go to File or Folder", shortcut: "Ctrl+P", run: w.showQuickOpen},
		{id: "sidebar", title: "View: Toggle Primary Side Bar", shortcut: "Ctrl+B", run: w.toggleSidebar},
		{id: "explorer", title: "View: Show Explorer", shortcut: "Ctrl+Shift+E", run: func() { w.showSidebarView("files") }},
		{id: "markdown-preview", title: "View: Open Markdown Preview", shortcut: "Ctrl+Shift+V", run: func() {
			if doc := w.active(); doc != nil {
				doc.toggleMarkdownPreview()
			}
		}},
		{id: "show-changes", title: "Git: Show Changes in gdiff", shortcut: "Ctrl+Alt+D", run: w.showChangesInGdiff},
		{id: "find", title: "Edit: Find", shortcut: "Ctrl+F", run: func() { w.showSearch(false) }},
		{id: "replace", title: "Edit: Replace", shortcut: "Ctrl+H", run: func() { w.showSearch(true) }},
		{id: "find-in-folder", title: "Search: Find in Folder", shortcut: "Ctrl+Shift+F", run: w.showFolderSearch},
		{id: "next-search-result", title: "Search: Go to Next Result", shortcut: "F4", run: func() { w.folderSearch.navigate(1) }},
		{id: "previous-search-result", title: "Search: Go to Previous Result", shortcut: "Shift+F4", run: func() { w.folderSearch.navigate(-1) }},
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

func (w *window) showPalette(title, placeholder string, load func(func([]paletteItem))) {
	p := w.newPalette(title, placeholder)
	var items []paletteItem
	p.entry.ConnectSearchChanged(func() {
		p.show(filterPaletteItems(items, p.entry.Text(), maxPaletteResults))
	})
	load(func(loaded []paletteItem) {
		if p.closed {
			return
		}
		items = withSearchKeys(loaded)
		p.show(filterPaletteItems(items, p.entry.Text(), maxPaletteResults))
	})
	p.present()
}

// palette is the dialog behind every list picker: a search entry over a
// list of rows. Callers decide which items show for the entry text.
type palette struct {
	w       *window
	dialog  *adw.Dialog
	entry   *gtk.SearchEntry
	list    *gtk.ListBox
	visible []paletteItem
	closed  bool
	// key, when set, sees each key press before the list does.
	key func(keyval uint, state gdk.ModifierType) bool
}

func (w *window) newPalette(title, placeholder string) *palette {
	p := &palette{w: w, dialog: adw.NewDialog()}
	p.dialog.ConnectClosed(func() { p.closed = true })
	p.dialog.SetTitle(title)
	p.dialog.SetContentWidth(680)
	p.dialog.SetContentHeight(520)
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.SetMarginTop(12)
	box.SetMarginBottom(12)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	p.entry = gtk.NewSearchEntry()
	p.entry.SetPlaceholderText(placeholder)
	p.list = gtk.NewListBox()
	p.list.SetSelectionMode(gtk.SelectionSingle)
	p.list.AddCSSClass("boxed-list")
	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	scroll.SetChild(p.list)
	box.Append(p.entry)
	box.Append(scroll)
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		if paletteDismissKey(keyval) {
			p.dialog.Close()
			return true
		}
		if p.key != nil && p.key(keyval, state) {
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
		if row := p.list.SelectedRow(); row != nil {
			current = row.Index()
		}
		if row := p.list.RowAtIndex(nextPaletteIndex(current, len(p.visible), direction)); row != nil {
			p.list.SelectRow(row)
		}
		return true
	})
	box.AddController(keys)
	p.dialog.SetChild(box)
	p.entry.ConnectActivate(func() {
		if row := p.list.SelectedRow(); row != nil {
			p.activate(row.Index())
		}
	})
	p.list.ConnectRowActivated(func(row *gtk.ListBoxRow) { p.activate(row.Index()) })
	return p
}

// show replaces the rows and selects the first one.
func (p *palette) show(items []paletteItem) {
	p.list.RemoveAll()
	p.visible = items
	for _, item := range items {
		p.list.Append(paletteRow(item))
	}
	if row := p.list.RowAtIndex(0); row != nil {
		p.list.SelectRow(row)
	}
}

// selected returns the highlighted item.
func (p *palette) selected() (paletteItem, bool) {
	row := p.list.SelectedRow()
	if row == nil {
		return paletteItem{}, false
	}
	return paletteItemAt(p.visible, row.Index())
}

func (p *palette) activate(index int) {
	item, ok := paletteItemAt(p.visible, index)
	if !ok {
		return
	}
	if !item.stay {
		p.dialog.Close()
	}
	item.run()
}

func (p *palette) present() {
	p.w.lastPalette = p
	p.dialog.Present(p.w.win)
	p.entry.GrabFocus()
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

// filterPaletteItems ranks items for query, best first. Spaces separate
// words: every word must match on its own, and the scores add up, so
// "dow otter" finds Downloads/bright-otter.json without matching letters
// scattered across unrelated names.
func filterPaletteItems(items []paletteItem, query string, limit int) []paletteItem {
	type match struct {
		item  paletteItem
		score int
	}
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return items[:min(len(items), max(limit, 0))]
	}
	wordRunes := make([][]rune, len(words))
	for i, word := range words {
		wordRunes[i] = []rune(word)
	}
	matches := make([]match, 0, min(len(items), 1024))
	for _, item := range items {
		key := item.key
		if key == nil {
			key = []rune(strings.ToLower(item.title + " " + item.detail))
		}
		total := item.boost
		for _, word := range wordRunes {
			score := fuzzyScoreRunes(word, key)
			if score < 0 {
				total = -1
				break
			}
			total += score
		}
		if total >= 0 {
			matches = append(matches, match{item: item, score: total})
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
	if item.icon != "" {
		icon := gtk.NewImageFromIconName(item.icon)
		icon.AddCSSClass("dim-label")
		content.Append(icon)
	}
	labels := gtk.NewBox(gtk.OrientationVertical, 2)
	labels.SetHExpand(true)
	// Long names are cut in the middle; without this a row's natural width
	// would stretch the dialog past the window.
	title := gtk.NewLabel(item.title)
	title.SetXAlign(0)
	title.SetHExpand(true)
	title.SetEllipsize(pango.EllipsizeMiddle)
	labels.Append(title)
	if item.detail != "" && item.detail != "." {
		detail := gtk.NewLabel(item.detail)
		detail.SetXAlign(0)
		detail.SetEllipsize(pango.EllipsizeStart)
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

// Scores for one word. An unbroken match beats any scattered one, and one
// that starts a word or path segment beats one in the middle.
const (
	unbrokenRuneScore = 15
	wordStartBonus    = 20
)

func fuzzyScoreRunes(queryRunes, candidateRunes []rune) int {
	if len(queryRunes) == 0 {
		return 0
	}
	if at := unbrokenMatch(candidateRunes, queryRunes); at >= 0 {
		score := unbrokenRuneScore * len(queryRunes)
		if wordStart(candidateRunes, at) {
			score += wordStartBonus
		}
		return score - len(candidateRunes)/8
	}
	// A scattered match may start at most one piece in the middle of a word;
	// "mdp" matches "Markdown Preview", but "otter" spread through
	// "tickets_by_version" does not.
	score, previous, searchFrom, midWord := 0, -2, 0, 0
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
		switch {
		case position == previous+1:
			score += 8
		case wordStart(candidateRunes, position):
			score += 6
		default:
			if midWord++; midWord > 1 {
				return -1
			}
		}
		previous, searchFrom = position, position+1
	}
	return score - len(candidateRunes)/8
}

func wordStart(text []rune, at int) bool {
	return at == 0 || strings.ContainsRune(" /_-:.", text[at-1])
}

// unbrokenMatch returns where part appears in text, preferring a word
// start, or -1.
func unbrokenMatch(text, part []rune) int {
	found := -1
	for start := 0; start+len(part) <= len(text); start++ {
		if !runesAt(text, part, start) {
			continue
		}
		if wordStart(text, start) {
			return start
		}
		if found < 0 {
			found = start
		}
	}
	return found
}

func runesAt(text, part []rune, start int) bool {
	for i, r := range part {
		if text[start+i] != r {
			return false
		}
	}
	return true
}
