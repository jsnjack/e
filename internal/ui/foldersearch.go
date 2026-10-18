package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/jsnjack/e/internal/activity"
	"github.com/jsnjack/e/internal/dispatch"
	"github.com/jsnjack/e/internal/workspace"
)

// maxFolderSearchResults caps one folder search. Each result is a list row
// built on the GTK thread, so a higher cap would stall typing.
const maxFolderSearchResults = 1000

// folderSearch is the sidebar's Search view (Ctrl+Shift+F). As in VS Code,
// it searches the project files while the query is typed and lists the
// matches grouped by file.
type folderSearch struct {
	w         *window
	root      *gtk.Box
	entry     *gtk.SearchEntry
	matchCase *gtk.ToggleButton
	wholeWord *gtk.ToggleButton
	regex     *gtk.ToggleButton
	status    *gtk.Label
	list      *gtk.ListBox
	// rows describes each list row, by row index.
	rows   []folderSearchRow
	groups []*folderSearchGroup
	// current is the row of the match last opened, or -1.
	current int
	// key names the folder and query of the newest search, so the delayed
	// search-changed signal does not repeat a search that already ran.
	key        string
	generation uint64
	cancel     context.CancelFunc
	// stale marks results from another folder; showing the view searches
	// again.
	stale bool
}

// folderSearchGroup is one file's header row and its match rows.
type folderSearchGroup struct {
	path     string
	header   *gtk.ListBoxRow
	chevron  *gtk.Image
	count    *gtk.Label
	rows     []*gtk.ListBoxRow
	expanded bool
}

type folderSearchRow struct {
	group *folderSearchGroup
	// match is nil on a file's header row.
	match *workspace.Match
}

func newFolderSearch(w *window) *folderSearch {
	s := &folderSearch{w: w, current: -1}
	s.root = gtk.NewBox(gtk.OrientationVertical, 6)
	s.root.AddCSSClass("folder-search")
	s.entry = gtk.NewSearchEntry()
	s.entry.SetPlaceholderText("Search")
	s.entry.SetHExpand(true)
	// VS Code waits 300 ms after the last key before it searches.
	s.entry.SetSearchDelay(300)
	s.matchCase = s.option("Aa", "Match Case (Alt+C)")
	s.wholeWord = s.option("<u>ab</u>", "Match Whole Word (Alt+W)")
	s.regex = s.option(".*", "Use Regular Expression (Alt+R)")
	options := gtk.NewBox(gtk.OrientationHorizontal, 0)
	options.AddCSSClass("linked")
	options.Append(s.matchCase)
	options.Append(s.wholeWord)
	options.Append(s.regex)
	query := gtk.NewBox(gtk.OrientationHorizontal, 4)
	query.Append(s.entry)
	query.Append(options)

	s.status = gtk.NewLabel("")
	s.status.SetXAlign(0)
	s.status.SetWrap(true)
	s.status.AddCSSClass("dim-label")
	s.status.AddCSSClass("caption")
	s.status.SetVisible(false)

	s.list = gtk.NewListBox()
	s.list.SetSelectionMode(gtk.SelectionSingle)
	s.list.AddCSSClass("navigation-sidebar")
	s.list.ConnectRowActivated(func(row *gtk.ListBoxRow) { s.activate(row.Index()) })
	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	scroll.SetChild(s.list)

	s.root.Append(query)
	s.root.Append(s.status)
	s.root.Append(scroll)

	s.entry.ConnectSearchChanged(func() { s.search(false) })
	s.entry.ConnectActivate(func() { s.search(true) })
	s.entry.ConnectStopSearch(w.focusEditor)
	entryKeys := gtk.NewEventControllerKey()
	entryKeys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		if keyval != gdk.KEY_Down || state&gtk.AcceleratorGetDefaultModMask() != 0 {
			return false
		}
		if row := s.list.RowAtIndex(0); row != nil {
			s.list.SelectRow(row)
			row.GrabFocus()
		}
		return true
	})
	s.entry.AddController(entryKeys)
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(s.keyPressed)
	s.root.AddController(keys)
	return s
}

// option builds one of the toggles beside the query, which search again
// when flipped.
func (s *folderSearch) option(markup, tooltip string) *gtk.ToggleButton {
	label := gtk.NewLabel("")
	label.SetMarkup(markup)
	button := gtk.NewToggleButton()
	button.SetChild(label)
	button.SetTooltipText(tooltip)
	button.SetFocusOnClick(false)
	button.AddCSSClass("folder-search-option")
	button.ConnectToggled(func() { s.search(false) })
	return button
}

