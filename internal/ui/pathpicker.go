package ui

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/jsnjack/e/internal/activity"
	"github.com/jsnjack/e/internal/dispatch"
	"github.com/jsnjack/e/internal/recent"
	"github.com/jsnjack/e/internal/workspace"
)

// maxPathPickerEntries caps one directory listing in the path picker.
const maxPathPickerEntries = 5000

// recentBoost is added to the score of a recent path.
const recentBoost = 15

// showQuickOpen is the one picker for files and folders (Ctrl+P).
//   - Text that names an existing folder ("/", "~/", or a relative path with
//     a "/") lists that folder. Relative paths resolve against the project,
//     or home without one.
//   - Other text fuzzy-matches the project's files, then recent paths and
//     home, then a fuzzy folder walk from home: "dow bana" finds
//     ~/Downloads/banana.txt.
func (w *window) showQuickOpen() {
	home, _ := os.UserHomeDir()
	root := w.projectRoot
	if root == "" {
		if doc := w.active(); doc != nil && doc.path != "" {
			root = projectRootFor(doc.path, pathExists)
		}
	}
	base := root
	if base == "" {
		base = home
	}
	p := w.newPalette("Go to File", "Search files and recent paths, or type / or ~/ to browse")
	var files []paletteItem
	var refresh func()
	browser := w.newPathBrowser(p, home, base, func() { refresh() })
	refresh = func() {
		text := p.entry.Text()
		if browser.show(text) {
			return
		}
		// Every source is ranked by the same word scores, then duplicates
		// of one path keep their best row.
		var homeEntries []workspace.Entry
		if home != "" {
			homeEntries, _ = browser.listing(home)
		}
		if strings.TrimSpace(text) == "" {
			p.show(appendNewPaths(nil, w.recentPathItems(home, homeEntries, browser.setText)))
			return
		}
		candidates := filterPaletteItems(files, text, 4*maxPaletteResults)
		candidates = append(candidates, w.recentPathItems(home, homeEntries, browser.setText)...)
		if tokens := pathTokens(text); len(tokens) >= 2 && home != "" {
			candidates = append(candidates, browser.fuzzyPath(home, tokens)...)
		}
		p.show(appendNewPaths(nil, filterPaletteItems(candidates, text, 0)))
	}
	p.entry.ConnectSearchChanged(refresh)
	if root != "" {
		w.indexProjectFiles(root, func(loaded []paletteItem) {
			if p.closed {
				return
			}
			// A recent project file keeps its short project row.
			recents := make(map[string]bool, len(w.recent))
			for _, entry := range w.recent {
				recents[entry.Path] = true
			}
			for i := range loaded {
				if recents[loaded[i].path] {
					loaded[i].boost = recentBoost
				}
			}
			files = loaded
			refresh()
		})
	}
	refresh()
	p.present()
}

// indexProjectFiles lists root's files off the GTK thread for the picker.
func (w *window) indexProjectFiles(root string, done func([]paletteItem)) {
	operation := w.activityHub.Begin(activity.Workspace, "Indexing project files")
	showHidden := w.options.Config.Editor.ShowHiddenFiles
	go func() {
		relatives, err := workspace.Files(w.ctx, root, 100_000, showHidden)
		if err != nil {
			operation.Fail(err)
			return
		}
		items := make([]paletteItem, 0, len(relatives))
		for _, relative := range relatives {
			path := filepath.Join(root, relative)
			items = append(items, paletteItem{
				title: filepath.Base(relative), detail: filepath.Dir(relative),
				icon: "text-x-generic-symbolic", complete: relative, path: path,
				// Name first so name matches rank high, then the full path so
				// text such as "ui/pal" matches across the separator.
				key: []rune(strings.ToLower(filepath.Base(relative) + " " + relative)),
				run: func() { w.openPath(path) },
			})
		}
		dispatch.Main(func() {
			operation.Finish(activity.Plural(len(items), "file", "files"), "", false)
			done(items)
		})
	}()
}

// pathTokens splits text on spaces and "/" for the fuzzy folder walk.
func pathTokens(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == '/' })
}

// appendNewPaths adds the rows of more whose path is not in items yet, up to
// maxPaletteResults rows.
func appendNewPaths(items, more []paletteItem) []paletteItem {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.path] = true
	}
	for _, item := range more {
		if !seen[item.path] && len(items) < maxPaletteResults {
			seen[item.path] = true
			items = append(items, item)
		}
	}
	return items
}

