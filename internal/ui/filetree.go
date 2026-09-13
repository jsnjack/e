package ui

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/dispatch"
	"github.com/jsnjack/e/internal/logging"
)

// setProjectRoot enables the project sidebar for a directory passed on the
// command line. Only the visible directory level is read; subdirectories are
// scanned on first expansion.
func (w *window) setProjectRoot(path string) {
	root, err := filepath.Abs(path)
	if err != nil {
		w.toast("Couldn’t open project folder")
		return
	}
	w.projectRoot = root
	logging.Trace("ui: open project", "root", root)
	clearBox(w.sidebarBody)
	w.sidebarBody.AddCSSClass("project-tree")
	headerRow := gtk.NewBox(gtk.OrientationHorizontal, 4)
	header := gtk.NewLabel(filepath.Base(root))
	header.SetXAlign(0)
	header.SetHExpand(true)
	header.AddCSSClass("heading")
	headerRow.SetMarginStart(8)
	headerRow.SetMarginEnd(4)
	headerRow.SetMarginTop(4)
	headerRow.SetMarginBottom(4)
	headerRow.Append(header)
	newFile := gtk.NewButtonFromIconName("document-new-symbolic")
	newFile.SetTooltipText("New File")
	newFile.AddCSSClass("flat")
	newFile.ConnectClicked(func() { w.createProjectEntry(root, false) })
	newFolder := gtk.NewButtonFromIconName("folder-new-symbolic")
	newFolder.SetTooltipText("New Folder")
	newFolder.AddCSSClass("flat")
	newFolder.ConnectClicked(func() { w.createProjectEntry(root, true) })
	refresh := gtk.NewButtonFromIconName("view-refresh-symbolic")
	refresh.SetTooltipText("Refresh Explorer")
	refresh.AddCSSClass("flat")
	refresh.ConnectClicked(func() { w.setProjectRoot(root) })
	headerRow.Append(newFile)
	headerRow.Append(newFolder)
	headerRow.Append(refresh)
	w.sidebarBody.Append(headerRow)
	items := gtk.NewBox(gtk.OrientationVertical, 0)
	w.sidebarBody.Append(items)
	w.loadDirectory(root, root, items)
	w.sidebarToggle.SetVisible(true)
	w.sidebar.SetVisible(true)
	w.content.SetPosition(max(w.sidebarWidth, 160))
	w.title.SetSubtitle(root)
}

func (w *window) loadDirectory(root, directory string, target *gtk.Box) {
	loading := gtk.NewLabel("Loading…")
	loading.SetXAlign(0)
	loading.AddCSSClass("dim-label")
	loading.SetMarginStart(8)
	target.Append(loading)
	go func() {
		entries, err := os.ReadDir(directory)
		dispatch.Main(func() {
			if w.projectRoot != root {
				return
			}
			clearBox(target)
			if err != nil {
				label := gtk.NewLabel("Folder unavailable")
				label.SetXAlign(0)
				label.AddCSSClass("error")
				target.Append(label)
				slog.Debug("read project directory", "path", directory, "err", err)
				return
			}
			logging.Trace("ui: project directory ready", "path", directory, "entries", len(entries))
			sort.SliceStable(entries, func(i, j int) bool {
				if entries[i].IsDir() != entries[j].IsDir() {
					return entries[i].IsDir()
				}
				return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
			})
			for _, entry := range entries {
				if entry.Name() == ".git" || entry.Name() == ".hg" || entry.Name() == ".svn" {
					continue
				}
				path := filepath.Join(directory, entry.Name())
				if entry.IsDir() {
					w.appendDirectory(root, path, entry.Name(), target)
				} else {
					w.appendFile(path, entry.Name(), target)
				}
			}
		})
	}()
}

