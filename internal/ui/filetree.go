package ui

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/jsnjack/e/internal/dispatch"
	"github.com/jsnjack/e/internal/editorconfig"
	"github.com/jsnjack/e/internal/logging"
)

// projectTree is the project sidebar. A GtkTreeListModel asks for a folder's
// children only when the folder expands, and the GtkListView builds widgets
// only for visible rows. Each folder's children live in one GtkStringList of
// absolute paths that is filled from a background directory read.
type projectTree struct {
	w        *window
	root     string
	closed   bool
	models   map[string]*gtk.StringList
	dirs     map[string]bool
	loads    map[string]int
	rows     map[uintptr]*projectRow
	tree     *gtk.TreeListModel
	list     *gtk.ListView
	status   *gtk.Label
	dirMenu  *gio.Menu
	fileMenu *gio.Menu
	popover  *gtk.PopoverMenu
	hidden   *gtk.Switch
	target   string
}

// projectRow is the widget set of one recycled list row.
type projectRow struct {
	expander *gtk.TreeExpander
	content  *gtk.Box
	icon     *gtk.Image
	label    *gtk.Label
	row      *gtk.TreeListRow
	notify   coreglib.SignalHandle
	path     string
	dir      bool
}

// setProjectRoot enables the project sidebar for a directory. Only the root
// level is read; subdirectories are read on expansion. Opening the current
// root again refreshes the tree in place.
func (w *window) setProjectRoot(path string) {
	root, err := filepath.Abs(path)
	if err != nil {
		w.toast("Couldn’t open project folder")
		return
	}
	if tree := w.projectTree; tree != nil && tree.root == root && w.projectRoot == root {
		tree.refresh()
	} else {
		if tree != nil {
			tree.closed = true
		}
		w.projectRoot = root
		logging.Trace("ui: open project", "root", root)
		w.projectTree = newProjectTree(w, root)
	}
	w.sidebarToggle.SetVisible(true)
	w.sidebar.SetVisible(true)
	w.content.SetPosition(max(w.sidebarWidth, 160))
	w.title.SetSubtitle(root)
	w.rememberPath(root, true)
	w.updateEmptyState()
}

func newProjectTree(w *window, root string) *projectTree {
	t := &projectTree{
		w:      w,
		root:   root,
		models: map[string]*gtk.StringList{},
		dirs:   map[string]bool{root: true},
		loads:  map[string]int{},
		rows:   map[uintptr]*projectRow{},
	}
	clearBox(w.sidebarBody)
	w.sidebarBody.AddCSSClass("project-tree")
	w.sidebarBody.Append(t.header())

	t.status = gtk.NewLabel("Loading…")
	t.status.SetXAlign(0)
	t.status.AddCSSClass("dim-label")
	t.status.SetMarginStart(8)
	w.sidebarBody.Append(t.status)

	t.tree = gtk.NewTreeListModel(t.model(root), false, false, t.createModel)
	factory := gtk.NewSignalListItemFactory()
	factory.ConnectSetup(t.setupRow)
	factory.ConnectBind(t.bindRow)
	factory.ConnectUnbind(t.unbindRow)
	factory.ConnectTeardown(t.teardownRow)
	t.list = gtk.NewListView(gtk.NewNoSelection(t.tree), &factory.ListItemFactory)
	t.list.AddCSSClass("navigation-sidebar")
	t.list.SetSingleClickActivate(true)
	t.list.ConnectActivate(t.activate)
	t.list.InsertActionGroup("tree", t.actions())

	t.dirMenu = gio.NewMenu()
	t.dirMenu.Append("New File", "tree.new-file")
	t.dirMenu.Append("New Folder", "tree.new-folder")
	t.dirMenu.Append("Rename…", "tree.rename")
	t.dirMenu.Append("Delete…", "tree.delete")
	t.fileMenu = gio.NewMenu()
	t.fileMenu.Append("Rename…", "tree.rename")
	t.fileMenu.Append("Delete…", "tree.delete")
	t.popover = gtk.NewPopoverMenuFromModel(t.dirMenu)
	t.popover.SetHasArrow(false)
	t.popover.SetHAlign(gtk.AlignStart)

	scroller := gtk.NewScrolledWindow()
	scroller.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroller.SetVExpand(true)
	scroller.SetChild(t.list)
	w.sidebarBody.Append(scroller)
	w.sidebarBody.Append(t.footer())

	t.load(root)
	return t
}

