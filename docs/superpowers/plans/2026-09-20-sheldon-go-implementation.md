# Sheldon Go Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a behaviorally equivalent Go implementation of Sheldon with Cobra, TOML configuration, plugin locking, and Bash/Zsh source generation.

**Architecture:** Cobra dispatches commands into deep internal modules. Config normalization produces a validated plugin model, source adapters acquire plugin content, the lock module records resolved files and templates, and the renderer generates shell code from locked data.

**Tech Stack:** Go, Cobra, BurntSushi/toml, doublestar, pongo2, standard-library `net/http` and `os/exec`.

**Spec:** `docs/superpowers/specs/2026-09-20-sheldon-go-design.md`

## Global Constraints

- Binary name: `sheldon`.
- Support Bash and Zsh behavior first.
- Preserve Sheldon command names, config concepts, default paths, and `SHELDON_*` environment variables.
- Lock and completion formatting may differ while behavior remains equivalent.
- Use TDD for production behavior and run focused tests after every slice.

---

### Task 1: Project foundation and Cobra command surface

**Files:**
- Create: `go.mod`
- Create: `cmd/sheldon/main.go`
- Create: `internal/cli/root.go`
- Create: `internal/cli/root_test.go`
- Create: `README.md`

**Interfaces:**
- Produce `cli.NewRoot() *cobra.Command`.
- Produce `cli.Execute(args []string, stdout, stderr io.Writer) error`.
- Define a runtime `Context` with resolved config file, config directory, data directory, profile, shell, and output settings.

- [ ] Write a failing test that parses `init`, `lock`, `source`, `add`, `edit`, `remove`, `completions`, and `version`, and verifies global flags can appear before a command.
- [ ] Run `go test ./internal/cli -run TestRootCommands` and confirm the missing command surface fails.
- [ ] Add the Go module and Cobra dependency.
- [ ] Implement root command metadata, global flags, environment-variable fallbacks, and command placeholders returning no error.
- [ ] Run `go test ./internal/cli -run TestRootCommands` and `go test ./...`.
- [ ] Add usage documentation with build and basic invocation examples.

### Task 2: Path resolution and initialization

**Files:**
- Create: `internal/cli/context.go`
- Create: `internal/cli/context_test.go`
- Create: `internal/config/init.go`
- Create: `internal/config/init_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- `ResolvePaths(home, configDir, dataDir, configFile string) (Paths, error)`.
- `config.Initialize(path string, shell Shell) error`.
- `config.DefaultConfig(shell Shell) string`.

- [ ] Write tests for default XDG paths, explicit directory/file overrides, and `init` idempotence.
- [ ] Run the focused tests and confirm they fail because path and initialization functions do not exist.
- [ ] Implement path precedence and Bash/Zsh initial TOML content.
- [ ] Wire `init` to create parent directories and leave an existing file unchanged.
- [ ] Run `go test ./internal/cli ./internal/config`.

### Task 3: Config model and minimally destructive editing

**Files:**
- Create: `internal/config/model.go`
- Create: `internal/config/load.go`
- Create: `internal/config/edit.go`
- Create: `internal/config/load_test.go`
- Create: `internal/config/edit_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- `config.Load(path string) (Config, error)`.
- `config.Add(path string, name string, plugin RawPlugin) error`.
- `config.Remove(path string, name string) error`.
- `config.Validate(Config) error`.

- [ ] Write tests for all plugin source kinds, references, use/apply/profiles/hooks, duplicate names, missing sources, and preservation of unrelated TOML content during add/remove.
- [ ] Run focused tests and verify expected failures.
- [ ] Implement raw TOML decoding and normalized validation using `BurntSushi/toml` plus a text-preserving edit strategy.
- [ ] Wire `add`, `remove`, and `edit`; `edit` validates the edited file before replacing the config.
- [ ] Run `go test ./internal/config ./internal/cli`.

### Task 4: Source acquisition adapters

**Files:**
- Create: `internal/source/source.go`
- Create: `internal/source/git.go`
- Create: `internal/source/remote.go`
- Create: `internal/source/local.go`
- Create: `internal/source/source_test.go`

