#!/usr/bin/env bash
# Regenerates the committed Windows resource object from build/appicon.png, so
# every Windows build — CI and a local `make desktop-windows` alike — carries
# the icon without needing this tool installed.
set -euo pipefail
cd "$(dirname "$0")/../../.."

python3 - <<'PY'
from PIL import Image

im = Image.open("desktop/build/appicon.png").convert("RGBA")
im.save(
    "desktop/build/windows/icon.ico",
    format="ICO",
    sizes=[(256, 256), (128, 128), (64, 64), (48, 48), (32, 32), (16, 16)],
)
PY

go run github.com/akavel/rsrc@v0.10.2 \
  -ico desktop/build/windows/icon.ico -arch amd64 \
  -o desktop/rsrc_windows_amd64.syso
