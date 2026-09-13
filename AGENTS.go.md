# Go Project Conventions

Applies to all Go projects in addition to `AGENTS.universal.md`.

**CLI framework:** Use cobra. Register commands and flags in `init()` within
`cmd/` files. Use `RunE` so errors propagate.

**Doc comments:** Every exported symbol's doc comment starts with the symbol's
name.

**Versioning:** Every binary declares a build-time `Version` variable, and the
Makefile stamps it with `monova` through `-ldflags`.

**Error handling:** Wrap errors with context. Return, handle, or trace every
error; do not discard one with a blank assignment.

**Logging:** Use `log/slog` with `slog.NewTextHandler`. `--debug` writes verbose
output to stderr and `--trace` writes maximum detail to `/tmp/<binary>.log`.

**Testing:** Use table-driven tests and the standard `testing` package.

**Commands and flags:** Root commands expose `--version`, persistent `--debug`
(`-d`), `--trace`, and `--config` (`-c`). No other flags receive short aliases.

**XDG layout:** Resolve configuration and cache directories with
`os.UserConfigDir` and `os.UserCacheDir`; resolve the data directory through
`XDG_DATA_HOME` with `os.UserHomeDir` as the fallback. Never hardcode `~`.

| Purpose | Default path |
|---|---|
| Configuration | `~/.config/<app>/config.<ext>` |
| Cache | `~/.cache/<app>/` |
| Persistent state | `~/.local/share/<app>/` |
| Trace log | `/tmp/<app>.log` |
