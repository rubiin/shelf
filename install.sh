#!/bin/sh
set -eu
# Standalone shelf installer, modeled on mise's install.sh: downloads a release
# archive, verifies its sha256, and installs the binary to ~/.local/bin/shelf.
#
#   curl -fsSL https://github.com/rubiin/shelf/releases/latest/download/install.sh | sh

#region logging setup
if [ "${SHELF_DEBUG-}" = "true" ] || [ "${SHELF_DEBUG-}" = "1" ]; then
    debug() {
        echo "$@" >&2
    }
else
    debug() {
        :
    }
fi
if [ "${SHELF_QUIET-}" = "1" ] || [ "${SHELF_QUIET-}" = "true" ]; then
    info() {
        :
    }
else
    info() {
        echo "$@" >&2
    }
fi
warn() {
    printf '%s\n' "$*" >&2
}
error() {
    echo "$@" >&2
    exit 1
}
unsupported_arch() {
    arch="$1"
    warn "unsupported architecture: $arch"
    warn ""
    warn "shelf does not provide prebuilt binaries for this platform."
    warn "If Go is available, install from source with:"
    warn "  go install shelf/cmd/shelf@latest"
    exit 1
}
#endregion

#region environment setup
get_os() {
    if [ -n "${SHELF_INSTALL_OS-}" ]; then
        echo "$SHELF_INSTALL_OS"
        return
    fi
    os="$(uname -s)"
    if [ "$os" = Darwin ]; then
        echo "Darwin"
    elif [ "$os" = Linux ]; then
        echo "Linux"
    else
        error "unsupported OS: $os"
    fi
}

get_arch() {
    if [ -n "${SHELF_INSTALL_ARCH-}" ]; then
        echo "$SHELF_INSTALL_ARCH"
        return
    fi
    arch="$(uname -m)"
    if [ "$arch" = x86_64 ] || [ "$arch" = amd64 ]; then
        echo "x86_64"
    elif [ "$arch" = aarch64 ] || [ "$arch" = arm64 ]; then
        echo "arm64"
    elif [ "$arch" = i386 ] || [ "$arch" = i486 ] || [ "$arch" = i586 ] || [ "$arch" = i686 ]; then
        echo "i386"
    else
        unsupported_arch "$arch"
    fi
}

shasum_bin() {
    if command -v sha256sum >/dev/null 2>&1; then
        echo "sha256sum"
    elif command -v shasum >/dev/null 2>&1; then
        echo "shasum -a 256"
    else
        error "shelf install requires sha256sum or shasum but neither is installed. Aborting."
    fi
}

download() {
    url="$1"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$url"
    else
        error "shelf install requires curl or wget but neither is installed. Aborting."
    fi
}

# get_checksums downloads the release's checksums file (GoReleaser names it
# either "checksums.txt" or "shelf_<version>_checksums.txt") to directory.
get_checksums() {
    base="$1"
    version="$2"
    directory="$3"
    for name in "checksums.txt" "shelf_${version}_checksums.txt"; do
        if download_file "${base}/${name}" "${directory}/${name}" 2>/dev/null; then
            echo "${directory}/${name}"
            return 0
        fi
    done
    error "release v${version} ships no checksums file"
}

resolve_version() {
    if [ -n "${SHELF_VERSION-}" ]; then
        echo "${SHELF_VERSION#v}"
        return
    fi
    tag="$(download "https://api.github.com/repos/rubiin/shelf/releases/latest" |
        sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
    [ -n "$tag" ] || error "could not determine the latest shelf release; set SHELF_VERSION"
    echo "${tag#v}"
}
#endregion

download_file() {
    url="$1"
    file="$2"
    if command -v curl >/dev/null 2>&1; then
        debug ">" curl -#fLo "$file" "$url"
        curl -#fLo "$file" "$url"
    elif command -v wget >/dev/null 2>&1; then
        debug ">" wget -qO "$file" "$url"
        stderr=$(mktemp)
        wget -O "$file" "$url" >"$stderr" 2>&1 || error "wget failed: $(cat "$stderr")"
        rm "$stderr"
    else
        error "shelf install requires curl or wget but neither is installed. Aborting."
    fi
}

