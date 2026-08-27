#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$SCRIPT_DIR/bin"
mkdir -p "$BIN_DIR"

VERSION="${NAVIDROME_VERSION:-0.62.0}"
TARGETOS="${TARGETOS:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
TARGETARCH="${TARGETARCH:-$(uname -m)}"

case "$TARGETARCH" in
  x86_64|amd64) TARGETARCH=amd64 ;;
  arm64|aarch64) TARGETARCH=arm64 ;;
  *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;;
esac

case "$TARGETOS" in
  linux*) TARGETOS=linux ;;
  darwin*) TARGETOS=darwin ;;
  *) echo "unsupported OS $TARGETOS" >&2; exit 1 ;;
esac

URL="https://github.com/navidrome/navidrome/releases/download/v${VERSION}/navidrome_${VERSION}_${TARGETOS}_${TARGETARCH}.tar.gz"
echo "Fetching navidrome $VERSION for $TARGETOS/$TARGETARCH from $URL"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
curl -fsSL "$URL" -o "$TMP/navidrome.tar.gz"
mkdir -p "$TMP/nd"
tar -xzf "$TMP/navidrome.tar.gz" -C "$TMP/nd"
install -m 0755 "$TMP/nd/navidrome" "$BIN_DIR/navidrome"
echo "navidrome installed to $BIN_DIR/navidrome"
