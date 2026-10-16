package ui

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/jsnjack/e/internal/dispatch"
	"github.com/jsnjack/e/internal/recent"
	"github.com/jsnjack/e/internal/workspace"
)

// maxPathPickerEntries caps one directory listing in the path picker.
const maxPathPickerEntries = 5000

// showPathPicker opens any file or folder on disk. Text that starts with "/"
// or "~/" browses one folder at a time and fuzzy-matches the last segment.
// Other text fuzzy-matches recent paths and the home folder. Tab completes
// the selected row, Enter on a folder row enters it, and Backspace after a
// "/" goes up one folder.
func (w *window) showPathPicker() {
	p := w.newPalette("Open Path", "Recent paths, or type / or ~/ to browse")
	p.entry.SetSearchDelay(0)
	home, _ := os.UserHomeDir()
	listings := map[string][]workspace.Entry{}
	loading := map[string]bool{}

	setText := func(text string) {
		p.entry.SetText(text)
		p.entry.SetPosition(-1)
	}
	var refresh func()
	// listing returns dir's cached entries, or starts a background read and
	// reports false.
	listing := func(dir string) ([]workspace.Entry, bool) {
		if entries, ok := listings[dir]; ok {
			return entries, true
		}
		if !loading[dir] {
			loading[dir] = true
			go func() {
				entries, err := workspace.ListDir(dir, maxPathPickerEntries)
				if err != nil {
					slog.Debug("path picker: list folder", "path", dir, "err", err)
				}
				dispatch.Main(func() {
					delete(loading, dir)
					listings[dir] = entries
					if !p.closed {
						refresh()
					}
				})
			}()
		}
		return nil, false
	}
	refresh = func() {
		text := p.entry.Text()
		if dir, filter, ok := parsePathQuery(text, home); ok {
			if entries, ok := listing(dir); ok {
				p.show(w.folderPathItems(dir, filter, entries, setText))
			}
			return
		}
		var entries []workspace.Entry
		if home != "" {
			entries, _ = listing(home)
		}
		p.show(w.recentPathItems(home, text, entries, setText))
	}
	p.key = func(keyval uint, state gdk.ModifierType) bool {
		switch keyval {
		case gdk.KEY_Tab:
			if item, ok := p.selected(); ok && item.complete != "" {
				setText(item.complete)
			}
			return true
		case gdk.KEY_BackSpace:
			if state&(gdk.ControlMask|gdk.AltMask) != 0 {
				return false
			}
			if parent, ok := parentPathQuery(p.entry.Text(), home); ok {
				setText(parent)
				return true
			}
		}
		return false
	}
	p.entry.ConnectSearchChanged(refresh)
	refresh()
	p.present()
}

// parsePathQuery splits browsing text into an absolute folder and the filter
// typed after its last "/". It reports false for text that searches recent
// paths instead.
func parsePathQuery(text, home string) (dir, filter string, ok bool) {
	switch {
	case strings.HasPrefix(text, "/"):
	case home != "" && (text == "~" || strings.HasPrefix(text, "~/")):
		text = home + strings.TrimPrefix(text, "~")
		if !strings.HasSuffix(text, "/") && filepath.Clean(text) == filepath.Clean(home) {
			text += "/"
		}
	default:
		return "", "", false
	}
	cut := strings.LastIndex(text, "/")
	return filepath.Clean(text[:cut+1]), text[cut+1:], true
}

// parentPathQuery is the text one folder up from text that ends in "/".
func parentPathQuery(text, home string) (string, bool) {
	if !strings.HasSuffix(text, "/") || text == "/" {
		return "", false
	}
	dir, _, ok := parsePathQuery(text, home)
	if !ok {
		return "", false
	}
	return pathQueryText(filepath.Dir(dir), true), true
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

// folderPathItems lists dir for the filter. Dot entries show only when the
// filter starts with ".", as in a shell. An empty filter puts "open this
// folder" first.
func (w *window) folderPathItems(dir, filter string, entries []workspace.Entry, setText func(string)) []paletteItem {
	items := w.folderEntryItems(dir, entries, strings.HasPrefix(filter, "."), setText)
	items = filterPaletteItems(items, filter, maxPaletteResults)
	if filter == "" {
		open := paletteItem{
			title: "Open “" + filepath.Base(dir) + "” as Project", detail: homeRelative(dir),
			icon: "folder-open-symbolic", complete: pathQueryText(dir, true),
			run: func() { w.setProjectRoot(dir) },
		}
		items = append([]paletteItem{open}, items...)
	}
	return items
}

// folderEntryItems turns a listing into rows. A folder row enters the folder;
// a file row opens the file. Rows match on the entry name only.
func (w *window) folderEntryItems(dir string, entries []workspace.Entry, showHidden bool, setText func(string)) []paletteItem {
	items := make([]paletteItem, 0, len(entries))
	for _, entry := range entries {
		if !showHidden && strings.HasPrefix(entry.Name, ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name)
		item := paletteItem{title: entry.Name, key: []rune(strings.ToLower(entry.Name)), complete: pathQueryText(path, entry.Dir)}
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

// recentPathItems fuzzy-matches recent paths first, then the home folder.
// Enter opens a recent folder as the project; Tab enters it.
func (w *window) recentPathItems(home, query string, homeEntries []workspace.Entry, setText func(string)) []paletteItem {
	seen := map[string]bool{}
	var recents []paletteItem
	for _, entry := range recent.Ranked(w.recent, time.Now()) {
		path := entry.Path
		seen[path] = true
		item := paletteItem{
			title: filepath.Base(path), detail: homeRelative(filepath.Dir(path)),
			icon: "text-x-generic-symbolic", complete: pathQueryText(path, entry.Dir),
			run: func() { w.openPath(path) },
		}
		if entry.Dir {
			item.icon = "folder-symbolic"
			item.run = func() { w.setProjectRoot(path) }
		}
		recents = append(recents, item)
	}
	var others []paletteItem
	if home != "" {
		for _, item := range w.folderEntryItems(home, homeEntries, strings.HasPrefix(query, "."), setText) {
			if seen[filepath.Join(home, strings.TrimSuffix(item.title, "/"))] {
				continue
			}
			item.detail, item.key = "~", nil
			others = append(others, item)
		}
	}
	items := filterPaletteItems(withSearchKeys(recents), query, maxPaletteResults)
	return append(items, filterPaletteItems(withSearchKeys(others), query, maxPaletteResults-len(items))...)
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
