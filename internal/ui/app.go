// Package ui owns E's GTK4 and libadwaita presentation layer.
package ui

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/jsnjack/e/internal/ai"
	"github.com/jsnjack/e/internal/config"
	"github.com/jsnjack/e/internal/logging"
)

const appID = "com.jsnjack.e"

// Options are the non-widget dependencies needed by the editor UI.
type Options struct {
	Version          string
	ConfigPath       string
	Config           config.Config
	Paths            config.Paths
	Completer        *ai.Completer
	RebuildCompleter func(config.Config, map[string]string, []string) (*ai.Completer, error)
	TestModel        func(context.Context, config.Model, string) error
}

// Run launches the GTK application and blocks until it exits.
func Run(options Options, paths []string) error {
	logging.Trace("ui: create application", "files", len(paths))
	app := adw.NewApplication(appID, gio.ApplicationHandlesOpen)
	var editor *window
	ensureWindow := func() *window {
		if editor == nil {
			logging.Trace("ui: create window")
			editor = newWindow(app, options)
		}
		// GApplication forwards later `e <path>` invocations to this process.
		// Present on every activation/open, not only on first construction, so
		// the existing editor leaves GTK's dimmed backdrop state and receives
		// focus instead of changing behind the invoking terminal.
		logging.Trace("ui: present window")
		editor.present()
		logging.Trace("ui: window presented")
		return editor
	}
	app.ConnectActivate(func() {
		logging.Trace("ui: activate")
		ensureWindow().restoreOrNew()
	})
	app.ConnectOpen(func(files []gio.Filer, _ string) {
		logging.Trace("ui: open files", "count", len(files))
		win := ensureWindow()
		win.restoreSession()
		for _, file := range files {
			win.openPath(file.Path())
		}
		// `e <folder>` with no restored tabs shows the empty state, which
		// offers Quick Open for that folder, instead of an untitled file.
	})
	argv := []string{"e"}
	argv = append(argv, paths...)
	logging.Trace("ui: run application")
	code := app.Run(argv)
	if editor != nil {
		editor.stop()
	}
	if code != 0 {
		return fmt.Errorf("GTK application exited with code %d", code)
	}
	slog.Debug("e stopped")
	return nil
}
