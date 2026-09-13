# E

> See [AGENTS.universal.md](./AGENTS.universal.md) and [AGENTS.go.md](./AGENTS.go.md) for universal conventions.
> Refresh: `make standards`

E is a native, fast text and code editor for GNOME, written in Go with GTK4,
libadwaita, and GtkSourceView 5. It provides syntax highlighting, search and
replace, session recovery, LSP integration, spell checking, multiple-selection
editing, EditorConfig support, and availability-aware AI autocomplete.

## Architecture

- `cmd/e` is the thin Cobra entry point and application wiring.
- `internal/ui` owns GTK and libadwaita widgets. GTK is single-threaded; slow
  file, process, and network operations run outside the main loop. Inline AI
  text uses a Pango overlay: beside the caret at a real line end, or in reserved
  space below the paragraph otherwise. It never masks, redraws, restyles, or
  enters document text.
- `internal/source` is the small dynamic cgo bridge to GtkSourceView 5 and
  libspelling. It wraps objects as their GTK base types for gotk4.
- `internal/document` owns file metadata, atomic saves, session recovery, and
  bounded context extraction. The GTK buffer remains the only live full copy.
- `internal/ai` owns OpenAI-compatible completion providers, ordered failover,
  availability state, cancellation, caching, and response cleanup.
- `internal/activity` is the GTK-free, non-blocking operation event hub. The UI
  renders it in a persistent bottom status bar with an activity-history panel.
- `internal/lsp` is a dependency-free JSON-RPC client for configured language
  servers.
- `internal/editorconfig` applies the core `.editorconfig` properties without
  introducing another parser dependency.
- `data` contains freedesktop desktop metadata and the application icon.
- `make test-ui` exercises GTK actions, shortcuts, tabs, editing, disk I/O, and
  Markdown preview in a real graphical session; it is separate from headless checks.

The editor must remain interactive with a 5 MB highlighted document. Never run
network or process I/O on the GTK thread, never copy the whole document on each
edit, and cancel stale completion and LSP requests.
