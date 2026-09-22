#!/usr/bin/env bash
# Build the relocatable Python runtime an install bundle ships spotDL and
# yt-dlp in, for the host OS/arch (macOS or Linux, amd64 or arm64).
#
#   build-python-runtime.sh <out-dir>
#
# <out-dir> is replaced with:
#
#   python/          a python-build-standalone interpreter with spotDL and yt-dlp
#   bin/spotdl       path-relative wrappers that run the tools from ../python
#   bin/yt-dlp
#
# Packaging copies both directories side by side (Reverb.app/Contents/Resources
# on macOS, the tarball root on Linux). The wrappers live outside python/ on
# purpose: the app's daily `python -m pip install --upgrade yt-dlp` rewrites
# python/bin/yt-dlp with an absolute shebang, which would stop working the
# moment the app is moved. Nothing pip owns is on the path the app runs.
#
# The in-tree venv from setup-python-venv.sh is the developer path; it links to
# whichever python3 built it and only works where it was made.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CACHE="$SCRIPT_DIR/.cache"

PBS_RELEASE="${PBS_RELEASE:-20260825}"
PY_VERSION="${PY_VERSION:-3.13.15}"
PY_SHORT="${PY_VERSION%.*}"
SPOTDL_VERSION="${SPOTDL_VERSION:-4.5.0}"
# Unpinned by default: the app upgrades yt-dlp daily anyway, and a stale pin
# only means the first downloads after install use an extractor YouTube broke.
YTDLP_SPEC="yt-dlp${YTDLP_VERSION:+==$YTDLP_VERSION}"

[ $# -eq 1 ] || { echo "usage: $0 <out-dir>" >&2; exit 2; }
OUT="$1"

case "$(uname -m)" in
  arm64|aarch64) PY_ARCH=aarch64 ;;
  x86_64|amd64)  PY_ARCH=x86_64 ;;
  *) echo "unsupported arch $(uname -m)" >&2; exit 1 ;;
esac
case "$(uname -s)" in
  Darwin) PY_TRIPLE="$PY_ARCH-apple-darwin" ;;
  Linux)  PY_TRIPLE="$PY_ARCH-unknown-linux-gnu" ;;
  *) echo "unsupported OS $(uname -s)" >&2; exit 1 ;;
esac

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

ASSET="cpython-$PY_VERSION+$PBS_RELEASE-$PY_TRIPLE-install_only.tar.gz"
BASE_URL="https://github.com/astral-sh/python-build-standalone/releases/download/$PBS_RELEASE"
TARBALL="$CACHE/$ASSET"
mkdir -p "$CACHE"
if [ ! -f "$TARBALL" ]; then
  echo "==> fetching python-build-standalone $PY_VERSION ($PY_TRIPLE)"
  curl -fsSL --retry 3 -o "$TARBALL.tmp" "$BASE_URL/${ASSET//+/%2B}"
  mv "$TARBALL.tmp" "$TARBALL"
fi
# Checked on every run, cached or not: a truncated download is otherwise only
# noticed as a baffling tar error, or not at all.
want="$(curl -fsSL --retry 3 "$BASE_URL/SHA256SUMS" | awk -v a="$ASSET" '$2 == a { print $1 }')"
[ -n "$want" ] || { echo "no checksum for $ASSET in the $PBS_RELEASE SHA256SUMS" >&2; exit 1; }
if [ "$(sha256 "$TARBALL")" != "$want" ]; then
  rm -f "$TARBALL"
  echo "checksum mismatch for $ASSET; removed the cached copy, run again" >&2
  exit 1
fi

# Built somewhere that is deleted before the result is checked, so a path that
# leaked into the tree fails the check below instead of working by accident.
BUILD="$(mktemp -d "$CACHE/python-build.XXXXXX")"
trap 'rm -rf "$BUILD"' EXIT
PYDIR="$BUILD/python"
tar xzf "$TARBALL" -C "$BUILD"

echo "==> installing spotDL $SPOTDL_VERSION and $YTDLP_SPEC"
export PIP_DISABLE_PIP_VERSION_CHECK=1
"$PYDIR/bin/python3" -m pip install -q --upgrade pip
"$PYDIR/bin/python3" -m pip install -q --no-warn-script-location "spotdl==$SPOTDL_VERSION" "$YTDLP_SPEC"

# pip wrote every console script with a shebang (or, for long paths, an exec
# line) naming $PYDIR. Swap that header for the one python-build-standalone
# ships its own scripts with, which finds the interpreter beside the script.
# The header's second and third lines are one Python string literal, so the rest
# of the file runs unchanged. -I keeps the script's own directory off sys.path,
# so a script can never shadow the package it launches, and ignores the user's
# PYTHONPATH and site-packages.
echo "==> making console scripts relocatable"
for f in "$PYDIR"/bin/*; do
  [ -f "$f" ] && [ ! -L "$f" ] || continue
  [ "$(head -c 2 "$f")" = "#!" ] || continue
  head -n 2 "$f" | grep -qF "$PYDIR/" || continue
  body="$(sed -n '2p' "$f" | grep -q "^'''exec'" && tail -n +4 "$f" || tail -n +2 "$f")"
  {
    printf '#!/bin/sh\n'
    printf "'''exec' \"\$(dirname -- \"\$(readlink -f -- \"\$0\")\")\"/'python%s' -I \"\$0\" \"\$@\"\n" "$PY_SHORT"
    printf "' '''\n"
    printf '%s\n' "$body"
  } > "$f.tmp"
  chmod 755 "$f.tmp"
  mv "$f.tmp" "$f"
done
# Nothing that names the build directory may remain in a shipped script.
if leaked="$(grep -lIF "$PYDIR" "$PYDIR"/bin/* 2>/dev/null)"; then
  echo "scripts still reference the build directory:" >&2
  echo "$leaked" >&2
  exit 1
fi

# The wrappers the app actually runs. -m resolves the tool from site-packages,
# so they keep working after pip replaces python/bin/yt-dlp.
mkdir -p "$BUILD/bin"
wrap() {
  cat > "$BUILD/bin/$1" <<EOF
#!/bin/sh
# Runs $1 from the Python runtime beside this directory.
DIR=\$(cd "\$(dirname "\$(readlink -f -- "\$0")")" && pwd)
exec "\$DIR/../python/bin/python$PY_SHORT" -I -m $2 "\$@"
EOF
  chmod 755 "$BUILD/bin/$1"
}
wrap spotdl spotdl
wrap yt-dlp yt_dlp

# Compile everything now, and copy with timestamps kept (here, and wherever the
# tree is copied into a bundle): a .pyc is only reused while its source's mtime
# matches, so otherwise the first run rewrites them all -- inside a signed .app
# that breaks the signature.
"$PYDIR/bin/python3" -m compileall -q -j 0 "$PYDIR/lib" >/dev/null || true

rm -rf "$OUT"
mkdir -p "$(dirname "$OUT")"
cp -pR "$BUILD" "$OUT"
rm -rf "$BUILD"
trap - EXIT

echo "==> checking the moved runtime"
"$OUT/bin/spotdl" --version
"$OUT/bin/yt-dlp" --version
"$OUT/python/bin/spotdl" --version >/dev/null
