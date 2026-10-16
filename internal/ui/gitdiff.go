package ui

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/jsnjack/e/internal/dispatch"
	"github.com/jsnjack/e/internal/document"
	"github.com/jsnjack/e/internal/linediff"
	"github.com/jsnjack/e/internal/source"
)

// diffDelayMS is the pause after an edit before change marks update.
const diffDelayMS = 300

// diffState is a document's gutter change marks. The base is kept as line
// hashes, so the buffer stays the only full copy of the text.
type diffState struct {
	base    []uint64
	baseGen uint64
	gen     uint64
	timer   glib.SourceHandle
	// marks are the bars drawn now.
	marks []source.DiffMark
}

// refreshDiffBase reloads what the marks compare against: the Git index copy
// of a tracked file, else the file as saved on disk. It runs after load,
// save, rename, and when the window regains focus, since a commit or
// `git add` elsewhere changes the index.
func (d *editorDocument) refreshDiffBase() {
	d.diff.baseGen++
	generation, path := d.diff.baseGen, d.path
	if path == "" || d.loading {
		d.diff.base, d.diff.marks = nil, nil
		d.view.SetDiffMarks(nil)
		return
	}
	ctx := d.window.ctx
	go func() {
		base, err := diffBase(ctx, path)
		if err != nil {
			slog.Debug("diff base", "path", path, "err", err)
		}
		dispatch.Main(func() {
			if d.closed || d.diff.baseGen != generation || d.path != path {
				return
			}
			d.diff.base = base
			d.updateDiffMarks()
		})
	}()
}

func diffBase(ctx context.Context, path string) ([]uint64, error) {
	command := exec.CommandContext(ctx, "git", "-C", filepath.Dir(path), "show", ":./"+filepath.Base(path))
	if out, err := command.Output(); err == nil {
		return linediff.Lines(string(bytes.TrimPrefix(out, []byte{0xef, 0xbb, 0xbf}))), nil
	}
	file, err := document.Open(path)
	if err != nil {
		return nil, err
	}
	return linediff.Lines(string(file.Text)), nil
}

// scheduleDiff recomputes the marks once edits pause.
func (d *editorDocument) scheduleDiff() {
	if d.diff.base == nil || d.diff.timer != 0 {
		return
	}
	d.diff.timer = glib.TimeoutAdd(diffDelayMS, func() bool {
		d.diff.timer = 0
		d.updateDiffMarks()
		return false
	})
}

// updateDiffMarks reads the buffer in idle-time chunks, then hashes and
// compares it off the GTK thread. Only the newest comparison is drawn.
func (d *editorDocument) updateDiffMarks() {
	d.diff.gen++
	generation, base := d.diff.gen, d.diff.base
	if base == nil {
		d.diff.marks = nil
		d.view.SetDiffMarks(nil)
		return
	}
	valid := func() bool { return d.diff.gen == generation }
	d.textSnapshotAsync(valid, func(text string) {
		go func() {
			hunks := linediff.Compare(base, linediff.Lines(text))
			dispatch.Main(func() {
				if d.closed || !valid() {
					return
				}
				d.diff.marks = diffMarks(hunks)
				d.view.SetDiffMarks(d.diff.marks)
			})
		}()
	})
}

func diffMarks(hunks []linediff.Hunk) []source.DiffMark {
	marks := make([]source.DiffMark, len(hunks))
	for i, hunk := range hunks {
		marks[i] = source.DiffMark{Line: hunk.Line, Count: hunk.Count, Kind: int(hunk.Kind)}
	}
	return marks
}

func (d *editorDocument) stopDiff() {
	if d.diff.timer != 0 {
		glib.SourceRemove(d.diff.timer)
		d.diff.timer = 0
	}
	d.diff.gen++
	d.diff.baseGen++
}

// showChangesInGdiff opens gdiff on the Git repository of the active file,
// or of the project folder.
func (w *window) showChangesInGdiff() {
	dir := w.projectRoot
	if doc := w.active(); doc != nil && doc.path != "" {
		dir = filepath.Dir(doc.path)
	}
	if dir == "" {
		w.toast("Open a file in a Git repository")
		return
	}
	go func() {
		message := ""
		out, err := exec.CommandContext(w.ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
		if err != nil {
			message = "Not a Git repository"
		} else if err := launchGdiff(strings.TrimSpace(string(out))); err != nil {
			slog.Warn("launch gdiff", "err", err)
			message = "Couldn’t start gdiff"
			if errors.Is(err, exec.ErrNotFound) {
				message = "Install gdiff to see changes"
			}
		}
		if message != "" {
			dispatch.Main(func() { w.toast(message) })
		}
	}()
}

// launchGdiff starts gdiff in its own session, so it outlives E and ignores
// signals sent to E's terminal. A running gdiff opens the repo as a tab.
func launchGdiff(repo string) error {
	command := exec.Command("gdiff", "--view", "changes", repo)
	command.Dir = repo
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}
