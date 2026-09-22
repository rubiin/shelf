#!/usr/bin/env bash
# Compare `shelf source` with `sheldon source` using hyperfine.
#
# Both tools run against byte-identical configurations inside a temporary HOME, so
# nothing in your real config or data directory is touched.
set -euo pipefail

usage() {
    cat <<'EOF'
usage: bench-vs-sheldon.sh [OPTIONS]

Options:
  --plugins N    number of plugins in the generated config (default: 20)
  --runs N       hyperfine runs per command (default: 100)
  --warmup N     hyperfine warmup runs per command (default: 10)
  --shelf PATH   build ./cmd/shelf fresh to PATH (default: a temp dir)
  --export PATH  write hyperfine's markdown report to PATH
  -h, --help     print this help
EOF
}

plugins=20
runs=100
warmup=10
export_path=""
shelf_binary=""

while [ $# -gt 0 ]; do
    case "$1" in
        --plugins) plugins=$2; shift 2 ;;
        --runs) runs=$2; shift 2 ;;
        --warmup) warmup=$2; shift 2 ;;
        --shelf) shelf_binary=$2; shift 2 ;;
        --export) export_path=$2; shift 2 ;;
        -h | --help) usage; exit 0 ;;
        *)
            echo "error: unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

for value in "$plugins" "$runs" "$warmup"; do
    case "$value" in
        '' | *[!0-9]*)
            echo "error: --plugins, --runs, and --warmup take whole numbers, got '$value'" >&2
            exit 2
            ;;
    esac
done

for tool in hyperfine sheldon; do
    if ! command -v "$tool" > /dev/null; then
        echo "error: $tool is required; install it before benchmarking" >&2
        exit 1
    fi
done

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/shelf-bench.XXXXXX")
trap 'rm -rf "$work"' EXIT

# Keep both tools inside the sandbox, since each honors HOME and the XDG directories.
export HOME="$work/home"
export XDG_CONFIG_HOME="$work/xdg/config"
export XDG_DATA_HOME="$work/xdg/data"
export XDG_CACHE_HOME="$work/xdg/cache"
export XDG_STATE_HOME="$work/xdg/state"
mkdir -p "$HOME" "$XDG_CONFIG_HOME/sheldon" "$XDG_CONFIG_HOME/shelf" "$XDG_DATA_HOME" "$XDG_CACHE_HOME" "$XDG_STATE_HOME"

# Most plugins are local, since those select files from disk; the rest are inline.
local_plugins=$((plugins * 3 / 5))
for index in $(seq 1 "$local_plugins"); do
    directory="$work/src/p$index"
    mkdir -p "$directory"
    for name in "p$index.plugin.zsh" "p$index.extra.sh" "unused.$index.sh"; do
        echo "echo p$index/$name" > "$directory/$name"
    done
done

config="$work/plugins.toml"
{
    echo 'shell = "zsh"'
    echo
    for index in $(seq 1 "$local_plugins"); do
        printf '[plugins.p%s]\nlocal = "%s"\nuse = ["p%s.*.zsh", "p%s.*.sh"]\n' \
            "$index" "$work/src/p$index" "$index" "$index"
        # Every fourth plugin also exercises hook rendering.
        if [ $((index % 4)) -eq 0 ]; then
            printf 'hooks = { pre = "echo pre-p%s", post = "echo post-p%s" }\n' "$index" "$index"
        fi
        echo
    done
    for index in $(seq 1 $((plugins - local_plugins))); do
        printf '[plugins.inline-%s]\ninline = "echo inline-%s"\n\n' "$index" "$index"
    done
} > "$config"
cp "$config" "$XDG_CONFIG_HOME/sheldon/plugins.toml"
cp "$config" "$XDG_CONFIG_HOME/shelf/plugins.toml"

# Always benchmark the freshly built binary so the comparison never uses a
# stale build; --shelf only picks where that build is written.
if [ -z "$shelf_binary" ]; then
    shelf_binary="$work/shelf"
fi
(cd "$root" && go build -trimpath -buildvcs=false -ldflags "-s -w" -o "$shelf_binary" ./cmd/shelf)

# Lock both tools up front, so the measured commands take the hot path.
sheldon lock > /dev/null 2>&1
"$shelf_binary" lock > /dev/null 2>&1

echo "sheldon: $(sheldon --version | head -1)"
echo "shelf:   $("$shelf_binary" --version), $(stat -c %s "$shelf_binary" 2> /dev/null || stat -f %z "$shelf_binary") bytes"
echo "config:  $plugins plugins ($local_plugins local, $((plugins - local_plugins)) inline)"
echo "output:  sheldon $(sheldon source | wc -l) lines, shelf $("$shelf_binary" source | wc -l) lines"
if ! diff <(sheldon source) <("$shelf_binary" source) > "$work/output.diff"; then
    echo "warning: rendered output differs, so the comparison is not like-for-like:" >&2
    head -20 "$work/output.diff" >&2
fi

hyperfine_args=(--warmup "$warmup" --runs "$runs")
if [ -n "$export_path" ]; then
    hyperfine_args+=(--export-markdown "$export_path")
fi

# The third argument is the shell hyperfine runs the command with; `none` measures the
# process itself, while the source line a shell evaluates needs a real shell.
compare() {
    local label=$1 sheldon_command=$2 shelf_command=$3 shell=$4
    echo
    echo "== $label =="
    hyperfine "${hyperfine_args[@]}" --shell="$shell" \
        -n "sheldon: $label" "$sheldon_command" \
        -n "shelf: $label" "$shelf_command"
}

compare "startup" "sheldon version" "$shelf_binary --version" none
compare "source" "sheldon source" "$shelf_binary source" none
export SHELF_BENCH_BINARY="$shelf_binary"
compare "eval in bash" 'eval "$(sheldon source)"' 'eval "$($SHELF_BENCH_BINARY source)"' bash
