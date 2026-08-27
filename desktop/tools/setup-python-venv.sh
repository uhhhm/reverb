#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$SCRIPT_DIR/bin"
VENV_DIR="$SCRIPT_DIR/python"
mkdir -p "$BIN_DIR"

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 not found" >&2
  exit 1
fi

echo "Creating python venv at $VENV_DIR"
python3 -m venv "$VENV_DIR"

# shellcheck disable=SC1091
"$VENV_DIR/bin/pip" install --upgrade pip
"$VENV_DIR/bin/pip" install "spotdl==4.5.0" yt-dlp

ln -sf "$VENV_DIR/bin/spotdl" "$BIN_DIR/spotdl"
ln -sf "$VENV_DIR/bin/yt-dlp" "$BIN_DIR/yt-dlp" 2>/dev/null || true

echo "spotdl installed at $VENV_DIR/bin/spotdl (symlinked to $BIN_DIR/spotdl)"
"$VENV_DIR/bin/spotdl" --version || true
"$VENV_DIR/bin/yt-dlp" --version || true