**Interfaces:**
- `source.Installer` with `Install(ctx context.Context, request Request) (Installed, error)`.
- `source.Request` represents Git, remote, local, and inline sources plus update/reinstall mode.
- `source.Installed` contains source directory and optional downloaded file.

- [ ] Write tests using temporary Git repositories and an `httptest.Server` for clone, update, reinstall, remote download, and local validation.
- [ ] Run focused tests and confirm failures.
- [ ] Implement Git commands with argument arrays, safe temporary paths, reference checkout, and deterministic data directories.
- [ ] Implement HTTP downloads with status checking and atomic writes.
- [ ] Implement local and inline source handling.
- [ ] Run `go test ./internal/source`.

### Task 5: Lock model and plugin file selection

**Files:**
- Create: `internal/lock/model.go`
- Create: `internal/lock/lock.go`
- Create: `internal/lock/glob.go`
- Create: `internal/lock/lock_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- `lock.Build(ctx Context, cfg config.Config, installer source.Installer, mode Mode) (LockedConfig, error)`.
- `lock.Verify(path string, ctx Context) (bool, error)`.
- `lock.Write(path string, locked LockedConfig) error`.
- `lock.Read(path string) (LockedConfig, error)`.

- [ ] Write tests for default Bash/Zsh matches, explicit `use`, first matching global pattern, profile filtering, inline plugins, hooks, and custom directory templates.
- [ ] Run focused tests and confirm failures.
- [ ] Implement lock construction, source de-duplication, ordered plugins, recursive glob selection with `doublestar`, and equivalent TOML lock serialization.
- [ ] Implement lock verification against context, directories, and selected files.
- [ ] Wire `lock` and automatic relocking in `source`.
- [ ] Run `go test ./internal/lock ./internal/source ./internal/config`.

### Task 6: Sheldon-compatible rendering

**Files:**
- Create: `internal/render/builtin.go`
- Create: `internal/render/template.go`
- Create: `internal/render/render_test.go`
- Modify: `internal/lock/lock.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- `render.Script(locked lock.LockedConfig, shell Shell) (string, error)`.
- `render.BuiltinTemplates(shell Shell) map[string]string`.
- `render.Template(name string, text string, data PluginData) (string, error)`.

- [ ] Write tests for Bash and Zsh source/PATH/path/fpath templates, pre/post hooks, inline output, newline handling, and custom loop templates.
- [ ] Run focused tests and confirm failures.
- [ ] Implement built-in templates and the supported Sheldon template syntax through Pongo2, including `files`, `dir`, `name`, `hooks`, `data_dir`, and `nl` behavior.
- [ ] Wire `source` to stdout and diagnostics to stderr.
- [ ] Run `go test ./internal/render ./internal/lock`.

### Task 7: Complete CLI behavior and end-to-end tests

**Files:**
- Create: `internal/cli/completions.go`
- Create: `internal/cli/e2e_test.go`
- Modify: `internal/cli/root.go`
- Modify: `README.md`

**Interfaces:**
- Cobra completion generation for supported shells.
- Exit codes and stdout/stderr behavior matching the compatibility contract.

- [ ] Write end-to-end tests that initialize a temporary config, add a local plugin, lock it, source it, remove it, and verify generated Bash/Zsh output.
- [ ] Add tests for command conflicts, `--update` versus `--reinstall`, profile-specific lock files, and environment-variable overrides.
- [ ] Run the end-to-end tests and verify failures.
- [ ] Implement completions, version information, editor selection, diagnostics, and command conflicts.
- [ ] Run `go test ./...` and `go vet ./...`.
- [ ] Build with `go build ./cmd/sheldon` and manually verify `./sheldon --help`, `./sheldon init`, and `./sheldon source` in a temporary HOME.

### Task 8: Final compatibility pass

**Files:**
- Modify: `README.md`
- Create: `testdata/compatibility/`
- Create: `internal/compatibility/compatibility_test.go`

- [ ] Add fixture configs for GitHub, remote, local, inline, profiles, hooks, custom templates, and both shells.
- [ ] Run all tests and inspect generated scripts and lock data for each fixture.
- [ ] Fix only compatibility defects found by the fixtures.
- [ ] Run `gofmt -w` on Go files, `go test ./...`, `go vet ./...`, and `go build ./cmd/sheldon`.