// fuzzyPathWidth is how many folders each word but the last keeps in the
// fuzzy folder walk.
const fuzzyPathWidth = 8

type fuzzyCandidate struct {
	path  string
	dir   bool
	score int
}

// fuzzyPath walks down from start one word at a time. Each word but the
// last fuzzy-matches folder names and keeps the best fuzzyPathWidth; the
// last word matches files and folders inside them. Scores add up along the
// path, so "dow bana" ranks ~/Downloads/banana.txt first. Folders still
// loading are left out until their listing arrives and the palette
// refreshes.
func (b *pathBrowser) fuzzyPath(start string, tokens []string) []paletteItem {
	level := []fuzzyCandidate{{path: start, dir: true}}
	for i, token := range tokens {
		last := i == len(tokens)-1
		hidden := strings.HasPrefix(token, ".")
		var next []fuzzyCandidate
		for _, parent := range level {
			entries, ok := b.listing(parent.path)
			if !ok {
				continue
			}
			for _, entry := range entries {
				if !last && !entry.Dir || !hidden && strings.HasPrefix(entry.Name, ".") {
					continue
				}
				if score := fuzzyScore(token, entry.Name); score >= 0 {
					next = append(next, fuzzyCandidate{filepath.Join(parent.path, entry.Name), entry.Dir, parent.score + score})
				}
			}
		}
		sort.SliceStable(next, func(i, j int) bool { return next[i].score > next[j].score })
		width := fuzzyPathWidth
		if last {
			width = maxPaletteResults
		}
		level = next[:min(len(next), width)]
	}
	items := make([]paletteItem, 0, len(level))
	for _, candidate := range level {
		path := candidate.path
		item := paletteItem{
			title: filepath.Base(path), detail: homeRelative(filepath.Dir(path)),
			icon: "text-x-generic-symbolic", complete: pathQueryText(path, candidate.dir), path: path,
			key: pathKey(path), run: func() { b.w.openPath(path) },
		}
		if candidate.dir {
			item.icon, item.title, item.stay = "folder-symbolic", item.title+"/", true
			item.run = func() { b.setText(item.complete) }
		}
		items = append(items, item)
	}
	return items
}

// pathBrowser lists one folder at a time inside a palette. Tab completes the
// selected row, Enter on a folder row enters it, and Backspace after a "/"
// goes up one folder. Listings are read off the GTK thread and cached for
// the palette's lifetime.
type pathBrowser struct {
	w        *window
	p        *palette
	home     string
	base     string
	listings map[string][]workspace.Entry
	failed   map[string]bool
	loading  map[string]bool
	refresh  func()
}

// newPathBrowser resolves relative text against base. It installs the
// palette's Tab and Backspace handling.
func (w *window) newPathBrowser(p *palette, home, base string, refresh func()) *pathBrowser {
	b := &pathBrowser{
		w: w, p: p, home: home, base: base, refresh: refresh,
		listings: map[string][]workspace.Entry{}, failed: map[string]bool{}, loading: map[string]bool{},
	}
	p.entry.SetSearchDelay(0)
	p.key = b.key
	return b
}

func (b *pathBrowser) setText(text string) {
	b.p.entry.SetText(text)
	b.p.entry.SetPosition(-1)
}

// listing returns dir's cached entries, or starts a background read and
// reports false.
func (b *pathBrowser) listing(dir string) ([]workspace.Entry, bool) {
	if entries, ok := b.listings[dir]; ok {
		return entries, true
	}
	if b.failed[dir] {
		return nil, false
	}
	if !b.loading[dir] {
		b.loading[dir] = true
		go func() {
			entries, err := workspace.ListDir(dir, maxPathPickerEntries)
			dispatch.Main(func() {
				delete(b.loading, dir)
				if err != nil {
					slog.Debug("path picker: list folder", "path", dir, "err", err)
					b.failed[dir] = true
				} else {
					b.listings[dir] = entries
				}
				if !b.p.closed {
					b.refresh()
				}
			})
		}()
	}
	return nil, false
}