func (t *projectTree) header() *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 4)
	header := gtk.NewLabel(filepath.Base(t.root))
	header.SetXAlign(0)
	header.SetHExpand(true)
	header.AddCSSClass("heading")
	row.SetMarginStart(8)
	row.SetMarginEnd(4)
	row.SetMarginTop(4)
	row.SetMarginBottom(4)
	row.Append(header)
	add := func(icon, tooltip string, run func()) {
		button := gtk.NewButtonFromIconName(icon)
		button.SetTooltipText(tooltip)
		button.AddCSSClass("flat")
		button.ConnectClicked(run)
		row.Append(button)
	}
	add("document-new-symbolic", "New File", func() { t.w.createProjectEntry(t.root, false) })
	add("folder-new-symbolic", "New Folder", func() { t.w.createProjectEntry(t.root, true) })
	add("view-refresh-symbolic", "Refresh Explorer", t.refresh)
	return row
}

// footer holds the hidden-files switch. Toggling it rereads the expanded
// folders in place and saves the choice to the config file.
func (t *projectTree) footer() *gtk.Box {
	row := gtk.NewBox(gtk.OrientationHorizontal, 8)
	row.AddCSSClass("project-tree-footer")
	label := gtk.NewLabel("Show hidden files")
	label.SetXAlign(0)
	label.SetHExpand(true)
	label.AddCSSClass("dim-label")
	toggle := gtk.NewSwitch()
	t.hidden = toggle
	toggle.SetVAlign(gtk.AlignCenter)
	toggle.SetActive(t.w.options.Config.Editor.ShowHiddenFiles)
	toggle.SetTooltipText("List files and folders whose names start with a dot")
	toggle.NotifyProperty("active", func() {
		if toggle.Active() == t.w.options.Config.Editor.ShowHiddenFiles {
			return
		}
		cfg := t.w.options.Config
		cfg.Editor.ShowHiddenFiles = toggle.Active()
		t.w.applyConfig(cfg)
		t.refresh()
	})
	row.Append(label)
	row.Append(toggle)
	return row
}

func (t *projectTree) actions() *gio.SimpleActionGroup {
	group := gio.NewSimpleActionGroup()
	add := func(name string, run func(path string)) {
		action := gio.NewSimpleAction(name, nil)
		action.ConnectActivate(func(*glib.Variant) {
			if t.target != "" {
				run(t.target)
			}
		})
		group.AddAction(action)
	}
	add("new-file", func(path string) { t.w.createProjectEntry(path, false) })
	add("new-folder", func(path string) { t.w.createProjectEntry(path, true) })
	add("rename", t.w.renameProjectEntry)
	add("delete", t.w.deleteProjectEntry)
	return group
}

// model returns the cached child list of directory, creating an empty one.
// It does not read the directory.
func (t *projectTree) model(directory string) *gtk.StringList {
	list := t.models[directory]
	if list == nil {
		list = gtk.NewStringList(nil)
		t.models[directory] = list
	}
	return list
}

// createModel gives GtkTreeListModel the child list of a folder row. GTK
// also calls it to probe whether a row can expand, so it never starts a
// read; the row's expanded notification does.
func (t *projectTree) createModel(item *coreglib.Object) *gio.ListModel {
	object, ok := item.Cast().(*gtk.StringObject)
	if !ok || !t.dirs[object.String()] {
		return nil
	}
	return &t.model(object.String()).ListModel
}

// load reads directory in the background and merges the result into its
// cached list. Only the newest read of a directory is applied.
func (t *projectTree) load(directory string) {
	t.loads[directory]++
	sequence := t.loads[directory]
	showHidden := t.w.options.Config.Editor.ShowHiddenFiles
	go func() {
		entries, err := readProjectDir(directory, showHidden)
		dispatch.Main(func() {
			if t.closed || t.loads[directory] != sequence {
				return
			}
			if directory == t.root {
				t.status.SetVisible(err != nil)
				if err != nil {
					t.status.SetText("Folder unavailable")
					t.status.RemoveCSSClass("dim-label")
					t.status.AddCSSClass("error")
				}
			}
			if err != nil {
				slog.Debug("read project directory", "path", directory, "err", err)
				return
			}
			logging.Trace("ui: project directory ready", "path", directory, "entries", len(entries))
			t.apply(directory, entries)
		})
	}()
}

