#!/usr/bin/env bash
# Build a distributable Reverb.app + zip for macOS.
#
# The app ships its own Python: the venv from setup-python-venv.sh symlinks into
# whichever python3 built it and hardcodes absolute shebangs, so it only ever
# works in-tree. build-python-runtime.sh produces the relocatable runtime and
# the path-relative spotdl/yt-dlp wrappers the bundle carries instead.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CACHE="$SCRIPT_DIR/.cache"
STAGE="$ROOT/dist/stage"
APP="$STAGE/Reverb.app"

VERSION="${VERSION:-dev}"

case "$(uname -m)" in
  arm64|aarch64) ZIP_ARCH=arm64 ;;
  x86_64)        ZIP_ARCH=x86_64 ;;
  *) echo "unsupported arch $(uname -m)" >&2; exit 1 ;;
esac

[ "$(uname -s)" = "Darwin" ] || { echo "package-mac.sh only runs on macOS" >&2; exit 1; }

for t in ffmpeg navidrome deno; do
  [ -x "$SCRIPT_DIR/bin/$t" ] || { echo "missing $SCRIPT_DIR/bin/$t — run 'make desktop-deps'" >&2; exit 1; }
done

echo "==> building SPA and desktop binary"
make -C "$ROOT" desktop VERSION="$VERSION" WAILS_TAGS=desktop,production

RUNTIME="$CACHE/runtime-darwin-$ZIP_ARCH"
"$SCRIPT_DIR/build-python-runtime.sh" "$RUNTIME"

echo "==> assembling Reverb.app"
rm -rf "$STAGE"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/bin"
cp "$ROOT/dist/reverb-desktop" "$APP/Contents/MacOS/"
cp "$SCRIPT_DIR/../build/darwin/Info.plist" "$APP/Contents/"
cp "$SCRIPT_DIR/bin/ffmpeg" "$SCRIPT_DIR/bin/navidrome" "$SCRIPT_DIR/bin/deno" "$APP/Contents/Resources/bin/"
cp -pR "$RUNTIME/python" "$APP/Contents/Resources/python"
cp "$RUNTIME/bin/spotdl" "$RUNTIME/bin/yt-dlp" "$APP/Contents/Resources/bin/"

if sips -s format icns "$SCRIPT_DIR/../build/appicon.png" \
     --out "$APP/Contents/Resources/icon.icns" >/dev/null 2>&1; then
  /usr/libexec/PlistBuddy -c "Add :CFBundleIconFile string icon" "$APP/Contents/Info.plist" >/dev/null 2>&1 || true
fi
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $VERSION" "$APP/Contents/Info.plist" >/dev/null 2>&1 || true


# Ad-hoc signature only: enough to run on Apple Silicon, but not notarized, so
# the recipient still needs right-click -> Open the first time.
echo "==> signing"
xattr -cr "$APP"
find "$APP/Contents/Resources" -type f -perm +111 -exec codesign --force -s - {} \; >/dev/null 2>&1 || true
codesign --force -s - "$APP/Contents/MacOS/reverb-desktop"
codesign --force -s - "$APP"

# Checked after signing, as shipped: every tool must resolve from inside the
# .app through the app's own lookup and run, with none of them on PATH. Running
# them must not write into the bundle (the runtime ships precompiled, with its
# timestamps kept), so the signature is checked strictly afterwards: a file the
# check left behind fails the build instead of shipping in a broken seal.
echo "==> verifying bundled tools"
"$SCRIPT_DIR/verify-bundle.sh" "$APP/Contents/MacOS/reverb-desktop"
codesign --verify --deep --strict "$APP"

ZIP="$ROOT/dist/Reverb-macOS-$ZIP_ARCH.zip"
rm -f "$ZIP"
(cd "$STAGE" && ditto -c -k --sequesterRsrc --keepParent Reverb.app "$ZIP")

echo
echo "app: $APP"
echo "zip: $ZIP ($(du -h "$ZIP" | cut -f1))"
echo "Recipient: drag to /Applications, then right-click -> Open the first time."
