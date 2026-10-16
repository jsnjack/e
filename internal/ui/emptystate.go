package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// emptyState is shown in place of the tabs when no document is open: after
// `e <folder>` and after the last tab closes. Its buttons run window actions,
// so they follow the same code paths as the shortcuts they show.
type emptyState struct {
	root     *gtk.Box
	title    *gtk.Label
	subtitle *gtk.Label
}

func newEmptyState() *emptyState {
	e := &emptyState{root: gtk.NewBox(gtk.OrientationVertical, 0)}
	e.root.AddCSSClass("empty-state")
	e.root.SetHExpand(true)
	e.root.SetVExpand(true)

	column := gtk.NewBox(gtk.OrientationVertical, 0)
	column.SetVAlign(gtk.AlignCenter)
	column.SetVExpand(true)
	column.SetMarginStart(24)
	column.SetMarginEnd(24)
	column.SetMarginTop(32)
	column.SetMarginBottom(32)

	hero := gtk.NewBox(gtk.OrientationVertical, 0)
	hero.AddCSSClass("empty-hero")
	hero.SetHAlign(gtk.AlignCenter)
	hero.Append(heroMark())
	column.Append(hero)

	e.title = gtk.NewLabel("E")
	e.title.AddCSSClass("empty-title")
	e.title.SetEllipsize(pango.EllipsizeEnd)
	e.title.SetMarginTop(22)
	column.Append(e.title)

	e.subtitle = gtk.NewLabel("A fast, focused editor")
	e.subtitle.AddCSSClass("empty-subtitle")
	e.subtitle.SetEllipsize(pango.EllipsizeStart)
	e.subtitle.SetMarginTop(4)
	column.Append(e.subtitle)

	actions := gtk.NewBox(gtk.OrientationVertical, 2)
	actions.AddCSSClass("empty-actions")
	actions.SetMarginTop(28)
	emptyAction(actions, "system-search-symbolic", "Go to File or Folder", "win.quick-open", "Ctrl+P")
	emptyAction(actions, "document-new-symbolic", "New File", "win.new", "Ctrl+N")
	emptyAction(actions, "folder-open-symbolic", "Open Folder…", "win.open-folder", "Ctrl+K Ctrl+O")
	emptyAction(actions, "utilities-terminal-symbolic", "Command Palette", "win.command-palette", "Ctrl+Shift+P")
	column.Append(actions)

	tip := gtk.NewLabel("Ctrl+K Ctrl+S lists every shortcut")
	tip.AddCSSClass("empty-tip")
	tip.SetMarginTop(18)
	column.Append(tip)

	clamp := adw.NewClamp()
	clamp.SetMaximumSize(420)
	clamp.SetTighteningThreshold(320)
	clamp.SetChild(column)
	e.root.Append(clamp)
	return e
}

// heroMark is the application icon when the theme has it, else a drawn
// monogram, so a build run from the source tree still looks finished.
func heroMark() gtk.Widgetter {
	if display := gdk.DisplayGetDefault(); display != nil && gtk.IconThemeGetForDisplay(display).HasIcon(appID) {
		icon := gtk.NewImageFromIconName(appID)
		icon.SetPixelSize(96)
		return icon
	}
	mark := gtk.NewLabel("e")
	mark.AddCSSClass("empty-monogram")
	return mark
}

// emptyAction appends one row: icon, label, and the shortcut as keycaps.
// Chord steps are separated by spaces, keys within a step by "+".
func emptyAction(parent *gtk.Box, icon, label, action, shortcut string) *gtk.Button {
	row := gtk.NewBox(gtk.OrientationHorizontal, 12)
	image := gtk.NewImageFromIconName(icon)
	row.Append(image)
	text := gtk.NewLabel(label)
	text.SetXAlign(0)
	text.SetHExpand(true)
	row.Append(text)
	keys := gtk.NewBox(gtk.OrientationHorizontal, 3)
	keys.SetVAlign(gtk.AlignCenter)
	for i, step := range strings.Fields(shortcut) {
		if i > 0 {
			gap := gtk.NewLabel("then")
			gap.AddCSSClass("empty-keygap")
			keys.Append(gap)
		}
		for _, key := range strings.Split(step, "+") {
			cap := gtk.NewLabel(key)
			cap.AddCSSClass("empty-keycap")
			keys.Append(cap)
		}
	}
	row.Append(keys)

	button := gtk.NewButton()
	button.AddCSSClass("flat")
	button.AddCSSClass("empty-action")
	button.SetChild(row)
	button.SetActionName(action)
	parent.Append(button)
	return button
}

// update names the open project, or the editor when there is none.
func (e *emptyState) update(projectRoot string) {
	if projectRoot == "" {
		e.title.SetText("E")
		e.subtitle.SetText("Open a folder or a file to start")
		return
	}
	e.title.SetText(filepath.Base(projectRoot))
	e.subtitle.SetText(homeRelative(projectRoot))
}

// homeRelative shows paths under the home directory with a leading "~".
func homeRelative(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

// updateEmptyState swaps between the tabs and the empty state. With no tab
// open, typing must not start a search in a document that is not there.
func (w *window) updateEmptyState() {
	if w.tabs.NPages() > 0 {
		w.mainStack.SetVisibleChildName("tabs")
		w.searchBar.SetKeyCaptureWidget(&w.win.Widget)
		return
	}
	w.empty.update(w.projectRoot)
	w.updateTitle(nil)
	w.searchBar.SetSearchMode(false)
	w.searchBar.SetKeyCaptureWidget(nil)
	w.mainStack.SetVisibleChildName("empty")
}
