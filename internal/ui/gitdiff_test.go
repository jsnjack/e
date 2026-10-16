package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/jsnjack/e/internal/config"
	"github.com/jsnjack/e/internal/linediff"
	"github.com/jsnjack/e/internal/source"
)

func TestDiffMarks(t *testing.T) {
	hunks := []linediff.Hunk{{Kind: linediff.Added, Line: 2, Count: 3}, {Kind: linediff.Deleted, Line: 9}}
	want := []source.DiffMark{{Line: 2, Count: 3, Kind: 1}, {Line: 9, Kind: 3}}
	if got := diffMarks(hunks); !reflect.DeepEqual(got, want) {
		t.Fatalf("diffMarks() = %+v, want %+v", got, want)
	}
}

// TestGitDiffRuntime checks that change marks follow the Git index of a
// tracked file and the saved copy of an untracked one.
func TestGitDiffRuntime(t *testing.T) {
	if os.Getenv("E_UI_RUNTIME_TEST") != "1" {
		t.Skip("set E_UI_RUNTIME_TEST=1 on a graphical test display")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	gtk.Init()
	app := adw.NewApplication("com.jsnjack.e.DiffRuntimeTest", gio.ApplicationNonUnique)
	if err := app.Register(context.Background()); err != nil {
		t.Fatalf("register test application: %v", err)
	}
	base := t.TempDir()
	cfg := config.Default()
	cfg.Editor.SpellCheck = false
	w := newWindow(app, Options{
		Config: cfg, ConfigPath: filepath.Join(base, "config.toml"),
		Paths: config.Paths{StateDir: filepath.Join(base, "state"), CacheDir: filepath.Join(base, "cache")},
	})
	defer func() {
		w.stop()
		w.win.Destroy()
	}()
	w.present()

	repo := filepath.Join(base, "repo")
	tracked := filepath.Join(repo, "main.go")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("package main\n\nfunc a() {}\n\nfunc b() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "main.go"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	// The working copy differs from the index before E opens it.
	if err := os.WriteFile(tracked, []byte("package main\n\nfunc a() {}\nfunc added() {}\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.openPath(tracked)
	doc := w.active()
	want := []source.DiffMark{{Line: 3, Count: 1, Kind: 1}, {Line: 5, Count: 1, Kind: 2}}
	waitForUI(t, func() bool { return reflect.DeepEqual(doc.diff.marks, want) })

	end := doc.buffer.EndIter()
	doc.buffer.Insert(end, "// tail\n")
	waitForUI(t, func() bool { return len(doc.diff.marks) == 3 })
	saveScreenshot(t, doc.view.Widget, "diff-marks.png")

	untracked := filepath.Join(base, "notes.txt")
	if err := os.WriteFile(untracked, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.openPath(untracked)
	notes := w.active()
	waitForUI(t, func() bool { return !notes.loading && notes.diff.base != nil })
	if len(notes.diff.marks) != 0 {
		t.Fatalf("saved file has marks %+v", notes.diff.marks)
	}
	start, _ := notes.buffer.IterAtLine(1)
	stop, _ := notes.buffer.IterAtLine(2)
	notes.buffer.Delete(start, stop)
	waitForUI(t, func() bool {
		return reflect.DeepEqual(notes.diff.marks, []source.DiffMark{{Line: 1, Kind: 3}})
	})
}
