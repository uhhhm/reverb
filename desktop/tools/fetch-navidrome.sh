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
  windows*|mingw*|msys*) TARGETOS=windows ;;
  *) echo "unsupported OS $TARGETOS" >&2; exit 1 ;;
esac

ARCHIVE_EXT="tar.gz"
NAVIDROME_BIN="navidrome"
if [[ "$TARGETOS" == "windows" ]]; then
  if [[ "$TARGETARCH" != "amd64" ]]; then
    echo "Navidrome does not publish a Windows arm64 archive" >&2
    exit 1
  fi
  ARCHIVE_EXT="zip"
  NAVIDROME_BIN="navidrome.exe"
fi
URL="https://github.com/navidrome/navidrome/releases/download/v${VERSION}/navidrome_${VERSION}_${TARGETOS}_${TARGETARCH}.${ARCHIVE_EXT}"
echo "Fetching navidrome $VERSION for $TARGETOS/$TARGETARCH from $URL"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
curl -fsSL "$URL" -o "$TMP/navidrome.${ARCHIVE_EXT}"
mkdir -p "$TMP/nd"
if [[ "$ARCHIVE_EXT" == "zip" ]]; then
  unzip -q "$TMP/navidrome.zip" -d "$TMP/nd"
else
  tar -xzf "$TMP/navidrome.tar.gz" -C "$TMP/nd"
fi
install -m 0755 "$TMP/nd/$NAVIDROME_BIN" "$BIN_DIR/$NAVIDROME_BIN"
echo "navidrome installed to $BIN_DIR/$NAVIDROME_BIN"