// show lists the folder that text names and reports true. A "/" or "~/"
// path whose folder does not exist is walked fuzzily. It reports false for
// text that is not a path, and for relative text whose folder does not
// exist, so the caller can fuzzy-search instead.
func (b *pathBrowser) show(text string) bool {
	query, ok := parsePathQuery(text, b.home, b.base)
	if !ok {
		return false
	}
	if entries, ok := b.listing(query.dir); ok {
		b.p.show(b.w.folderPathItems(query, entries, b.display(query.relative), b.setText))
		return true
	}
	if b.failed[query.dir] {
		if query.relative {
			return false
		}
		// "~/dow/bana" names no folder; walk it fuzzily instead.
		start, rest := "/", text
		if strings.HasPrefix(text, "~") {
			start, rest = b.home, strings.TrimPrefix(text, "~")
		}
		b.p.show(b.fuzzyPath(start, pathTokens(rest)))
	}
	// While the folder loads, the previous rows stay.
	return true
}

// display returns how completions are written: relative to base when the
// user typed a relative path, else with "~" for home.
func (b *pathBrowser) display(relative bool) func(string, bool) string {
	if !relative {
		return pathQueryText
	}
	return func(path string, dir bool) string {
		text, err := filepath.Rel(b.base, path)
		if err != nil || text == ".." || strings.HasPrefix(text, "../") {
			return pathQueryText(path, dir)
		}
		if dir {
			text += "/"
		}
		return text
	}
}

func (b *pathBrowser) key(keyval uint, state gdk.ModifierType) bool {
	switch keyval {
	case gdk.KEY_Tab:
		if item, ok := b.p.selected(); ok && item.complete != "" {
			b.setText(item.complete)
		}
		return true
	case gdk.KEY_BackSpace:
		if state&(gdk.ControlMask|gdk.AltMask) != 0 {
			return false
		}
		if parent, ok := parentPathQuery(b.p.entry.Text(), b.home, b.base); ok {
			b.setText(parent)
			return true
		}
	}
	return false
}

// pathQuery is browsing text split into an absolute folder and the filter
// typed after its last "/".
type pathQuery struct {
	dir, filter string
	// relative is set when the text was relative to the base folder.
	relative bool
}

// parsePathQuery reads text that starts with "/" or "~/", or relative text
// with a "/" when base is set. It reports false for other text.
func parsePathQuery(text, home, base string) (pathQuery, bool) {
	relative := false
	switch {
	case strings.HasPrefix(text, "/"):
	case home != "" && (text == "~" || strings.HasPrefix(text, "~/")):
		text = home + strings.TrimPrefix(text, "~")
		if !strings.HasSuffix(text, "/") && filepath.Clean(text) == filepath.Clean(home) {
			text += "/"
		}
	case base != "" && strings.Contains(text, "/"):
		text, relative = filepath.Join(base, text)+suffixSlash(text), true
	default:
		return pathQuery{}, false
	}
	cut := strings.LastIndex(text, "/")
	return pathQuery{dir: filepath.Clean(text[:cut+1]), filter: text[cut+1:], relative: relative}, true
}

// suffixSlash keeps the trailing "/" that filepath.Join drops.
func suffixSlash(text string) string {
	if strings.HasSuffix(text, "/") {
		return "/"
	}
	return ""
}

// parentPathQuery is the text one folder up from text that ends in "/". One
// level above base, relative text becomes empty.
func parentPathQuery(text, home, base string) (string, bool) {
	if !strings.HasSuffix(text, "/") || text == "/" {
		return "", false
	}
	query, ok := parsePathQuery(text, home, base)
	if !ok {
		return "", false
	}
	parent := filepath.Dir(query.dir)
	if query.relative {
		if parent == filepath.Clean(base) {
			return "", true
		}
		if rel, err := filepath.Rel(base, parent); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return rel + "/", true
		}
	}
	return pathQueryText(parent, true), true
}

// pathQueryText is how the entry shows path: under home with "~", and with a
// trailing "/" for folders so the next keystroke filters inside them.
func pathQueryText(path string, dir bool) string {
	text := homeRelative(path)
	if dir && !strings.HasSuffix(text, "/") {
		text += "/"
	}
	return text
}

