#!/usr/bin/env bash
# Build a distributable Reverb.app + zip for macOS.
#
# The app ships its own Python: the venv from setup-python-venv.sh symlinks into
# whichever python3 built it and hardcodes absolute shebangs, so it only ever
# works in-tree. Here spotDL and yt-dlp are installed into a relocatable
# python-build-standalone runtime and their entry points rewritten as
# path-relative wrappers.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CACHE="$SCRIPT_DIR/.cache"
STAGE="$ROOT/dist/stage"
APP="$STAGE/Reverb.app"

VERSION="${VERSION:-dev}"
PBS_RELEASE="${PBS_RELEASE:-20260825}"
PY_VERSION="${PY_VERSION:-3.13.15}"
PY_SHORT="${PY_VERSION%.*}"
SPOTDL_VERSION="${SPOTDL_VERSION:-4.5.0}"

case "$(uname -m)" in
  arm64|aarch64) PY_ARCH=aarch64; ZIP_ARCH=arm64 ;;
  x86_64)        PY_ARCH=x86_64;  ZIP_ARCH=x86_64 ;;
  *) echo "unsupported arch $(uname -m)" >&2; exit 1 ;;
esac

[ "$(uname -s)" = "Darwin" ] || { echo "package-mac.sh only runs on macOS" >&2; exit 1; }

for t in ffmpeg navidrome deno; do
  [ -x "$SCRIPT_DIR/bin/$t" ] || { echo "missing $SCRIPT_DIR/bin/$t — run 'make desktop-deps'" >&2; exit 1; }
done

echo "==> building SPA and desktop binary"
make -C "$ROOT" web
(cd "$ROOT" && go build -tags desktop,production \
  -ldflags "-X main.version=$VERSION" -o dist/reverb-desktop ./desktop)

PY_TARBALL="$CACHE/cpython-$PY_VERSION-$PY_ARCH.tar.gz"
if [ ! -f "$PY_TARBALL" ]; then
  echo "==> fetching python-build-standalone $PY_VERSION"
  mkdir -p "$CACHE"
  curl -fsSL -o "$PY_TARBALL.tmp" \
    "https://github.com/astral-sh/python-build-standalone/releases/download/$PBS_RELEASE/cpython-$PY_VERSION%2B$PBS_RELEASE-$PY_ARCH-apple-darwin-install_only.tar.gz"
  mv "$PY_TARBALL.tmp" "$PY_TARBALL"
fi

PYDIR="$CACHE/python"
echo "==> installing spotDL $SPOTDL_VERSION and yt-dlp"
rm -rf "$PYDIR"
tar xzf "$PY_TARBALL" -C "$CACHE"
"$PYDIR/bin/python3" -m pip install -q --upgrade pip
"$PYDIR/bin/python3" -m pip install -q "spotdl==$SPOTDL_VERSION" yt-dlp

# Console scripts carry an absolute shebang into $PYDIR. Move each script aside
# and replace it with a wrapper that resolves the interpreter relative to itself,
# so the tree works from anywhere. -P keeps the script's own directory off
# sys.path: without it bin/_scripts/spotdl.py shadows the spotdl package.
echo "==> making entry points relocatable"
(
  cd "$PYDIR/bin" && mkdir -p _scripts
  for f in *; do
    [ -f "$f" ] && [ ! -L "$f" ] || continue
    head -1 "$f" | grep -q "^#\!$PYDIR" || continue
    tail -n +2 "$f" > "_scripts/$f.py"
    printf '#!/bin/sh\nDIR=$(cd "$(dirname "$(readlink -f "$0")")" && pwd)\nexec "$DIR/python%s" -P "$DIR/_scripts/%s.py" "$@"\n' \
      "$PY_SHORT" "$f" > "$f"
    chmod +x "$f"
  done
)

echo "==> assembling Reverb.app"
rm -rf "$STAGE"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/bin"
cp "$ROOT/dist/reverb-desktop" "$APP/Contents/MacOS/"
cp "$SCRIPT_DIR/../build/darwin/Info.plist" "$APP/Contents/"
cp "$SCRIPT_DIR/bin/ffmpeg" "$SCRIPT_DIR/bin/navidrome" "$SCRIPT_DIR/bin/deno" "$APP/Contents/Resources/bin/"
cp -R "$PYDIR" "$APP/Contents/Resources/python"
ln -sf ../python/bin/spotdl "$APP/Contents/Resources/bin/spotdl"
ln -sf ../python/bin/yt-dlp "$APP/Contents/Resources/bin/yt-dlp"

if sips -s format icns "$SCRIPT_DIR/../build/appicon.png" \
     --out "$APP/Contents/Resources/icon.icns" >/dev/null 2>&1; then
  /usr/libexec/PlistBuddy -c "Add :CFBundleIconFile string icon" "$APP/Contents/Info.plist" >/dev/null 2>&1 || true
fi
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $VERSION" "$APP/Contents/Info.plist" >/dev/null 2>&1 || true

echo "==> verifying bundled tools"
"$APP/Contents/Resources/bin/spotdl" --version
"$APP/Contents/Resources/bin/yt-dlp" --version
"$APP/Contents/Resources/bin/ffmpeg" -version | head -1

# Ad-hoc signature only: enough to run on Apple Silicon, but not notarized, so
# the recipient still needs right-click -> Open the first time.
echo "==> signing"
xattr -cr "$APP"
find "$APP/Contents/Resources" -type f -perm +111 -exec codesign --force -s - {} \; >/dev/null 2>&1 || true
codesign --force -s - "$APP/Contents/MacOS/reverb-desktop"
codesign --force -s - "$APP"

ZIP="$ROOT/dist/Reverb-macOS-$ZIP_ARCH.zip"
rm -f "$ZIP"
(cd "$STAGE" && ditto -c -k --sequesterRsrc --keepParent Reverb.app "$ZIP")

echo
echo "app: $APP"
echo "zip: $ZIP ($(du -h "$ZIP" | cut -f1))"
echo "Recipient: drag to /Applications, then right-click -> Open the first time."
