<p align="center">
  <img src="./logo.png" width="200" alt="Shelf" />
</p>
<h1 align="center">Shelf</h1>
<p align="center">
  <em>Fast, configurable shell plugin manager written in Go.</em>
</p>

<p align="center">
  <a href="https://github.com/rubiin/shelf/blob/master/LICENSE"><img alt="License" src="https://img.shields.io/github/license/rubiin/shelf" /></a>
  <a href="https://github.com/rubiin/shelf/actions"><img alt="GitHub Actions Workflow Status" src="https://img.shields.io/github/actions/workflow/status/rubiin/shelf/ci.yml"></a>
  <a href="https://aur.archlinux.org/packages/shelf-sh-bin"><img alt="AUR Version" src="https://img.shields.io/aur/version/shelf-sh-bin"></a>


</p>

## Features

- Git, GitHub, remote, local, and inline plugins.
- Branch, tag, and revision selection for Git sources.
- Bash and Zsh output.
- Plugin file selection with glob patterns.
- Profiles, hooks, custom apply templates, and lock files.
- TOML configuration.
- Cobra-generated shell completions.

## Installation

### Arch Linux

Install the packaged binary from the AUR:

```sh
yay -S shelf-sh-bin
```

The package also installs Bash, Zsh, and Fish completion files.

### Linux packages

Release builds include packages for Debian-based, RPM-based, and Alpine Linux
systems. Download the matching `.deb`, `.rpm`, or `.apk` file from the
[latest release](https://github.com/rubiin/shelf/releases/latest), then install
it with your distribution's package manager.

### Prebuilt archives

Linux and macOS tarballs are available on the [releases
page](https://github.com/rubiin/shelf/releases). Extract the archive and place
the `shelf` binary somewhere on your `PATH`.

### Build from source

Install Go 1.23 or newer, then build the binary locally:

```sh
git clone https://github.com/rubiin/shelf.git
cd shelf
go build -o shelf ./cmd/shelf
install -Dm755 shelf "$HOME/.local/bin/shelf"
```

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
shelf lock [--update | --reinstall] [--concurrency N]
shelf source [--relock | --update | --reinstall] [--concurrency N]
shelf update [--lock] [--concurrency N]
shelf path
shelf status
shelf doctor
shelf clean
shelf list
shelf add NAME ...
shelf edit
shelf remove NAME
shelf remove --interactive
shelf completions SHELL
shelf version
```

`lock` and `source` install plugins concurrently by default, with up to eight
installs in flight. Use `--concurrency N` to set a different positive limit.

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
SHELF_EDITOR="nvim --wait"
```

`XDG_CONFIG_HOME` and `XDG_DATA_HOME` still control base directories when
explicit shelf directory flags are absent.

`shelf edit` picks its editor in this order: `SHELF_EDITOR`, then `VISUAL`,
then `EDITOR`. The value is split with shell-word rules, so quoted paths and
flags with spaces are preserved. `SHELF_SHELL` accepts only `bash` or `zsh`;
another value is an error rather than a silent fallback.

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
shelf update
```

Update plugin sources and write the refreshed lockfile without printing shell code:

```sh
shelf update --lock
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