// apply merges entries into the cached list of directory. Unchanged entries
// keep their rows, so expanded folders below them stay expanded.
func (t *projectTree) apply(directory string, entries []projectEntry) {
	list := t.models[directory]
	if list == nil {
		return
	}
	count := list.NItems()
	old := make([]projectEntry, 0, count)
	for i := uint(0); i < count; i++ {
		path := list.String(i)
		old = append(old, projectEntry{path: path, dir: t.dirs[path]})
	}
	splices := diffProjectEntries(old, entries)
	if len(splices) == 0 {
		return
	}
	for _, entry := range entries {
		t.dirs[entry.path] = entry.dir
	}
	for _, splice := range splices {
		additions := make([]string, len(splice.additions))
		for i, entry := range splice.additions {
			additions[i] = entry.path
		}
		list.Splice(uint(splice.position), uint(splice.removals), additions)
	}
	kept := make(map[string]bool, len(entries))
	for _, entry := range entries {
		kept[entry.path] = true
	}
	for _, entry := range old {
		if !kept[entry.path] {
			t.forget(entry.path)
		}
	}
}

// forget drops cached state for path and everything below it.
func (t *projectTree) forget(path string) {
	prefix := path + string(filepath.Separator)
	for key := range t.dirs {
		if key == path || strings.HasPrefix(key, prefix) {
			delete(t.dirs, key)
		}
	}
	for key := range t.models {
		if key == path || strings.HasPrefix(key, prefix) {
			delete(t.models, key)
			delete(t.loads, key)
		}
	}
}

// reload rereads directory if the tree has listed it.
func (t *projectTree) reload(directory string) {
	if t.models[directory] != nil {
		t.load(directory)
	}
}

// refresh rereads the root and every expanded folder in place. Cached lists
// of collapsed folders are dropped; they are read again on expansion.
func (t *projectTree) refresh() {
	expanded := map[string]bool{t.root: true}
	for i := uint(0); i < t.tree.NItems(); i++ {
		row := t.tree.Row(i)
		if row == nil || !row.Expanded() {
			continue
		}
		if path, ok := projectRowPath(row); ok {
			expanded[path] = true
		}
	}
	for path := range t.models {
		if !expanded[path] {
			delete(t.models, path)
			delete(t.loads, path)
		}
	}
	for path := range expanded {
		t.model(path)
		t.load(path)
	}
}

func projectRowPath(row *gtk.TreeListRow) (string, bool) {
	item := row.Item()
	if item == nil {
		return "", false
	}
	object, ok := item.Cast().(*gtk.StringObject)
	if !ok {
		return "", false
	}
	return object.String(), true
}

func (t *projectTree) activate(position uint) {
	row := t.tree.Row(position)
	if row == nil {
		return
	}
	path, ok := projectRowPath(row)
	if !ok {
		return
	}
	if t.dirs[path] {
		row.SetExpanded(!row.Expanded())
		return
	}
	t.w.openPath(path)
}

