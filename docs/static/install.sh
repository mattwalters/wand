#!/bin/sh
# wand install script — https://wandcli.com/install.sh
#
#   curl -fsSL https://wandcli.com/install.sh | sh
#
# Downloads the goreleaser-built release archive matching this platform,
# verifies it against checksums.txt, and installs the wand binary.
#
# Env vars:
#   WAND_VERSION      pin a release tag (e.g. v0.1.0) instead of latest
#   WAND_INSTALL_DIR  install to this directory instead of the default
#
# POSIX sh only — this gets piped into whatever /bin/sh is (dash on Debian
# and Ubuntu), not bash.
set -eu

repo="mattwalters/wand"

say() {
    printf '%s\n' "$*"
}

err() {
    printf 'wand install: %s\n' "$*" >&2
    exit 1
}

command -v curl >/dev/null 2>&1 || err "curl is required but not found"
command -v tar >/dev/null 2>&1 || err "tar is required but not found"

# --- OS detection ------------------------------------------------------
case "$(uname -s)" in
    Darwin) os=Darwin ;;
    Linux) os=Linux ;;
    *)
        err "unsupported OS ($(uname -s)) — wand's curl installer covers macOS and Linux only. Windows binaries are on https://github.com/${repo}/releases"
        ;;
esac

# --- Arch detection ------------------------------------------------------
case "$(uname -m)" in
    x86_64 | amd64) arch=x86_64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *)
        err "unsupported architecture ($(uname -m)) — see https://github.com/${repo}/releases"
        ;;
esac

# --- Version resolution ------------------------------------------------
# api.github.com's unauthenticated rate limit (60/hr) is too easy for a
# popular one-liner to hit, so latest is resolved from the redirect the
# releases page itself gives, not the API.
if [ "${WAND_VERSION:-}" != "" ]; then
    tag="$WAND_VERSION"
else
    tag=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${repo}/releases/latest") \
        || err "could not resolve the latest release"
    tag=${tag##*/tag/}
    [ -n "$tag" ] || err "could not resolve the latest release"
fi

version=${tag#v}

workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT INT TERM

archive="wand_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/${repo}/releases/download/${tag}"

say "Downloading ${archive} (${tag})..."
curl -fsSL -o "${workdir}/${archive}" "${base_url}/${archive}" \
    || err "failed to download ${base_url}/${archive} — does release ${tag} exist for this platform?"
curl -fsSL -o "${workdir}/checksums.txt" "${base_url}/checksums.txt" \
    || err "failed to download checksums.txt for ${tag}"

# --- Checksum verification ------------------------------------------------
say "Verifying checksum..."
(
    cd "$workdir"
    grep " ${archive}\$" checksums.txt >checksum.line 2>/dev/null || exit 1
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum -c checksum.line >/dev/null
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 -c checksum.line >/dev/null
    else
        exit 1
    fi
) || err "checksum verification failed for ${archive} — the download does not match checksums.txt"

# --- Unpack ------------------------------------------------------------
tar -xzf "${workdir}/${archive}" -C "$workdir"
[ -f "${workdir}/wand" ] || err "archive did not contain a wand binary"
chmod +x "${workdir}/wand"

# --- Install dir resolution ------------------------------------------------
if [ "${WAND_INSTALL_DIR:-}" != "" ]; then
    install_dir="$WAND_INSTALL_DIR"
elif [ -w /usr/local/bin ]; then
    install_dir=/usr/local/bin
else
    install_dir="$HOME/.local/bin"
fi
mkdir -p "$install_dir"

previous=$(command -v wand 2>/dev/null || true)

mv "${workdir}/wand" "${install_dir}/wand"
say "Installed wand ${tag} to ${install_dir}/wand"

# --- PATH check ------------------------------------------------------
case ":$PATH:" in
    *":${install_dir}:"*) ;;
    *)
        say "warning: ${install_dir} is not on your PATH. Add it with:"
        say "    export PATH=\"${install_dir}:\$PATH\""
        ;;
esac

# --- Cask-collision check ------------------------------------------------
# A Homebrew-managed wand, or a dev build, is a copy this install must not
# silently shadow or be shadowed by without saying so.
resolved=$(command -v wand 2>/dev/null || true)
if [ -n "$previous" ] && [ "$previous" != "${install_dir}/wand" ]; then
    if [ "$resolved" = "${install_dir}/wand" ]; then
        say "note: your PATH now resolves wand to the copy just installed. $previous is still there, but shadowed."
    else
        say "warning: installed to ${install_dir}/wand, but your PATH still resolves wand to $previous — that copy runs until you fix PATH order or use the full path."
    fi
fi

say "Run 'wand version' to confirm."
