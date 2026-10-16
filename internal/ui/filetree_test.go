package ui

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/ai"
	"github.com/jsnjack/e/internal/config"
	"github.com/jsnjack/e/internal/gtktest"
	"github.com/jsnjack/e/internal/recent"
)

func TestSafeProjectTargetRejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	w := window{projectRoot: root}
	if !w.safeProjectTarget(filepath.Join(root, "new.txt")) {
		t.Fatal("ordinary project child was rejected")
	}
	if w.safeProjectTarget(filepath.Join(root, "..", "outside.txt")) {
		t.Fatal("lexical project escape was accepted")
	}
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if w.safeProjectTarget(filepath.Join(link, "escaped.txt")) {
		t.Fatal("symlink project escape was accepted")
	}
}

func TestRenameProjectPathDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	destination := filepath.Join(root, "destination.txt")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("destination"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameProjectPath(source, destination); !os.IsExist(err) {
		t.Fatalf("rename error = %v, want file exists", err)
	}
	for path, want := range map[string]string{source: "source", destination: "destination"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q, %v", path, data, err)
		}
	}
	renamed := filepath.Join(root, "renamed.txt")
	if err := renameProjectPath(source, renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(renamed); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRootFor(t *testing.T) {
	repos := map[string]bool{"/home/user/repo/.git": true}
	exists := func(path string) bool { return repos[path] }
	tests := []struct{ file, want string }{
		{"/home/user/repo/cmd/main.go", "/home/user/repo"},
		{"/home/user/repo/main.go", "/home/user/repo"},
		{"/home/user/notes/todo.md", "/home/user/notes"},
		{"/todo.md", "/"},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			if got := projectRootFor(test.file, exists); got != test.want {
				t.Fatalf("projectRootFor(%q) = %q, want %q", test.file, got, test.want)
			}
		})
	}
}