func (t *projectTree) setupRow(object *coreglib.Object) {
	item, ok := object.Cast().(*gtk.ListItem)
	if !ok {
		return
	}
	r := &projectRow{expander: gtk.NewTreeExpander()}
	r.expander.SetIndentForIcon(true)
	r.expander.AddCSSClass("project-entry-row")
	r.content = gtk.NewBox(gtk.OrientationHorizontal, 7)
	r.icon = gtk.NewImage()
	r.label = gtk.NewLabel("")
	r.label.AddCSSClass("project-entry-label")
	r.label.SetXAlign(0)
	r.label.SetHExpand(true)
	r.label.SetEllipsize(pango.EllipsizeMiddle)
	more := gtk.NewButtonFromIconName("view-more-symbolic")
	more.AddCSSClass("flat")
	more.AddCSSClass("project-entry-menu")
	more.SetVAlign(gtk.AlignCenter)
	more.ConnectClicked(func() {
		if bounds, ok := more.ComputeBounds(r.expander); ok {
			t.showMenu(r, gdk.NewRectangle(int(bounds.X()), int(bounds.Y()), int(bounds.Width()), int(bounds.Height())))
		}
	})
	r.content.Append(r.icon)
	r.content.Append(r.label)
	r.content.Append(more)
	r.expander.SetChild(r.content)
	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_SECONDARY)
	click.ConnectPressed(func(_ int, x, y float64) {
		t.showMenu(r, gdk.NewRectangle(int(x), int(y), 1, 1))
	})
	r.expander.AddController(click)
	item.SetChild(r.expander)
	t.rows[item.Native()] = r
}

func (t *projectTree) bindRow(object *coreglib.Object) {
	item, ok := object.Cast().(*gtk.ListItem)
	if !ok {
		return
	}
	r := t.rows[item.Native()]
	if r == nil || item.Item() == nil {
		return
	}
	row, ok := item.Item().Cast().(*gtk.TreeListRow)
	if !ok {
		return
	}
	path, ok := projectRowPath(row)
	if !ok {
		return
	}
	r.row, r.path, r.dir = row, path, t.dirs[path]
	r.expander.SetListRow(row)
	r.label.SetText(filepath.Base(path))
	r.expander.SetTooltipText(path)
	if r.dir {
		r.icon.SetFromIconName("folder-symbolic")
		r.content.RemoveCSSClass("project-entry-open")
		r.content.AddCSSClass("project-folder")
	} else {
		r.icon.SetFromIconName("text-x-generic-symbolic")
		r.content.RemoveCSSClass("project-folder")
		r.content.AddCSSClass("project-entry-open")
	}
	r.notify = row.NotifyProperty("expanded", func() {
		if row.Expanded() {
			t.load(path)
		}
	})
}

func (t *projectTree) unbindRow(object *coreglib.Object) {
	item, ok := object.Cast().(*gtk.ListItem)
	if !ok {
		return
	}
	r := t.rows[item.Native()]
	if r == nil || r.row == nil {
		return
	}
	r.row.HandlerDisconnect(r.notify)
	r.expander.SetListRow(nil)
	r.row, r.path = nil, ""
}

func (t *projectTree) teardownRow(object *coreglib.Object) {
	item, ok := object.Cast().(*gtk.ListItem)
	if !ok {
		return
	}
	r := t.rows[item.Native()]
	delete(t.rows, item.Native())
	if r != nil && t.popover.Parent() != nil &&
		gtk.BaseWidget(t.popover.Parent()).Native() == r.expander.Native() {
		t.popover.Popdown()
		t.popover.Unparent()
	}
}

// showMenu opens the shared context menu for the row's entry. The one
// popover moves to the clicked row instead of each row owning a menu.
func (t *projectTree) showMenu(r *projectRow, at gdk.Rectangle) {
	if r.path == "" {
		return
	}
	t.target = r.path
	if r.dir {
		t.popover.SetMenuModel(t.dirMenu)
	} else {
		t.popover.SetMenuModel(t.fileMenu)
	}
	if parent := t.popover.Parent(); parent == nil || gtk.BaseWidget(parent).Native() != r.expander.Native() {
		if parent != nil {
			t.popover.Unparent()
		}
		t.popover.SetParent(r.expander)
	}
	t.popover.SetPointingTo(&at)
	t.popover.Popup()
}

// reloadProjectDir rereads directory after a file operation; other folders
// keep their rows and expansion.
func (w *window) reloadProjectDir(directory string) {
	if tree := w.projectTree; tree != nil && !tree.closed {
		tree.reload(directory)
	}
}

func clearBox(box *gtk.Box) {
	for child := box.FirstChild(); child != nil; child = box.FirstChild() {
		box.Remove(child)
	}
}

