#!/bin/sh
#
# Lay out the single npm package @spicrawl/cli from GoReleaser's build output.
#
#   scripts/build-npm.sh [VERSION]
#
# Run from the repo root after `goreleaser release` (or `make snapshot`). Copies npm/
# (launcher + package.json), the repo's README.md (the one README for GitHub
# and npm) and LICENSE to dist/npm/cli/, and every per-target binary from
# dist/spicrawl_<goos>_<goarch>*/ into dist/npm/cli/bin/<os>-<cpu>/, using
# npm's process.platform/process.arch names (darwin|linux|win32, x64|arm64).
# One package, all six binaries: no platform packages, no optional
# dependencies, no postinstall.
#
# VERSION defaults to the "version" in dist/metadata.json. This script only
# writes files; publishing is done by .github/workflows/release.yml on a v*
# tag, or can be done manually:
#
#   cd dist/npm/cli && npm publish --access public

set -eu

cd "$(dirname "$0")/.."

DIST=dist
OUT="$DIST/npm"
PKG="$OUT/cli"

die() { printf 'build-npm: %s\n' "$*" >&2; exit 1; }

command -v node >/dev/null 2>&1 || die "need node"
[ -d "$DIST" ] || die "no $DIST/ here; run goreleaser (make snapshot) first"
[ -f README.md ] || die "missing README.md (the README for GitHub and the npm page)"

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  [ -f "$DIST/metadata.json" ] || die "no VERSION given and no $DIST/metadata.json"
  VERSION=$(node -p 'require("./'"$DIST"'/metadata.json").version')
fi
VERSION="${VERSION#v}"
printf 'build-npm: version %s\n' "$VERSION" >&2

rm -rf "$OUT"
mkdir -p "$OUT"
cp -R npm "$PKG"
cp README.md LICENSE "$PKG/"

# goos goarch npm-os npm-cpu
TARGETS="linux amd64 linux x64
linux arm64 linux arm64
darwin amd64 darwin x64
darwin arm64 darwin arm64
windows amd64 win32 x64
windows arm64 win32 arm64"

printf '%s\n' "$TARGETS" | while read -r goos goarch npmos npmcpu; do
  exe=spicrawl
  [ "$goos" = windows ] && exe=spicrawl.exe

  src=""
  for d in "$DIST"/spicrawl_"${goos}"_"${goarch}"*; do
    if [ -f "$d/$exe" ]; then src="$d/$exe"; break; fi
  done
  [ -n "$src" ] || die "no binary for $goos/$goarch under $DIST/"

  dst="$PKG/bin/${npmos}-${npmcpu}"
  mkdir -p "$dst"
  cp "$src" "$dst/$exe"
  chmod 755 "$dst/$exe"
  printf 'build-npm: %s <- %s\n' "$dst/$exe" "$src" >&2
done

VERSION="$VERSION" node -e '
  const fs = require("fs");
  const file = process.argv[1];
  const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
  pkg.version = process.env.VERSION;
  fs.writeFileSync(file, JSON.stringify(pkg, null, 2) + "\n");
' "$PKG/package.json"
chmod 755 "$PKG/bin/spicrawl.js"

printf 'build-npm: wrote %s/\n' "$PKG" >&2
