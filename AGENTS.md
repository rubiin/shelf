# AGENTS.md

## Project

Shelf is a Go shell plugin manager modeled on [Sheldon](https://github.com/rossmacarthur/sheldon), Zinit, and Zplug.

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
golangci-lint run ./...
```

Run focused tests with:

```sh
go test ./internal/cli -run TestName
go test ./internal/config -run TestName
go test ./internal/source -run TestName
go test ./internal/lock -run TestName
go test ./internal/render -run TestName
```

## Definition of done

A change is not complete until all of the following hold:

1. `gofmt` produces no output (all files formatted).
2. `go vet ./...`, `go build ./cmd/shelf`, and `go test ./...` pass with no failures.
3. `golangci-lint run ./...` reports zero issues.
4. New or changed behavior has test coverage; Sheldon-compatibility changes have explicit tests.
5. The diff contains only the requested change. No unrelated edits, reformatting, or files.
6. No signature, footer, or attribution is appended to the commit (see Commit standards).

## Code rules

**Architecture**

- Keep Cobra parsing and flag/env/path resolution in `internal/cli`; delegate config, source, lock, and rendering behavior to their respective packages. `internal/cli` orchestrates, it does not implement.
- Prefer the Go standard library over third-party dependencies. Any new dependency must be justified in the PR description.
- Keep packages decoupled: communicate through exported functions and types, not shared globals or package-state imports.

**Compatibility and I/O**

- Keep `source` shell code on stdout only. Send all status, progress, and diagnostic output to stderr.
- Keep Bash and Zsh behavior explicit, tested, and reviewed together: a change to one shell's output must be checked against the other.
- Do not change `SHELF_*` env var names or shelf's default directories without updating tests and the README in the same change.

**Config and filesystem safety**

- Keep config edits minimally destructive: preserve comments, formatting, and unrelated TOML when adding, removing, or updating plugins.
- Use temporary files plus atomic renames (`os.Rename` within the same filesystem) for any downloaded or generated content. Never write final output files in place.
- Use argument arrays with `os/exec` (for example `exec.Command(name, args...)`). Never build shell commands through string concatenation or interpolation.

**Errors and concurrency**

- Wrap errors with `fmt.Errorf("...: %w", err)` to preserve the chain. Never swallow or discard errors silently.
- Prefer `context.Context` for cancellation and timeouts on network and subprocess calls (git clones, HTTP fetches). Thread it through rather than reaching for globals.

**Testing**

- Write tests after adding new production behavior, and always when adding or changing Sheldon-compatibility behavior (command names, TOML shape, lock format, rendered output).
- Prefer table-driven tests and golden-file comparisons for renderer and lock output, consistent with existing tests in each package.
- Add a regression test for every bug fix before considering the fix done.

## Best practices

**Code review**

- Re-read your own diff before finishing. Remove debug output, dead code, and speculative abstractions.
- Keep diffs small and single-purpose. If a change mixes concerns, split it.
- Prefer boring, explicit code over cleverness. Optimize only when a benchmark or profile shows a problem.

**Maintenance**

- Review and refactor code regularly to maintain clarity, simplicity, and adherence to established patterns.
- Keep comments concise and valuable: state why, not what. Run the `unslop` skill when writing or rewriting comments.
- Run the `brainstorming` skill to generate and evaluate ideas before implementing them in code.
- Use sub-agent-driven development whenever working on multiple independent tasks.

**Communication**

- Be direct and professional. Do not add pleasantries, filler, or sign-offs to the end of responses.
- Never append footers, signatures, or AI attribution to any text you produce (see Commit standards below for commits).

## Commit standards

- Use [Conventional Commits](https://www.conventionalcommits.org/): `<type>(<scope>): <summary>`. Use `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, or `revert`.
- Write the subject in the imperative, under 72 characters, with no trailing period. Explain body details as what and why, not how.
- Do not commit unless explicitly requested.
- Keep each commit and PR scoped to a single logical change; call out any Sheldon-compatibility implication explicitly in the description.
- Never append a footer to a commit message:
  - No `Co-Authored-By`, no `Signed-off-by` unless requested, no "Generated with", no AI tool or model names, no attribution lines of any kind.
- The same rule applies to all generated text: PR descriptions, issue reports, docs, and replies must not carry footers, signatures, or AI attribution.

## Workflow

- Run the checks in Definition of done and confirm their output before claiming any change is complete.
- Do not commit changes unless explicitly requested.
- Call out any Sheldon-compatibility implication explicitly in the PR description.