// keyPressed toggles the query options with VS Code's Alt shortcuts.
func (s *folderSearch) keyPressed(keyval, _ uint, state gdk.ModifierType) bool {
	if state&gtk.AcceleratorGetDefaultModMask() != gdk.AltMask {
		return false
	}
	var button *gtk.ToggleButton
	switch gdk.KeyvalToLower(keyval) {
	case gdk.KEY_c:
		button = s.matchCase
	case gdk.KEY_w:
		button = s.wholeWord
	case gdk.KEY_r:
		button = s.regex
	default:
		return false
	}
	button.SetActive(!button.Active())
	return true
}

func (s *folderSearch) query() workspace.Query {
	return workspace.Query{
		Text:      s.entry.Text(),
		MatchCase: s.matchCase.Active(),
		WholeWord: s.wholeWord.Active(),
		Regex:     s.regex.Active(),
	}
}

// search runs the query over the project files off the GTK thread. Unless
// force is set, it skips a query whose results are already shown.
func (s *folderSearch) search(force bool) {
	root := s.w.projectRoot
	query := s.query()
	key := fmt.Sprintf("%s\x00%+v", root, query)
	if !force && !s.stale && key == s.key {
		return
	}
	s.key, s.stale = key, false
	s.stop()
	generation := s.generation
	if query.Text == "" {
		s.clear()
		s.setStatus("")
		return
	}
	if root == "" {
		s.clear()
		s.setStatus("Open a folder to search its files.")
		return
	}
	re, err := query.Compile()
	if err != nil {
		s.clear()
		s.setStatus("Invalid regular expression: " + strings.TrimPrefix(err.Error(), "error parsing regexp: "))
		return
	}
	ctx, cancel := context.WithCancel(s.w.ctx)
	s.cancel = cancel
	// The previous results stay until the new ones arrive, so typing does
	// not make the list flash empty.
	s.setStatus("Searching…")
	operation := s.w.activityHub.Begin(activity.Workspace, "Searching "+filepath.Base(root))
	hidden := s.w.options.Config.Editor.ShowHiddenFiles
	go func() {
		result, err := workspace.Search(ctx, root, re, maxFolderSearchResults, hidden)
		dispatch.Main(func() {
			cancel()
			if generation != s.generation {
				operation.Cancel()
				return
			}
			s.cancel = nil
			if err != nil {
				operation.Fail(err)
				s.clear()
				s.setStatus(fmt.Sprintf("Couldn’t search “%s”.", filepath.Base(root)))
				return
			}
			operation.Finish(activity.Plural(len(result.Matches), "result", "results"), "", false)
			s.show(root, result)
		})
	}()
}

// stop cancels the running search, so its results are dropped.
func (s *folderSearch) stop() {
	s.generation++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// rootChanged drops the results of the previous folder. The new folder is
// searched now when the view is showing, or else when it is shown.
func (s *folderSearch) rootChanged() {
	s.stop()
	s.clear()
	s.setStatus("")
	s.stale = true
	if s.w.sidebar.Visible() && s.w.sidebarStack.VisibleChildName() == "search" {
		s.search(false)
	}
}

func (s *folderSearch) setStatus(text string) {
	s.status.SetLabel(text)
	s.status.SetVisible(text != "")
}

func (s *folderSearch) clear() {
	s.list.RemoveAll()
	s.rows, s.groups, s.current = nil, nil, -1
}

// show lists the matches under one collapsible header per file.
func (s *folderSearch) show(root string, result workspace.SearchResult) {
	s.clear()
	var group *folderSearchGroup
	for i := range result.Matches {
		match := &result.Matches[i]
		if group == nil || group.path != filepath.Join(root, match.Path) {
			group = &folderSearchGroup{path: filepath.Join(root, match.Path), expanded: true}
			group.header = s.headerRow(group, match.Path)
			s.groups = append(s.groups, group)
			s.list.Append(group.header)
			s.rows = append(s.rows, folderSearchRow{group: group})
		}
		row := matchRow(match)
		group.rows = append(group.rows, row)
		s.list.Append(row)
		s.rows = append(s.rows, folderSearchRow{group: group, match: match})
	}
	for _, group := range s.groups {
		group.count.SetLabel(strconv.Itoa(len(group.rows)))
	}
	switch {
	case len(result.Matches) == 0:
		s.setStatus("No results found.")
	case result.Truncated:
		s.setStatus(fmt.Sprintf("The first %d results in %s. Narrow the search to see the rest.",
			len(result.Matches), activity.Plural(result.Files, "file", "files")))
	default:
		s.setStatus(fmt.Sprintf("%s in %s", activity.Plural(len(result.Matches), "result", "results"),
			activity.Plural(result.Files, "file", "files")))
	}
}

func (s *folderSearch) headerRow(group *folderSearchGroup, relative string) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	group.chevron = gtk.NewImageFromIconName("pan-down-symbolic")
	icon := gtk.NewImageFromIconName("text-x-generic-symbolic")
	name := gtk.NewLabel(filepath.Base(relative))
	name.SetXAlign(0)
	name.SetEllipsize(pango.EllipsizeMiddle)
	folder := gtk.NewLabel("")
	if dir := filepath.Dir(relative); dir != "." {
		folder.SetLabel(dir)
	}
	folder.SetXAlign(0)
	folder.SetHExpand(true)
	folder.SetEllipsize(pango.EllipsizeStart)
	folder.AddCSSClass("dim-label")
	folder.AddCSSClass("caption")
	group.count = gtk.NewLabel("")
	group.count.SetVAlign(gtk.AlignCenter)
	group.count.AddCSSClass("folder-search-count")
	box.Append(group.chevron)
	box.Append(icon)
	box.Append(name)
	box.Append(folder)
	box.Append(group.count)
	row := gtk.NewListBoxRow()
	row.SetChild(box)
	row.SetTooltipText(relative)
	return row
}