# Prints the version of an installed shelf binary. The release prints
# "shelf version <version>"; prints nothing when the binary cannot report one.
installed_shelf_version() {
    bin="$1"
    if [ -x "$bin" ]; then
        version="$("$bin" --version 2>/dev/null | head -n1 | awk '{print $NF}')"
        echo "${version#v}"
    fi
}

install_shelf() {
    version="$(resolve_version)"
    version="${version#v}"
    os="${SHELF_INSTALL_OS:-$(get_os)}"
    arch="${SHELF_INSTALL_ARCH:-$(get_arch)}"
    install_path="${SHELF_INSTALL_PATH:-$HOME/.local/bin/shelf}"
    archive="shelf_${os}_${arch}.tar.gz"
    base="https://github.com/rubiin/shelf/releases/download/v${version}"

    if [ -d "$install_path" ]; then
        error "SHELF_INSTALL_PATH '$install_path' is a directory. Please set it to a file path, e.g. '$install_path/shelf'."
    fi

    # Opt-in: skip the download when the binary already at the install path is
    # the requested version.
    skip_if_exists="${SHELF_INSTALL_SKIP_IF_EXISTS-}"
    if [ "$skip_if_exists" = "1" ] || [ "$skip_if_exists" = "true" ]; then
        if [ -x "$install_path" ]; then
            existing_version="$(installed_shelf_version "$install_path")"
            if [ -n "$existing_version" ] && [ "$existing_version" = "$version" ]; then
                info "shelf: $install_path is already at version $version, skipping install"
                return 0
            fi
        fi
    fi

    info "shelf: installing shelf..."
    download_dir="$(mktemp -d)"
    extract_dir="$(mktemp -d)"
    # shellcheck disable=SC2064
    trap "rm -rf '$download_dir' '$extract_dir'" EXIT INT TERM

    debug "shelf: downloading ${base}/${archive}"
    download_file "${base}/${archive}" "${download_dir}/${archive}"
    checksums="$(get_checksums "$base" "$version" "$download_dir")"

    debug "shelf: verifying sha256"
    expected="$(awk -v name="$archive" '$2 == name || $2 == "*" name { print $1 }' "$checksums")"
    [ -n "$expected" ] || error "$(basename "$checksums") has no entry for ${archive}"
    actual="$(cd "$download_dir" && $(shasum_bin) "$archive" | awk '{print $1}')"
    [ "$expected" = "$actual" ] || error "checksum mismatch for ${archive}: got ${actual}, want ${expected}"

    tar --no-same-owner -xzf "${download_dir}/${archive}" -C "$extract_dir" shelf
    mkdir -p "$(dirname "$install_path")"
    rm -f "$install_path"
    mv "${extract_dir}/shelf" "$install_path"
    chmod 755 "$install_path"
    info "shelf: installed successfully to $install_path"
}

after_finish_help() {
    install_dir="$(dirname "$install_path")"
    case ":${PATH}:" in
    *":${install_dir}:"*) ;;
    *)
        info ""
        info "shelf: ${install_dir} is not on your PATH; add it with:"
        info "  export PATH=\"${install_dir}:\$PATH\""
        ;;
    esac
    case "${SHELL:-}" in
    */zsh)
        info ""
        info "shelf: add shelf to your shell with:"
        info "  echo 'eval \"\$(shelf source)\"' >> \"${ZDOTDIR-$HOME}/.zshrc\""
        ;;
    */bash)
        info ""
        info "shelf: add shelf to your shell with:"
        info "  echo 'eval \"\$(shelf source)\"' >> ~/.bashrc"
        ;;
    *)
        info ""
        info "shelf: run \`shelf init\` to create a configuration"
        ;;
    esac
}

install_path="${SHELF_INSTALL_PATH:-$HOME/.local/bin/shelf}"
install_shelf
if [ "${SHELF_INSTALL_HELP-}" != 0 ]; then
    after_finish_help
fi
