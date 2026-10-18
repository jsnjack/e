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
  text appears only at a real line end: one line beside the caret, plus a
  `+N lines` hint. The source view draws it in its own `ABOVE_TEXT` layer. It
  never changes document layout, restyles document text, or enters the buffer.
  Typing the predicted text keeps the rest of the suggestion visible.
- Multiple cursors follow VS Code. The last cursor is GTK's own caret and
  selection; the others are character offsets, merged when they overlap.
  Their carets are drawn in the same source-view layer as ghost text.
- Change bars in the left text margin are drawn in that layer too. They
  compare the buffer with the Git index copy of a tracked file, else with
  the saved file. The base is kept as line hashes, not as text. Ctrl+Alt+D
  opens the file's repository in gdiff.
- Go to File or Folder (Ctrl+P) is the one picker. Text naming a folder (`/`,
  `~/`, or a relative path with a `/`) lists it; relative paths resolve
  against the project, or home without one. Other text fuzzy-matches project
  files, then recent paths (`internal/recent`) and home, then walks folders
  from home: `dow bana` finds `~/Downloads/banana.txt`.
- Find in Folder (Ctrl+Shift+F) is the sidebar's Search view, beside the
  file tree, as in VS Code. It searches off the GTK thread and stops at 1000
  matches. Results are grouped by file; F4 and Shift+F4 step through them.
  Ctrl+F and Ctrl+Shift+F start from a one-line selection.
- The project sidebar is a `GtkListView` over a `GtkTreeListModel`. Folders
  are read off the GTK thread when expanded, and file operations reload only
  the affected folder, so expanded folders stay open. Dot files are hidden
  unless the sidebar's `show_hidden_files` switch is on.
- With no tab open, the editor area shows an empty state instead of an
  untitled file. This happens after `e <folder>` and after the last tab closes.
- Unsaved text reaches the session a few seconds after each edit, and a tab
  that is still loading keeps its last session state.
- `internal/source` is the small dynamic cgo bridge to GtkSourceView 5 and
  libspelling. It wraps objects as their GTK base types for gotk4, and
  subclasses the source view to draw ghost text and secondary carets.
- `internal/gtktest` renders live widgets to pixels for `make test-ui`.
- `internal/document` owns file metadata, atomic saves, session recovery, and
  bounded context extraction. The GTK buffer remains the only live full copy.
- `internal/ai` owns OpenAI-compatible completion providers, ordered failover,
  availability state, cancellation, caching, and response cleanup.
- `internal/activity` is the GTK-free, non-blocking operation event hub. The UI
  renders it in a persistent bottom status bar with an activity-history panel.
- `internal/lsp` is a dependency-free JSON-RPC client for configured language
  servers. The UI shares one process per project root, language, and command;
  pipe writes go through an unbounded FIFO away from GTK's main thread. The
  client answers server requests and reaps the process on close.
- `internal/markdownpreview` renders Markdown in a lazily loaded WebKitGTK view
  and replaces the page body in place after the first load.
- `internal/workspace` lists project files for Quick Open, through
  `git ls-files` inside a Git repository, and single folders for the picker.
  It also searches the text of those files for Find in Folder.
- `internal/recent` stores opened files and folders, ranked by how often and
  how lately each was opened.
- `internal/linediff` compares line hashes with a bounded Myers diff.
- `internal/editorconfig` applies the core `.editorconfig` properties without
  introducing another parser dependency.
- `data` contains freedesktop desktop metadata and the application icon. The
  installed metainfo is generated from `data/com.jsnjack.e.metainfo.xml.in`,
  which carries the monova version and date in its newest release entry.
- `packaging/e.spec` packages the prebuilt binary as an RPM. `make release`
  builds the RPM and tarball into `dist/` and publishes them with grm.
- `make test-ui` exercises GTK actions, shortcuts, tabs, editing, disk I/O, the
  file tree, the path picker, change bars, folder search, and Markdown preview.
  `scripts/headless.sh` runs it in a private headless Weston session;
  `E_TEST_VISIBLE=1` uses the desktop instead. `E_TEST_SCREENSHOTS=<dir>`
  saves PNGs of the empty state, path picker, change bars, and folder search.

The editor must remain interactive with a 5 MB highlighted document. Never run
network or process I/O on the GTK thread, never copy the whole document on each
edit, and cancel stale completion and LSP requests.