// matchRow shows the matched line with the match highlighted.
func matchRow(match *workspace.Match) *gtk.ListBoxRow {
	label := gtk.NewLabel("")
	label.SetMarkup(glib.MarkupEscapeText(match.Before) +
		`<span weight="bold" background="#f8e45c" bgalpha="45%">` + glib.MarkupEscapeText(match.Text) + "</span>" +
		glib.MarkupEscapeText(match.After))
	label.SetXAlign(0)
	label.SetEllipsize(pango.EllipsizeEnd)
	label.AddCSSClass("folder-search-line")
	row := gtk.NewListBoxRow()
	row.SetChild(label)
	row.SetTooltipText(fmt.Sprintf("Line %d", match.Line+1))
	return row
}

// activate opens a match, or folds a file's matches away.
func (s *folderSearch) activate(index int) {
	if index < 0 || index >= len(s.rows) {
		return
	}
	row := s.rows[index]
	if row.match == nil {
		s.setExpanded(row.group, !row.group.expanded)
		return
	}
	s.current = index
	s.open(row)
}

func (s *folderSearch) setExpanded(group *folderSearchGroup, expanded bool) {
	group.expanded = expanded
	for _, row := range group.rows {
		row.SetVisible(expanded)
	}
	if expanded {
		group.chevron.SetFromIconName("pan-down-symbolic")
	} else {
		group.chevron.SetFromIconName("pan-end-symbolic")
	}
}

func (s *folderSearch) open(row folderSearchRow) {
	match := row.match
	s.w.openPathAt(row.group.path,
		textPosition{line: match.Line, column: match.Column},
		textPosition{line: match.EndLine, column: match.EndColumn})
}

// navigate opens the next or previous match, wrapping around, as F4 and
// Shift+F4 do in VS Code.
func (s *folderSearch) navigate(direction int) {
	count := len(s.rows)
	if count == 0 {
		return
	}
	index := s.current
	if index < 0 && direction < 0 {
		index = count
	}
	for range count {
		index = ((index+direction)%count + count) % count
		if s.rows[index].match != nil {
			break
		}
	}
	row := s.rows[index]
	if !row.group.expanded {
		s.setExpanded(row.group, true)
	}
	if listRow := s.list.RowAtIndex(index); listRow != nil {
		s.list.SelectRow(listRow)
		// Focusing the row scrolls it into view; opening the match then
		// moves focus to the editor.
		listRow.GrabFocus()
	}
	s.current = index
	s.open(row)
}

// showFolderSearch opens the sidebar's Search view. A selection on one line
// becomes the query, as in VS Code.
func (w *window) showFolderSearch() {
	doc := w.active()
	if w.projectRoot == "" {
		if doc == nil || doc.path == "" {
			w.toast("Open a folder to search its files")
			return
		}
		w.setProjectRoot(projectRootFor(doc.path, pathExists))
		if w.projectRoot == "" {
			return
		}
	}
	s := w.folderSearch
	if doc != nil {
		if seed := doc.searchSeed(false); seed != "" {
			s.entry.SetText(seed)
		}
	}
	w.showSidebarView("search")
	s.search(false)
	s.entry.GrabFocus()
	s.entry.SelectRegion(0, -1)
}

func (w *window) focusEditor() {
	if doc := w.active(); doc != nil {
		doc.view.Widget.GrabFocus()
	}
}
