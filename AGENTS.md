# AGENTS.md

## Project

Shelf is a Go shell plugin manager modeled on Sheldon.

Compatibility target: preserve Sheldon command names, TOML concepts, Bash and Zsh behavior, lock semantics, and source output. Project-specific names differ:

- Binary: `shelf`
- Environment prefix: `SHELF_*`
- Config directory: `~/.config/shelf`
- Data directory: `~/.local/share/shelf`
- Directory flags remain `--config-dir`, `--data-dir`, and `--config-file`

## Repository layout

- `cmd/shelf`: executable entrypoint
- `internal/cli`: Cobra command surface and path resolution
- `internal/config`: TOML model, validation, and minimally destructive editing
- `internal/source`: Git, HTTP, local, and inline source acquisition
- `internal/lock`: lock model, file selection, persistence, and verification
- `internal/render`: Bash/Zsh script and template rendering
- `docs/superpowers`: design and implementation documents

## Development commands

Run from repository root:

```sh
gofmt -w $(find . -name '*.go' -type f)
go test ./...
go vet ./...
go build ./cmd/shelf
```

Run focused tests with:

```sh
go test ./internal/cli -run TestName
go test ./internal/config -run TestName
go test ./internal/source -run TestName
go test ./internal/lock -run TestName
go test ./internal/render -run TestName
```

## Code rules

- Use Go standard library APIs where practical.
- Keep Cobra parsing in `internal/cli`; delegate config, source, lock, and rendering behavior to their packages.
- Write tests before new production behavior when adding or changing compatibility behavior.
- Keep `source` shell code on stdout. Send status and diagnostics to stderr.
- Keep config edits minimally destructive: preserve comments and unrelated TOML when adding or removing plugins.
- Use argument arrays with `os/exec`; never build shell commands through string concatenation.
- Use temporary files and atomic renames for downloaded content.
- Keep Bash and Zsh behavior explicit and tested.
- Do not change `SHELF_*` names or shelf default directories without updating tests and README.
- Do not commit changes unless explicitly requested.

