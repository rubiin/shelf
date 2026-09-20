# shelf

Fast, configurable shell plugin manager written in Go.

Shelf follows Sheldon’s configuration model while using a different binary,
environment prefix, and default directory name.

## Features

- Git, GitHub, remote, local, and inline plugins.
- Branch, tag, and revision selection for Git sources.
- Bash and Zsh output.
- Plugin file selection with glob patterns.
- Profiles, hooks, custom apply templates, and lock files.
- TOML configuration.
- Cobra-generated shell completions.

## Differences from Sheldon

- Binary: `shelf` instead of `sheldon`.
- Environment variables: `SHELF_*` instead of `SHELDON_*`.
- Default config directory: `~/.config/shelf`.
- Default data directory: `~/.local/share/shelf`.
- Build system: Go modules instead of Cargo.
- Lock files use equivalent TOML data, not byte-identical Sheldon output.

## Getting started

Initialize a Bash or Zsh configuration:

```sh
shelf init
```

Add a plugin manually:

```toml
[plugins.base16]
github = "chriskempson/base16-shell"
```

Or add an inline plugin:

```toml
[plugins.prompt]
inline = "echo 'plugin loaded'"
```

Install plugins and generate shell code:

```sh
shelf lock
eval "$(shelf source)"
```

Add the `eval` command to `.bashrc` or `.zshrc`.

## Build and test

Requirements: Go 1.23 or newer and Git for Git sources.

```sh
go build ./cmd/shelf
go test ./...
go vet ./...
```

## Command-line interface

```text
shelf init
shelf lock [--update | --reinstall]
shelf source [--relock | --update | --reinstall]
shelf add NAME ...
shelf edit
shelf remove NAME
shelf completions SHELL
shelf version
```

Global options:

```text
--quiet
--non-interactive
--verbose
--color auto|always|never
--config-dir PATH
--data-dir PATH
--config-file PATH
--profile PROFILE
```

Directory flags keep Sheldon’s names. Their environment equivalents use the
`SHELF_` prefix:

```sh
SHELF_CONFIG_DIR="$HOME/.config/shelf"
SHELF_DATA_DIR="$HOME/.local/share/shelf"
SHELF_CONFIG_FILE="$HOME/.config/shelf/config.toml"
SHELF_PROFILE="work"
SHELF_SHELL="zsh"
```

`XDG_CONFIG_HOME` and `XDG_DATA_HOME` still control base directories when
explicit shelf directory flags are absent.

## Configuration

Basic plugin sources:

```toml
shell = "zsh"

[plugins.git-plugin]
git = "https://github.com/example/plugin.git"
branch = "main"
use = ["*.zsh"]

[plugins.remote-plugin]
remote = "https://example.com/plugin.zsh"

[plugins.local-plugin]
local = "/path/to/plugins"

[plugins.inline-plugin]
inline = "echo loaded"
```

Plugin options include `use`, `apply`, `profiles`, `hooks`, `dir`, and `file`.
Profiles restrict loading to a selected `--profile`. `use` accepts recursive
glob patterns relative to the installed plugin directory.

Locking records installed sources and selected files under:

```text
$XDG_CONFIG_HOME/shelf/plugins.lock
```

`source` verifies the lock context and selected files. It regenerates the lock
when the configuration, profile, shell, or installed files changed.

## Examples

Update plugin sources:

```sh
shelf lock --update
```

Reinstall all sources:

```sh
shelf lock --reinstall
```

Use a separate profile:

```sh
SHELF_PROFILE=work shelf source
```

Use a temporary configuration:

```sh
shelf --config-file /tmp/config.toml source
```

## Status

The repository contains focused tests for CLI path resolution, TOML
configuration, source acquisition, lock handling, and shell rendering.
Compatibility work continues against Sheldon’s upstream fixtures.
