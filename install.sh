#!/bin/sh
#
# Install the Spicrawl CLI (the spicrawl command).
#
#   curl -fsSL https://spicrawl.com/install.sh | sh
#
# Environment:
#   SPICRAWL_INSTALL_DIR   where to put the binary (default: ~/.local/bin)
#   SPICRAWL_VERSION       a release tag such as v1.2.3 (default: latest)
#   SPICRAWL_REPO          GitHub owner/repo hosting the releases
#   SPICRAWL_DOWNLOAD_URL  base URL holding the archives and checksums.txt
#                        (a mirror; overrides SPICRAWL_REPO and SPICRAWL_VERSION)
#
# Non-interactive: never prompts, never uses sudo. The archive's SHA-256 is
# checked against the release's checksums.txt before anything is installed.
# Exits non-zero on any failure.

set -eu

# The GitHub repo that hosts the release archives. Must match
# release.github in .goreleaser.yaml.
SPICRAWL_REPO="${SPICRAWL_REPO:-Spicrawl/cli}"
SPICRAWL_VERSION="${SPICRAWL_VERSION:-latest}"
INSTALL_DIR="${SPICRAWL_INSTALL_DIR:-${HOME}/.local/bin}"

say() { printf 'spicrawl-install: %s\n' "$*" >&2; }
die() { say "error: $*"; exit 1; }

# --- platform ---------------------------------------------------------------

os=$(uname -s)
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  MINGW* | MSYS* | CYGWIN*)
    die "Windows: download spicrawl_windows_amd64.zip from https://github.com/${SPICRAWL_REPO}/releases, or run: npx @spicrawl/cli" ;;
  *) die "unsupported OS: $os" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported architecture: $arch" ;;
esac

# A shell running under Rosetta reports x86_64 on Apple silicon; prefer native.
if [ "$os" = darwin ] && [ "$arch" = amd64 ]; then
  if [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
    arch=arm64
  fi
fi

archive="spicrawl_${os}_${arch}.tar.gz"
if [ -n "${SPICRAWL_DOWNLOAD_URL:-}" ]; then
  base="${SPICRAWL_DOWNLOAD_URL%/}"
elif [ "$SPICRAWL_VERSION" = latest ]; then
  base="https://github.com/${SPICRAWL_REPO}/releases/latest/download"
else
  base="https://github.com/${SPICRAWL_REPO}/releases/download/${SPICRAWL_VERSION}"
fi

# --- tools ------------------------------------------------------------------

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  die "need curl or wget"
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
elif command -v openssl >/dev/null 2>&1; then
  sha256() { openssl dgst -sha256 "$1" | sed 's/^.*= *//'; }
else
  die "need sha256sum, shasum or openssl to verify the download"
fi

command -v tar >/dev/null 2>&1 || die "need tar"

# --- download and verify ----------------------------------------------------

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t spicrawl)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT TERM

say "downloading ${base}/${archive}"
fetch "${base}/${archive}" "${tmp}/${archive}" || die "download failed: ${base}/${archive}"
fetch "${base}/checksums.txt" "${tmp}/checksums.txt" || die "download failed: ${base}/checksums.txt"

want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "${tmp}/checksums.txt")
[ -n "$want" ] || die "${archive} is not listed in checksums.txt"
got=$(sha256 "${tmp}/${archive}")
[ "$want" = "$got" ] || die "checksum mismatch for ${archive}: expected ${want}, got ${got}"
say "checksum ok"

tar -xzf "${tmp}/${archive}" -C "$tmp" spicrawl || die "archive does not contain the spicrawl binary"

# --- install ----------------------------------------------------------------

mkdir -p "$INSTALL_DIR" || die "cannot create ${INSTALL_DIR} (set SPICRAWL_INSTALL_DIR)"
# Copy next to the target and rename, so a running spicrawl is never half-written.
cp "${tmp}/spicrawl" "${INSTALL_DIR}/.spicrawl.tmp.$$" || die "cannot write to ${INSTALL_DIR} (set SPICRAWL_INSTALL_DIR)"
chmod 755 "${INSTALL_DIR}/.spicrawl.tmp.$$"
mv -f "${INSTALL_DIR}/.spicrawl.tmp.$$" "${INSTALL_DIR}/spicrawl"

say "installed ${INSTALL_DIR}/spicrawl"
"${INSTALL_DIR}/spicrawl" version >&2 2>/dev/null || true

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    say "${INSTALL_DIR} is not on your PATH. Add it with:"
    say "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac
