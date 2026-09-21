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

Install Go 1.26 or newer, then build the binary locally:

```sh
git clone https://github.com/rubiin/shelf.git
cd shelf
just build
install -Dm755 shelf "$HOME/.local/bin/shelf"
```

`just build` (and every release build) passes `-trimpath`, `-buildvcs=false`,
and `-ldflags "-s -w"`. That keeps the binary around 7 MB instead of 11 MB and
makes builds reproducible. Without `just`, the same build is:

```sh
go build -trimpath -buildvcs=false -ldflags "-s -w" -o shelf ./cmd/shelf
```

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

Add the `eval` command to `.bashrc` or `.zshrc`. Each plugin is evaluated
separately, so aliases and functions defined by one plugin are available to
later plugins.

## Build and test

Requirements: Go 1.26 or newer and Git for Git sources.

```sh
go build ./cmd/shelf
go test ./...
go vet ./...
```

## Benchmarks

`scripts/bench-vs-sheldon.sh` compares `shelf source` with `sheldon source`. It
builds shelf, writes one config into a temporary `HOME` that both tools read,
locks both, checks that the rendered script is identical, and then measures
three commands with [hyperfine](https://github.com/sharkdp/hyperfine): process
startup, `source` with a warm lock, and `eval "$(… source)"` in a shell. Your
real config and data directories are never touched.

```sh
just bench              # 20 plugins, 100 runs
just bench runs=25      # fewer runs
./scripts/bench-vs-sheldon.sh --plugins 60 --export bench.md
```

Measured on a Ryzen 7 5700U with hyperfine 1.20.0, sheldon 0.8.5, and a config
of 20 plugins (12 local, 8 inline):

| command | sheldon | shelf |
| --- | --- | --- |
| startup (`version`) | 13.4 ms | 5.6 ms |
| `source`, warm lock | 14.5 ms | 7.3 ms |
| `eval "$(… source)"` in bash | 16.9 ms | 10.2 ms |

Both tools render the same script, so the difference is overhead: sheldon
spends most of its time before it reads the config, while shelf reads, verifies,
and renders in less than sheldon takes to start.

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

Their environment equivalents use the
`SHELF_` prefix:

```sh
SHELF_CONFIG_DIR="$HOME/.config/shelf"
SHELF_DATA_DIR="$HOME/.local/share/shelf"
SHELF_CONFIG_FILE="$HOME/.config/shelf/plugins.toml"
SHELF_PROFILE="work"
SHELF_SHELL="zsh"
SHELF_EDITOR="nvim --wait"
```

`XDG_CONFIG_HOME` and `XDG_DATA_HOME` still control base directories when
explicit shelf directory flags are absent.

The configuration file is `plugins.toml`. When `--config-file` is set without
`--config-dir`, the configuration directory is that file's parent directory.

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

[plugins.optional-local-plugin]
local = "/path/to/optional-plugin"
optional = true

[plugins.inline-plugin]
inline = "echo loaded"
```

Plugin options include `use`, `apply`, `profiles`, `hooks`, `dir`, `file`, and
`proto`. `proto` selects how `github` and `gist` sources are cloned: `https`
(the default), `git`, or `ssh`. `shelf add --proto ssh` writes the same field.
`use` accepts recursive glob patterns relative to the installed plugin
directory.

A plugin that sets `optional = true` must use a `local` source. Shelf skips it
when its path does not exist. A plugin that sets `profiles` only loads while one of those profiles is
selected by `--profile` or `SHELF_PROFILE`; a plugin without `profiles` always
loads.

Templates engine: `{{ value }}` expressions with `| nl` filters,
`{% if %} … {% else if %} … {% else %} … {% endif %}` conditionals, and
`{% for file in files %}` loops that nest and expose `loop.index`,
`loop.first`, and `loop.last`. Maps such as `hooks` iterate with two
variables: `{% for name, value in hooks %}`. Lookups fail on a missing value
unless it is written optionally, as in `{{ hooks?.pre }}`.

```toml
[templates]
defer = "{{ hooks?.pre | nl }}{% for file in files %}zsh-defer source \"{{ file }}\"\n{% endfor %}{{ hooks?.post | nl }}"
```

Bare `{name}`, `{dir}`, `{file}`, and `{nl}` placeholders remain available as a
shelf extension for templates without `{{ }}` or `{% %}` blocks.

Sources are installed in different paths: git sources under
`$XDG_DATA_HOME/shelf/repos/<host>/<owner>/<repository>` and downloaded files
under `$XDG_DATA_HOME/shelf/downloads/<host>/<path>`. Inline plugins install
nothing: their text is recorded in the lock and rendered as a template, so each
inline plugin can use `{{ name }}` and its hooks.

Locking records the resolved templates, the installed sources, and the selected
files under:

```text
$XDG_DATA_HOME/shelf/plugins.lock
$XDG_DATA_HOME/shelf/plugins.<profile>.lock
```

`source` verifies the lock context and selected files. It regenerates the lock
when the configuration, profile, shell, or installed files changed. Commands
take a shared lock on the configuration directory while reading and an
exclusive lock while writing, so concurrent shells wait instead of racing.
Installed sources that are no longer configured are pruned by `lock`, by
`update`, and by `source` when it relocks.

Diagnostics go to stderr: `Loaded` and `Locked` headers, right-aligned
`Checked` and `Skipped` statuses, and `Unlocked`, `Rendered`, `Inlined`, and
`Removed` when `--verbose` is set. A failed command prints `error:` and exits
with status 2.

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
shelf --config-file /tmp/plugins.toml source
```

## Status

The repository contains focused tests for CLI path resolution, TOML
configuration, source acquisition, lock handling, and shell rendering.
