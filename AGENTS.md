# AGENTS.md

## Project

Shelf is a Go shell plugin manager modeled on [Sheldon](https://github.com/rossmacarthur/sheldon), Zinit and ZPlug.


| Concept | Value |
|---|---|
| Binary | `shelf` |
| Environment prefix | `SHELF_*` |
| Config directory | `~/.config/shelf` |
| Data directory | `~/.local/share/shelf` |
| Directory flags | `--config-dir`, `--data-dir`, `--config-file` |

## Repository layout

| Path | Responsibility |
|---|---|
| `cmd/shelf` | Executable entrypoint |
| `internal/cli` | Cobra command surface and path resolution |
| `internal/config` | TOML model, validation, and minimally destructive editing |
| `internal/source` | Git, HTTP, local, and inline source acquisition |
| `internal/lock` | Lock model, file selection, persistence, and verification |
| `internal/render` | Bash/Zsh script and template rendering |
| `docs/superpowers` | Design and implementation documents |

## Development commands

Run from the repository root:

```sh
gofmt -w $(find . -name '*.go' -type f)
go vet ./...
go build ./cmd/shelf
go test ./...
```

Run focused tests with:

```sh
go test ./internal/cli -run TestName
go test ./internal/config -run TestName
go test ./internal/source -run TestName
go test ./internal/lock -run TestName
go test ./internal/render -run TestName
```

If `golangci-lint` is configured for this repo, run it before submitting changes:

```sh
golangci-lint run ./...
```

## Code rules

**Architecture**

* Keep Cobra parsing and flag/env/path resolution in `internal/cli`; delegate config, source, lock, and rendering behavior to their respective packages. `internal/cli` should orchestrate, not implement.
* Prefer the Go standard library over third-party dependencies where practical. Justify any new dependency in the PR description.

**Compatibility and I/O**

* Keep `source` shell code on stdout only. Send all status, progress, and diagnostic output to stderr.
* Keep Bash and Zsh behavior explicit, tested, and reviewed together — a change to one shell's output should be checked against the other.
* Do not change `SHELF_*` env var names or shelf's default directories without updating tests and the README in the same change.

**Config and filesystem safety**

* Keep config edits minimally destructive: preserve comments, formatting, and unrelated TOML when adding, removing, or updating plugins.
* Use temporary files plus atomic renames (`os.Rename` within the same filesystem) for any downloaded or generated content — never write final output files in place.
* Use argument arrays with `os/exec` (e.g. `exec.Command(name, args...)`); never build shell commands through string concatenation or interpolation.

**Errors and concurrency**

* Wrap errors with `fmt.Errorf("...: %w", err)` to preserve the chain; avoid swallowing or discarding errors silently.
* Prefer `context.Context` for cancellation and timeouts on network and subprocess calls (git clones, HTTP fetches); thread it through rather than reaching for globals.

**Testing**

* Write tests after adding new production behavior, and always when adding or changing Sheldon-compatibility behavior (command names, TOML shape, lock format, rendered output).
* Prefer table-driven tests and golden-file comparisons for renderer and lock output, consistent with existing tests in each package.

## Workflow

* Run `gofmt`, `go vet`, `go build`, and `go test ./...` and lint before considering a change complete.
* Do not commit changes unless explicitly requested.
* Keep commits and PRs scoped to a single logical change; call out any Sheldon-compatibility implication explicitly in the description.
* Use unslop skill to write comments clearly and concisely, avoiding unnecessary verbosity and ensuring they add value to the code. Same
  applies for generated commit
* Use brainstroming skill to generate and evaluate ideas effectively before implementing them in code.
* Review and refactor code regularly to maintain clarity, simplicity, and adherence to established patterns and best practices.
* Try to use sub-agent driven development whenever possible
