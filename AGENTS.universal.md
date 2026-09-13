# Universal Project Conventions

Run `make check` after every change. It must pass before reporting work as
complete.

## Work patterns

- Keep each change focused and its footprint minimal.
- Ask before choosing among materially different valid requirements.
- Describe plans in plain language rather than code vocabulary.
- Run `make build` after a change compiles so the binary stays current.
- Keep `AGENTS.md` synchronized with architectural and structural decisions.
- Never commit on behalf of the user.

## Build and validation

The validation gate runs format, vet, build, tests, and lint. Missing tools must
print an installation command and exit rather than installing automatically.

```sh
make check
make test
make build
make standards
```

## Logging

Commands with observable behavior expose two independent persistent flags:

- `--debug` / `-d` writes human-readable verbose output to stderr.
- `--trace` writes maximum detail to `/tmp/<binary>.log` and truncates it on
  each start.

Default output contains warnings and errors only. Never write logs to the
terminal from a TUI application.

## Code quality

- Keep entry points thin and give packages one responsibility.
- Express cross-package effects through explicit callbacks or interfaces.
- Wrap errors with operational context.
- Fail fast during setup and recover gracefully from runtime worker failures.
- Introduce dependencies only when their need has been explained and approved.
- State breaking behavior changes before implementing them.
- Match existing project patterns instead of adding competing ones.
- Document every exported symbol, and name symbols for their purpose.
- Remove dead code and avoid magic values used in more than one place.
- Comments explain non-obvious reasons, compatibility constraints, or security
  invariants. They do not narrate control flow or record implementation work.

## Writing style

Use calm, factual, complete sentences. Avoid marketing fragments, bold-lead
claims, obvious explanations, negative framing, competitor comparisons, and
jargon when an ordinary word is available.

## Commits

Commit subjects start with the `monova` change prefix: `M` for a breaking major
change, `m` for a compatible feature, or `p` for a compatible fix. Codex never
commits for the user.

## Testing

Tests live beside the code they cover and are deterministic, isolated, and
readable. New exported behavior and bug fixes receive tests. Table-driven cases
run independently with `t.Run`.