func (w *window) appendDirectory(root, path, name string, target *gtk.Box) {
	container := gtk.NewOverlay()
	container.AddCSSClass("project-entry-row")
	expander := gtk.NewExpander("")
	expander.SetHExpand(true)
	labelRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	labelRow.AddCSSClass("project-folder")
	labelRow.SetMarginEnd(32)
	icon := gtk.NewImageFromIconName("folder-symbolic")
	label := gtk.NewLabel(name)
	label.AddCSSClass("project-entry-label")
	label.SetXAlign(0)
	label.SetHExpand(true)
	labelRow.Append(icon)
	labelRow.Append(label)
	expander.SetLabelWidget(labelRow)
	children := gtk.NewBox(gtk.OrientationVertical, 0)
	children.SetMarginStart(14)
	expander.SetChild(children)
	loaded := false
	expander.ConnectActivate(func() {
		if loaded {
			return
		}
		loaded = true
		w.loadDirectory(root, path, children)
	})
	menu := w.projectEntryMenu(path, true)
	menu.SetHAlign(gtk.AlignEnd)
	menu.SetVAlign(gtk.AlignStart)
	menu.SetMarginTop(2)
	menu.SetMarginEnd(2)
	container.SetChild(expander)
	container.AddOverlay(menu)
	target.Append(container)
}

func (w *window) appendFile(path, name string, target *gtk.Box) {
	container := gtk.NewBox(gtk.OrientationHorizontal, 2)
	container.AddCSSClass("project-entry-row")
	button := gtk.NewButton()
	button.AddCSSClass("flat")
	button.AddCSSClass("project-entry-open")
	button.SetHAlign(gtk.AlignFill)
	button.SetHExpand(true)
	content := gtk.NewBox(gtk.OrientationHorizontal, 7)
	icon := gtk.NewImageFromIconName("text-x-generic-symbolic")
	label := gtk.NewLabel(name)
	label.AddCSSClass("project-entry-label")
	label.SetXAlign(0)
	label.SetHExpand(true)
	content.Append(icon)
	content.Append(label)
	button.SetChild(content)
	button.SetTooltipText(path)
	button.ConnectClicked(func() { w.openPath(path) })
	container.Append(button)
	container.Append(w.projectEntryMenu(path, false))
	target.Append(container)
}

func clearBox(box *gtk.Box) {
	for child := box.FirstChild(); child != nil; child = box.FirstChild() {
		box.Remove(child)
	}
}

func (w *window) projectEntryMenu(path string, directory bool) *gtk.MenuButton {
	menu := gtk.NewMenuButton()
	menu.SetIconName("view-more-symbolic")
	menu.SetTooltipText("More actions for " + filepath.Base(path))
	menu.AddCSSClass("flat")
	menu.AddCSSClass("project-entry-menu")
	popover := gtk.NewPopover()
	actions := gtk.NewBox(gtk.OrientationVertical, 2)
	actions.SetMarginTop(6)
	actions.SetMarginBottom(6)
	actions.SetMarginStart(6)
	actions.SetMarginEnd(6)
	add := func(label string, run func()) {
		button := gtk.NewButtonWithLabel(label)
		button.AddCSSClass("flat")
		button.SetHAlign(gtk.AlignFill)
		button.ConnectClicked(func() {
			popover.Popdown()
			run()
		})
		actions.Append(button)
	}
	if directory {
		add("New File", func() { w.createProjectEntry(path, false) })
		add("New Folder", func() { w.createProjectEntry(path, true) })
	}
	add("Rename…", func() { w.renameProjectEntry(path) })
	add("Delete…", func() { w.deleteProjectEntry(path) })
	popover.SetChild(actions)
	menu.SetPopover(popover)
	return menu
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
				w.setProjectRoot(w.projectRoot)
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
						doc.path = replacement
						doc.updatePresentation()
					} else if strings.HasPrefix(doc.path, prefix) {
						doc.path = filepath.Join(replacement, strings.TrimPrefix(doc.path, prefix))
						doc.updatePresentation()
					}
				}
				w.setProjectRoot(w.projectRoot)
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
				w.setProjectRoot(w.projectRoot)
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
	dialog.ConnectResponse(func(response string) {
		if response != "confirm" {
			return
		}
		name := strings.TrimSpace(entry.Text())
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
			w.toast("Enter a valid name without slashes")
			return
		}
		done(name)
	})
	dialog.Present(w.win)
	entry.GrabFocus()
	entry.SelectRegion(0, -1)
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
