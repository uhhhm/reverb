#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$SCRIPT_DIR/bin"
mkdir -p "$BIN_DIR"

TARGETOS="${TARGETOS:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
TARGETARCH="${TARGETARCH:-$(uname -m)}"

case "$TARGETARCH" in
  x86_64|amd64) ARCH=x86_64 ;;
  arm64|aarch64) ARCH=aarch64 ;;
  *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;;
esac

case "$TARGETOS" in
  linux*) OS=unknown-linux-gnu ;;
  darwin*) OS=apple-darwin ;;
  windows*|mingw*|msys*) OS=pc-windows-msvc ;;
  *) echo "unsupported OS $TARGETOS" >&2; exit 1 ;;
esac

FILENAME="deno-${ARCH}-${OS}.zip"
URL="https://github.com/denoland/deno/releases/latest/download/${FILENAME}"
echo "Fetching deno for $TARGETOS/$TARGETARCH ($FILENAME) from $URL"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
curl -fsSL "$URL" -o "$TMP/deno.zip"
unzip -q "$TMP/deno.zip" -d "$TMP"
DENO_BIN="deno"
if [[ "$OS" == "pc-windows-msvc" ]]; then
  DENO_BIN="deno.exe"
fi
install -m 0755 "$TMP/$DENO_BIN" "$BIN_DIR/$DENO_BIN"
echo "deno installed to $BIN_DIR/$DENO_BIN"
