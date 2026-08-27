#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$SCRIPT_DIR/bin"
mkdir -p "$BIN_DIR"

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

if [[ "$TARGETOS" == "linux" ]]; then
  URL="https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-${TARGETARCH}-static.tar.xz"
  echo "Fetching ffmpeg for $TARGETOS/$TARGETARCH from $URL"
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  curl -fsSL "$URL" -o "$TMP/ffmpeg.tar.xz"
  tar -xJf "$TMP/ffmpeg.tar.xz" -C "$TMP"
  FFMPEG_BIN="$(find "$TMP" -type f -name ffmpeg | head -n 1)"
  if [[ -z "$FFMPEG_BIN" ]]; then
    echo "ffmpeg binary not found in archive" >&2
    exit 1
  fi
  install -m 0755 "$FFMPEG_BIN" "$BIN_DIR/ffmpeg"
  echo "ffmpeg installed to $BIN_DIR/ffmpeg"
else
  URL="https://evermeet.cx/ffmpeg/getrelease/ffmpeg/zip"
  echo "Fetching ffmpeg for $TARGETOS/$TARGETARCH from $URL"
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  curl -fsSL "$URL" -o "$TMP/ffmpeg.zip"
  unzip -q "$TMP/ffmpeg.zip" -d "$TMP"
  FFMPEG_BIN="$TMP/ffmpeg"
  if [[ ! -f "$FFMPEG_BIN" ]]; then
    FFMPEG_BIN="$(find "$TMP" -type f -name ffmpeg | head -n 1)"
  fi
  if [[ -z "$FFMPEG_BIN" || ! -f "$FFMPEG_BIN" ]]; then
    echo "ffmpeg binary not found in archive" >&2
    exit 1
  fi
  install -m 0755 "$FFMPEG_BIN" "$BIN_DIR/ffmpeg"
  echo "ffmpeg installed to $BIN_DIR/ffmpeg"
fi
