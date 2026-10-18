# E

E is a native text and code editor for GNOME. It is written in Go with GTK 4,
libadwaita, and GtkSourceView 5. The interface stays close to GNOME Text Editor
while adding inline AI and language-server completion.

## Features

- Syntax highlighting, line numbers, undo and redo through GtkSourceView 5.
- Search and replace with highlighted matches.
- Chunked loading and background disk access for multi-megabyte files.
- Atomic saves, line-ending preservation, session recovery, and EditorConfig.
- Opt-in inline AI completion with cancellation and an ordered fallback chain.
- Review-before-apply AI transformations for selected text.
- Offline completion through a local OpenAI-compatible endpoint such as
  Lemonade serving Gemma.
- LSP completion and diagnostics when a matching language server is installed.
- Spell checking through libspelling and VS Code-style multiple cursors.
- A collapsible, lazily scanned file tree. Without an open folder, `Ctrl+B`
  uses the active file's Git root or folder.
- Fuzzy Command Palette and asynchronous project Quick Open, which follows
  `.gitignore` inside Git repositories.
- Unmodified files reload when they change on disk. Unsaved text is written
  to the session every few seconds, so a crash keeps it.
- Live, side-by-side GitHub-flavored Markdown preview through WebKitGTK 6.

Automatic AI suggestions while typing are disabled by default and require an
explicit, persisted consent in Preferences. A manual `Alt+\` request works
independently and is itself an explicit, one-shot request. The default model
is `Gemma-4-E4B-it-GGUF` at Lemonade on `localhost:13305`; add remote models in
Preferences. On first launch, E imports
the ordered AI chain from Mailbox when available. Requests contain bounded text
around the caret and the file's base name, never its full path. Credentials are
read from the system keyring, first under `e-ai` and then under `mailbox-ai`; they
are never stored in the configuration file and can be explicitly forgotten in
Preferences.

## Build

On Fedora, install the native build and runtime packages:

```sh
sudo dnf install golang gcc gtk4-devel libadwaita-devel gtksourceview5 libspelling webkitgtk6.0
make build
```

Run `./e [FILE_OR_DIRECTORY...]` from the repository. `make check` runs the
portable quality gate. `make test-ui` runs the GTK interaction and native
Markdown-preview smoke tests in a private headless Weston session, so no
windows appear (`sudo dnf install weston`; set `E_TEST_VISIBLE=1` to watch
them on the desktop instead). Installation
targets `/usr/local` by default:

```sh
sudo make install
```

Each GitHub release provides an RPM and a binary tarball. Install the RPM
with `sudo dnf install ./e-<version>-1.fc44.x86_64.rpm`. `make release` builds
both and publishes them with [grm](https://github.com/jsnjack/grm) under the
version that monova calculates from the commit history.

## Configuration

Configuration is stored at `$XDG_CONFIG_HOME/e/config.toml`, or
`~/.config/e/config.toml`. Models appear as an ordered `[[ai.chain]]` list. Every
entry is tried according to availability after automatic suggestions are enabled.
Plain HTTP endpoints require `allow_insecure_http = true`; loopback endpoints are
accepted automatically.

Common shortcuts:

| Shortcut | Action |
|---|---|
| `Ctrl+O` / `Ctrl+S` | Open / save |
| `Ctrl+F` / `Ctrl+H` | Find / replace |
| `Ctrl+Shift+F` | Search the project folder |
| `F4` / `Shift+F4` | Open the next / previous folder search result |
| `Tab` | Accept an inline suggestion |
| `Ctrl+Right` | Accept the next part of a suggestion |
| `Alt+\` | Request an AI suggestion |
| `Ctrl+Space` | Request an LSP suggestion |
| `Ctrl+D` | Add the next matching occurrence |
| `Ctrl+Shift+L` | Select all matching occurrences |
| `Ctrl+Shift+Up/Down` or `Shift+Alt+Up/Down` | Add a cursor above or below |
| `Alt+Click` | Add or remove a cursor |
| `Shift+Alt+Drag` | Column selection, one cursor per line |
| `Ctrl+U` | Remove the last added cursor |
| `Escape` | Return to one cursor |
| `Ctrl+B` | Show or hide the project sidebar |
| `Ctrl+Shift+E` | Show the project files |
| `Ctrl+Shift+P` / `Ctrl+P` | Command Palette / Quick Open |
| `Ctrl+Shift+V` | Toggle Markdown preview |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Select the next / previous tab |
| `F3` / `Shift+F3` | Find next / previous |
| `Ctrl+F2` / `Alt+Enter` | Select all occurrences |
| `Ctrl+K Ctrl+S` | Show shortcut help |

## Large files

File reads, saves, AI calls, and language-server startup happen outside GTK's
main thread. Loaded text is inserted in 64 KiB idle chunks with highlighting
temporarily disabled. AI receives only a bounded cursor context, and language
servers are skipped for files larger than 2 MiB. Search computation runs in a
worker and limits on-screen highlighting to 2,000 matches while retaining the
full match count.