func (w *window) createProjectEntry(parent string, directory bool) {
	title, label := "New File", "Create File"
	if directory {
		title, label = "New Folder", "Create Folder"
	}
	w.promptProjectName(title, "", label, func(name string) {
		path := filepath.Join(parent, name)
		if !w.safeProjectTarget(path) {
			w.toast("Choose a name inside the project")
			return
		}
		go func() {
			var err error
			if directory {
				err = os.Mkdir(path, 0o755)
			} else {
				var file *os.File
				file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
				if err == nil {
					err = file.Close()
				}
			}
			dispatch.Main(func() {
				if err != nil {
					w.toast("Couldn’t create " + name)
					return
				}
				w.reloadProjectDir(parent)
				if !directory {
					w.openPath(path)
				}
			})
		}()
	})
}

func (w *window) renameProjectEntry(path string) {
	w.promptProjectName("Rename", filepath.Base(path), "Rename", func(name string) {
		replacement := filepath.Join(filepath.Dir(path), name)
		if !w.safeProjectTarget(path) || !w.safeProjectTarget(replacement) {
			w.toast("Choose a name inside the project")
			return
		}
		go func() {
			err := renameProjectPath(path, replacement)
			dispatch.Main(func() {
				if err != nil {
					w.toast("Couldn’t rename " + filepath.Base(path))
					return
				}
				prefix := path + string(filepath.Separator)
				for _, doc := range w.documents {
					if doc.path == path {
						doc.changePath(replacement)
					} else if strings.HasPrefix(doc.path, prefix) {
						doc.changePath(filepath.Join(replacement, strings.TrimPrefix(doc.path, prefix)))
					}
				}
				w.reloadProjectDir(filepath.Dir(path))
			})
		}()
	})
}

func renameProjectPath(path, replacement string) error {
	if path == replacement {
		return nil
	}
	if _, err := os.Lstat(replacement); err == nil {
		return os.ErrExist
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(path, replacement)
}

func (w *window) deleteProjectEntry(path string) {
	if !w.safeProjectTarget(path) {
		return
	}
	dialog := adw.NewAlertDialog("Delete “"+filepath.Base(path)+"”?", "This permanently removes it from the project.")
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("delete", "Delete")
	dialog.SetCloseResponse("cancel")
	dialog.SetResponseAppearance("delete", adw.ResponseDestructive)
	dialog.ConnectResponse(func(response string) {
		if response != "delete" {
			return
		}
		go func() {
			err := os.RemoveAll(path)
			dispatch.Main(func() {
				if err != nil {
					w.toast("Couldn’t delete " + filepath.Base(path))
					return
				}
				prefix := path + string(filepath.Separator)
				for _, doc := range w.documents {
					if doc.path == path || strings.HasPrefix(doc.path, prefix) {
						doc.stopLSP()
						doc.path = ""
						doc.modTime = 0
						doc.props = editorconfig.Properties{}
						doc.buffer.SetModified(true)
						doc.updatePresentation()
						doc.refreshDiffBase()
					}
				}
				w.reloadProjectDir(filepath.Dir(path))
			})
		}()
	})
	dialog.Present(w.win)
}

func (w *window) promptProjectName(title, initial, confirm string, done func(string)) {
	dialog := adw.NewAlertDialog(title, "Enter a single file or folder name.")
	entry := gtk.NewEntry()
	entry.SetText(initial)
	entry.SetActivatesDefault(true)
	dialog.SetExtraChild(entry)
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("confirm", confirm)
	dialog.SetCloseResponse("cancel")
	dialog.SetDefaultResponse("confirm")
	dialog.SetResponseAppearance("confirm", adw.ResponseSuggested)
	dialog.SetResponseEnabled("confirm", validProjectName(initial))
	entry.Connect("changed", func() {
		dialog.SetResponseEnabled("confirm", validProjectName(entry.Text()))
	})
	dialog.ConnectResponse(func(response string) {
		if response != "confirm" {
			return
		}
		name := strings.TrimSpace(entry.Text())
		done(name)
	})
	dialog.Present(w.win)
	entry.GrabFocus()
	entry.SelectRegion(0, -1)
}

func validProjectName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\\`)
}

func (w *window) safeProjectTarget(path string) bool {
	root, err := filepath.Abs(w.projectRoot)
	if err != nil || root == "" {
		return false
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, filepath.Join(parent, filepath.Base(target)))
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