// folderPathItems lists the query's folder for its filter. Dot entries show
// only when the filter starts with ".", as in a shell. An empty filter puts
// "open this folder" first.
func (w *window) folderPathItems(query pathQuery, entries []workspace.Entry, display func(string, bool) string, setText func(string)) []paletteItem {
	dir, filter := query.dir, query.filter
	items := w.folderEntryItems(dir, entries, strings.HasPrefix(filter, "."), display, setText)
	items = filterPaletteItems(items, filter, maxPaletteResults)
	if filter == "" {
		open := paletteItem{
			title: "Open “" + filepath.Base(dir) + "” as Project", detail: homeRelative(dir),
			icon: "folder-open-symbolic", complete: display(dir, true),
			run: func() { w.setProjectRoot(dir) },
		}
		items = append([]paletteItem{open}, items...)
	}
	return items
}

// folderEntryItems turns a listing into rows. A folder row enters the folder;
// a file row opens the file. Rows match on the entry name only.
func (w *window) folderEntryItems(dir string, entries []workspace.Entry, showHidden bool, display func(string, bool) string, setText func(string)) []paletteItem {
	items := make([]paletteItem, 0, len(entries))
	for _, entry := range entries {
		if !showHidden && strings.HasPrefix(entry.Name, ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name)
		item := paletteItem{title: entry.Name, key: []rune(strings.ToLower(entry.Name)), complete: display(path, entry.Dir), path: path}
		if entry.Dir {
			item.icon, item.title, item.stay = "folder-symbolic", entry.Name+"/", true
			item.run = func() { setText(item.complete) }
		} else {
			item.icon = "text-x-generic-symbolic"
			item.run = func() { w.openPath(path) }
		}
		items = append(items, item)
	}
	return items
}

// recentPathItems lists recent paths, best first, then the home folder's
// entries. Enter opens a recent folder as the project; Tab enters it. The
// rows are not filtered.
func (w *window) recentPathItems(home string, homeEntries []workspace.Entry, setText func(string)) []paletteItem {
	var items []paletteItem
	for _, entry := range recent.Ranked(w.recent, time.Now()) {
		path := entry.Path
		item := paletteItem{
			title: filepath.Base(path), detail: homeRelative(filepath.Dir(path)),
			icon: "text-x-generic-symbolic", complete: pathQueryText(path, entry.Dir), path: path,
			key: pathKey(path), boost: recentBoost,
			run: func() { w.openPath(path) },
		}
		if entry.Dir {
			item.icon = "folder-symbolic"
			item.run = func() { w.setProjectRoot(path) }
		}
		items = append(items, item)
	}
	if home != "" {
		for _, item := range w.folderEntryItems(home, homeEntries, false, pathQueryText, setText) {
			item.detail, item.key = "~", pathKey(item.path)
			items = append(items, item)
		}
	}
	return items
}

// pathKey is the match text of a path row: its name, then its path with "~"
// for home, like the "name relative/path" key of project files.
func pathKey(path string) []rune {
	return []rune(strings.ToLower(filepath.Base(path) + " " + homeRelative(path)))
}

// rememberPath records an opened file or folder for the path picker and
// saves the store in the background.
func (w *window) rememberPath(path string, dir bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	w.recent = recent.Add(w.recent, abs, dir, time.Now())
	w.saveRecent()
}

func (w *window) recentPath() string {
	if w.options.Paths.StateDir == "" {
		return ""
	}
	return filepath.Join(w.options.Paths.StateDir, "recent.json")
}

// loadRecent reads the store off the GTK thread. Paths recorded before the
// read finishes are kept.
func (w *window) loadRecent() {
	path := w.recentPath()
	if path == "" {
		return
	}
	go func() {
		entries, err := recent.Load(path)
		if err != nil {
			slog.Warn("load recent paths", "err", err)
		}
		dispatch.Main(func() {
			early := len(w.recent) > 0
			w.recent = recent.Merge(w.recent, entries)
			w.recentLoaded = true
			if early {
				w.saveRecent()
			}
		})
	}()
}

// saveRecent writes the store. Before the first read finishes it waits, so
// a write cannot replace the file with only this session's paths.
func (w *window) saveRecent() {
	path := w.recentPath()
	if path == "" || !w.recentLoaded {
		return
	}
	w.recentSeq++
	sequence, entries := w.recentSeq, w.recent
	go func() {
		w.recentMu.Lock()
		defer w.recentMu.Unlock()
		if sequence <= w.recentWritten {
			return
		}
		if err := recent.Save(path, entries); err != nil {
			slog.Warn("save recent paths", "err", err)
			return
		}
		w.recentWritten = sequence
	}()
}
