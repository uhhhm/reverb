#!/usr/bin/env bash
# Build the Linux install bundle, dist/Reverb-linux-<arch>.tar.gz, for the host
# arch:
#
#   Reverb/reverb-desktop           the desktop binary
#   Reverb/bin/                     ffmpeg, navidrome, deno, spotdl, yt-dlp
#   Reverb/python/                  the relocatable runtime spotdl/yt-dlp run in
#   Reverb/reverb.png               the app icon
#   Reverb/reverb-desktop.desktop   desktop entry template
#   Reverb/install.sh, uninstall.sh per-user install into a writable location
#
# A tarball plus an install script rather than an AppImage: the updater swaps
# the binary in place, which a read-only image does not allow.
#
# BINARY=path packages an already-built binary (the release workflow passes the
# one it also ships as the update payload); otherwise the SPA and binary are
# built here with 'make desktop' and WAILS_TAGS. The tools come from 'make desktop-deps'.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CACHE="$SCRIPT_DIR/.cache"

VERSION="${VERSION:-dev}"
WAILS_TAGS="${WAILS_TAGS:-desktop,production,webkit2_41}"

[ "$(uname -s)" = "Linux" ] || { echo "package-linux.sh only runs on Linux" >&2; exit 1; }
case "$(uname -m)" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "unsupported arch $(uname -m)" >&2; exit 1 ;;
esac

missing=0
for t in ffmpeg navidrome deno; do
  if [ ! -x "$SCRIPT_DIR/bin/$t" ]; then
    echo "missing $SCRIPT_DIR/bin/$t" >&2
    missing=1
  fi
done
[ "$missing" = 0 ] || { echo "run 'make desktop-deps' first" >&2; exit 1; }

if [ -n "${BINARY:-}" ]; then
  [ -x "$BINARY" ] || { echo "BINARY=$BINARY is not an executable" >&2; exit 1; }
else
  echo "==> building SPA and desktop binary"
  make -C "$ROOT" desktop VERSION="$VERSION" WAILS_TAGS="$WAILS_TAGS"
  BINARY="$ROOT/dist/reverb-desktop"
fi

RUNTIME="$CACHE/runtime-linux-$ARCH"
"$SCRIPT_DIR/build-python-runtime.sh" "$RUNTIME"

echo "==> assembling the bundle"
STAGE="$ROOT/dist/stage-linux"
BUNDLE="$STAGE/Reverb"
CHECK=""
trap 'rm -rf "$STAGE" ${CHECK:+"$CHECK"}' EXIT
rm -rf "$STAGE"
mkdir -p "$BUNDLE/bin"
install -m 0755 "$BINARY" "$BUNDLE/reverb-desktop"
install -m 0755 "$SCRIPT_DIR/bin/ffmpeg" "$SCRIPT_DIR/bin/navidrome" "$SCRIPT_DIR/bin/deno" "$BUNDLE/bin/"
cp "$RUNTIME/bin/spotdl" "$RUNTIME/bin/yt-dlp" "$BUNDLE/bin/"
cp -pR "$RUNTIME/python" "$BUNDLE/python"
install -m 0644 "$ROOT/desktop/build/appicon.png" "$BUNDLE/reverb.png"
install -m 0644 "$ROOT/desktop/build/linux/reverb-desktop.desktop" "$BUNDLE/"
install -m 0755 "$ROOT/desktop/build/linux/install.sh" "$ROOT/desktop/build/linux/uninstall.sh" "$BUNDLE/"

TARBALL="$ROOT/dist/Reverb-linux-$ARCH.tar.gz"
rm -f "$TARBALL"
tar -C "$STAGE" -czf "$TARBALL" Reverb
rm -rf "$STAGE"

echo "==> verifying the extracted bundle"
CHECK="$(mktemp -d)"
tar -C "$CHECK" -xzf "$TARBALL"
"$SCRIPT_DIR/verify-bundle.sh" "$CHECK/Reverb/reverb-desktop"

echo
echo "bundle: $TARBALL ($(du -h "$TARBALL" | cut -f1))"
echo "Install: tar xzf $(basename "$TARBALL") && Reverb/install.sh"
