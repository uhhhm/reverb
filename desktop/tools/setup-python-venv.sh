#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BIN_DIR="$SCRIPT_DIR/bin"
VENV_DIR="$SCRIPT_DIR/python"
mkdir -p "$BIN_DIR"

TARGETOS="${TARGETOS:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
TARGETARCH="${TARGETARCH:-$(uname -m)}"

case "$TARGETARCH" in
  x86_64|amd64) TARGETARCH=amd64 ;;
  arm64|aarch64) TARGETARCH=arm64 ;;
  *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;;
esac

case "$TARGETOS" in
  windows*|mingw*|msys*) TARGETOS=windows ;;
  linux*) TARGETOS=linux ;;
  darwin*) TARGETOS=darwin ;;
  *) echo "unsupported OS $TARGETOS" >&2; exit 1 ;;
esac

if [[ "$TARGETOS" == "windows" ]]; then
  PYTHON_BUILD="${PYTHON_STANDALONE_BUILD:-20260510}"
  PYTHON_VERSION="${PYTHON_STANDALONE_VERSION:-3.12.13}"
  case "$TARGETARCH" in
    amd64) PYTHON_ARCH=x86_64 ;;
    arm64) PYTHON_ARCH=aarch64 ;;
  esac
  ARCHIVE="cpython-${PYTHON_VERSION}+${PYTHON_BUILD}-${PYTHON_ARCH}-pc-windows-msvc-install_only_stripped.tar.gz"
  URL="https://github.com/astral-sh/python-build-standalone/releases/download/${PYTHON_BUILD}/${ARCHIVE}"
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  echo "Fetching relocatable Python $PYTHON_VERSION for Windows/$TARGETARCH from $URL"
  curl -fsSL "$URL" -o "$TMP/python.tar.gz"
  rm -rf "$VENV_DIR"
  tar -xzf "$TMP/python.tar.gz" -C "$SCRIPT_DIR"
  PYTHON="$VENV_DIR/python.exe"
else
  if ! command -v python3 >/dev/null 2>&1; then
    echo "python3 not found" >&2
    exit 1
  fi
  echo "Creating python venv at $VENV_DIR"
  python3 -m venv "$VENV_DIR"
  PYTHON="$VENV_DIR/bin/python3"
fi

"$PYTHON" -m pip install --upgrade pip
"$PYTHON" -m pip install "spotdl==4.5.0" yt-dlp

if [[ "$TARGETOS" == "windows" ]]; then
  GOOS=windows GOARCH="$TARGETARCH" go build \
    -ldflags "-s -w -H windowsgui -X main.moduleName=spotdl" \
    -o "$BIN_DIR/spotdl.exe" "$ROOT/desktop/tools/python-launcher"
  GOOS=windows GOARCH="$TARGETARCH" go build \
    -ldflags "-s -w -H windowsgui -X main.moduleName=yt_dlp" \
    -o "$BIN_DIR/yt-dlp.exe" "$ROOT/desktop/tools/python-launcher"
  SPOTDL="$BIN_DIR/spotdl.exe"
  YTDLP="$BIN_DIR/yt-dlp.exe"
else
  ln -sf "$VENV_DIR/bin/spotdl" "$BIN_DIR/spotdl"
  ln -sf "$VENV_DIR/bin/yt-dlp" "$BIN_DIR/yt-dlp" 2>/dev/null || true
  SPOTDL="$VENV_DIR/bin/spotdl"
  YTDLP="$VENV_DIR/bin/yt-dlp"
fi

echo "spotdl installed at $SPOTDL"
"$SPOTDL" --version
"$YTDLP" --version