func TestReadProjectDirSortsAndSkipsMetadata(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".git", "beta", "Alpha"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"b.txt", "A.txt", ".hidden"} {
		if err := os.WriteFile(filepath.Join(root, file), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := readProjectDir(root, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []projectEntry{
		{path: filepath.Join(root, "Alpha"), dir: true},
		{path: filepath.Join(root, "beta"), dir: true},
		{path: filepath.Join(root, ".hidden")},
		{path: filepath.Join(root, "A.txt")},
		{path: filepath.Join(root, "b.txt")},
	}
	if !equalProjectEntries(entries, want) {
		t.Fatalf("readProjectDir = %v, want %v", entries, want)
	}

	entries, err = readProjectDir(root, false)
	if err != nil {
		t.Fatal(err)
	}
	want = []projectEntry{want[0], want[1], want[3], want[4]}
	if !equalProjectEntries(entries, want) {
		t.Fatalf("readProjectDir without hidden = %v, want %v", entries, want)
	}
}

func TestDiffProjectEntries(t *testing.T) {
	file := func(name string) projectEntry { return projectEntry{path: "/p/" + name} }
	dir := func(name string) projectEntry { return projectEntry{path: "/p/" + name, dir: true} }
	tests := []struct {
		name      string
		old, next []projectEntry
		splices   int
	}{
		{"unchanged", []projectEntry{dir("a"), file("b")}, []projectEntry{dir("a"), file("b")}, 0},
		{"first load", nil, []projectEntry{dir("a"), file("b"), file("c")}, 1},
		{"insert middle", []projectEntry{dir("a"), file("c")}, []projectEntry{dir("a"), file("b"), file("c")}, 1},
		{"delete", []projectEntry{dir("a"), file("b"), file("c")}, []projectEntry{dir("a"), file("c")}, 1},
		{"rename", []projectEntry{dir("a"), file("b"), file("c")}, []projectEntry{dir("a"), file("c"), file("d")}, 2},
		{"type change", []projectEntry{file("a")}, []projectEntry{dir("a")}, 1},
		{"moved", []projectEntry{file("a"), file("b"), file("c")}, []projectEntry{file("c"), file("a"), file("b")}, 2},
		{"clear", []projectEntry{dir("a"), file("b")}, nil, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			splices := diffProjectEntries(test.old, test.next)
			if got := applyProjectSplices(test.old, splices); !equalProjectEntries(got, test.next) {
				t.Fatalf("applied diff = %v, want %v", got, test.next)
			}
			if len(splices) != test.splices {
				t.Fatalf("splices = %d (%v), want %d", len(splices), splices, test.splices)
			}
			for _, splice := range splices {
				for _, kept := range test.old {
					if containsProjectEntry(test.next, kept) && containsProjectEntry(splice.additions, kept) && test.name != "moved" {
						t.Fatalf("kept entry %v was removed and added again", kept)
					}
				}
			}
		})
	}
}

func applyProjectSplices(entries []projectEntry, splices []projectSplice) []projectEntry {
	result := append([]projectEntry(nil), entries...)
	for _, splice := range splices {
		tail := append([]projectEntry(nil), result[splice.position+splice.removals:]...)
		result = append(append(result[:splice.position], splice.additions...), tail...)
	}
	return result
}

func equalProjectEntries(a, b []projectEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsProjectEntry(entries []projectEntry, entry projectEntry) bool {
	for _, candidate := range entries {
		if candidate == entry {
			return true
		}
	}
	return false
}

// TestProjectTreeRuntime checks in a real GTK session that reloads keep
// folders expanded and that a large folder expands without blocking.
func TestProjectTreeRuntime(t *testing.T) {
	if os.Getenv("E_UI_RUNTIME_TEST") != "1" {
		t.Skip("set E_UI_RUNTIME_TEST=1 on a graphical test display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	gtk.Init()
	app := adw.NewApplication("com.jsnjack.e.TreeRuntimeTest", gio.ApplicationNonUnique)
	if err := app.Register(context.Background()); err != nil {
		t.Fatalf("register test application: %v", err)
	}
	base := t.TempDir()
	cfg := config.Default()
	cfg.AI.Enabled = false
	cfg.Editor.SpellCheck = false
	w := newWindow(app, Options{
		Config: cfg, ConfigPath: filepath.Join(base, "config", "config.toml"),
		Paths:     config.Paths{StateDir: filepath.Join(base, "state"), CacheDir: filepath.Join(base, "cache")},
		Completer: ai.NewCompleter([]ai.Provider{runtimeProvider{}}),
	})
	defer func() {
		w.stop()
		w.win.Destroy()
	}()
	w.present()
	waitForUI(t, func() bool { return w.win.Width() > 0 && w.win.Height() > 0 })

	root := filepath.Join(base, "project")
	sub := filepath.Join(root, "sub")
	big := filepath.Join(root, "big")
	for _, dir := range []string{sub, big} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sub, "inner.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := range 5000 {
		if err := os.WriteFile(filepath.Join(big, fmt.Sprintf("file-%04d.txt", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.setProjectRoot(root)
	tree := w.projectTree
	waitForUI(t, func() bool { return tree.tree.NItems() == 2 })

	rowFor := func(path string) *gtk.TreeListRow {
		for i := uint(0); i < tree.tree.NItems(); i++ {
			row := tree.tree.Row(i)
			if got, ok := projectRowPath(row); ok && got == path {
				return row
			}
		}
		return nil
	}
	rowFor(sub).SetExpanded(true)
	waitForUI(t, func() bool { return rowFor(filepath.Join(sub, "inner.txt")) != nil })

	added := filepath.Join(root, "added.txt")
	if err := os.WriteFile(added, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w.reloadProjectDir(root)
	waitForUI(t, func() bool { return rowFor(added) != nil })
	if row := rowFor(sub); row == nil || !row.Expanded() {
		t.Fatal("reloading the root collapsed an expanded folder")
	}

	if err := os.WriteFile(filepath.Join(sub, "second.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	tree.refresh()
	waitForUI(t, func() bool { return rowFor(filepath.Join(sub, "second.txt")) != nil && rowFor(added) == nil })
	if row := rowFor(sub); row == nil || !row.Expanded() {
		t.Fatal("refresh collapsed an expanded folder")
	}

	before := tree.tree.NItems()
	started := time.Now()
	rowFor(big).SetExpanded(true)
	waitForUI(t, func() bool { return tree.tree.NItems() == before+5000 })
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("expanding 5000 entries took %v", elapsed)
	}
	t.Logf("expanded 5000 entries in %v", time.Since(started))

	waitForUI(t, func() bool {
		for _, row := range tree.rows {
			if row.path != "" {
				return true
			}
		}
		return false
	})
	for _, row := range tree.rows {
		if row.path != "" {
			tree.showMenu(row, gdk.NewRectangle(0, 0, 1, 1))
			break
		}
	}
	waitForUI(t, func() bool { return tree.popover.Visible() })
	tree.popover.Popdown()

	w.setProjectRoot(root)
	if w.projectTree != tree {
		t.Fatal("opening the same root rebuilt the tree")
	}

	if w.mainStack.VisibleChildName() != "empty" || w.empty.title.Text() != "project" || !w.empty.quickOpen.Visible() {
		t.Fatalf("empty state = %q titled %q", w.mainStack.VisibleChildName(), w.empty.title.Text())
	}
	saveScreenshot(t, w.toastOverlay, "empty-state.png")

	hidden := filepath.Join(root, ".hidden")
	if err := os.WriteFile(hidden, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tree.refresh()
	waitForUI(t, func() bool { return rowFor(added) == nil && rowFor(filepath.Join(sub, "second.txt")) != nil })
	if rowFor(hidden) != nil {
		t.Fatal("hidden file listed with the switch off")
	}
	tree.hidden.SetActive(true)
	waitForUI(t, func() bool { return rowFor(hidden) != nil })
	if row := rowFor(sub); row == nil || !row.Expanded() {
		t.Fatal("showing hidden files collapsed an expanded folder")
	}
	waitForUI(t, func() bool {
		saved, err := config.Load(w.options.ConfigPath)
		return err == nil && saved.Editor.ShowHiddenFiles
	})
	tree.hidden.SetActive(false)
	waitForUI(t, func() bool { return rowFor(hidden) == nil })

	doc := w.newDocument(nil)
	if w.mainStack.VisibleChildName() != "tabs" {
		t.Fatal("opening a document kept the empty state")
	}
	w.tabs.ClosePage(doc.page)
	waitForUI(t, func() bool { return w.mainStack.VisibleChildName() == "empty" && len(w.documents) == 0 })

	checkPathPicker(t, w, root, sub)
}

// checkPathPicker browses into a folder, completes a file with Tab, opens
// it, and finds the folder again among recent paths.
func checkPathPicker(t *testing.T, w *window, root, sub string) {
	t.Helper()
	waitForUI(t, func() bool { return w.recentLoaded })
	w.showPathPicker()
	p := w.lastPalette
	p.entry.SetText(sub + "/inn")
	waitForUI(t, func() bool {
		item, ok := p.selected()
		return ok && item.title == "inner.txt"
	})
	p.key(gdk.KEY_Tab, 0)
	if got := p.entry.Text(); got != pathQueryText(filepath.Join(sub, "inner.txt"), false) {
		t.Fatalf("Tab completed %q", got)
	}
	p.entry.SetText(root + "/")
	waitForUI(t, func() bool { item, ok := p.selected(); return ok && item.icon == "folder-open-symbolic" })
	saveScreenshot(t, p.dialog, "path-picker.png")
	p.entry.SetText(sub + "/")
	if p.key(gdk.KEY_BackSpace, 0); p.entry.Text() != pathQueryText(root, true) {
		t.Fatalf("Backspace went to %q", p.entry.Text())
	}
	p.entry.SetText(sub + "/inn")
	waitForUI(t, func() bool { item, ok := p.selected(); return ok && item.title == "inner.txt" })
	p.entry.Emit("activate")
	waitForUI(t, func() bool {
		doc, _ := w.documentForPath(filepath.Join(sub, "inner.txt"))
		return doc != nil && p.closed
	})

	w.showPathPicker()
	p = w.lastPalette
	p.entry.SetText("project")
	waitForUI(t, func() bool {
		item, ok := p.selected()
		return ok && item.title == "project" && item.icon == "folder-symbolic"
	})
	p.dialog.Close()
	waitForUI(t, func() bool {
		saved, err := recent.Load(w.recentPath())
		return err == nil && len(saved) >= 2
	})
}

// saveScreenshot writes the widget as a PNG when E_TEST_SCREENSHOTS names a
// folder, for reviewing designs by eye.
func saveScreenshot(t *testing.T, widget gtk.Widgetter, name string) {
	t.Helper()
	dir := os.Getenv("E_TEST_SCREENSHOTS")
	if dir == "" {
		return
	}
	waitFrames(t, widget, 3)
	pixels, stride, err := gtktest.Render(widget)
	if err != nil {
		t.Fatal(err)
	}
	height := len(pixels) / stride
	img := &image.RGBA{Pix: pixels, Stride: stride, Rect: image.Rect(0, 0, stride/4, height)}
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
