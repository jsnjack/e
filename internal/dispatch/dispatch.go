// Package dispatch marshals background results onto GTK's main loop.
package dispatch

import "github.com/diamondburned/gotk4/pkg/core/glib"

// Main schedules fn on the GTK main loop.
func Main(fn func()) { glib.IdleAdd(fn) }
