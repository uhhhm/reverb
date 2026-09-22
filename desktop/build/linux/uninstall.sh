#!/usr/bin/env bash
# Remove a per-user Reverb install made by install.sh: the app directory, its
# desktop entry and its icon. The library, settings and downloads
# (~/.config/reverb, ~/Music/Reverb) are left alone; delete those by hand to
# remove every trace.
set -euo pipefail

# The install this script sits in, wherever XDG_DATA_HOME pointed when
# install.sh ran; the desktop entry is beside it in the same data directory.
DEST="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APPS="$(dirname "$DEST")/applications"
ENTRY="$APPS/reverb-desktop.desktop"

die() { echo "uninstall.sh: $*" >&2; exit 1; }

# The extracted bundle carries this script too; it is not an install, and is
# not deleted from under the user.
[ "$(basename "$DEST")" = reverb-app ] && [ -x "$DEST/reverb-desktop" ] && [ -d "$DEST/python" ] ||
  die "$DEST is not a Reverb install; run the uninstall.sh inside ~/.local/share/reverb-app"

running_from() {
  local exe
  for exe in /proc/[0-9]*/exe; do
    exe="$(readlink "$exe" 2>/dev/null)" || continue
    case "$exe" in "$1"/*) return 0 ;; esac
  done
  return 1
}
if running_from "$DEST"; then
  die "Reverb is running; quit it (Reverb menu -> Quit Reverb and stop sync) and run uninstall.sh again"
fi

rm -f "$ENTRY"
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database -q "$APPS" 2>/dev/null || true
fi
for kbuild in kbuildsycoca6 kbuildsycoca5; do
  if command -v "$kbuild" >/dev/null 2>&1; then
    "$kbuild" >/dev/null 2>&1 || true
    break
  fi
done
# The icon lives inside the install directory, so this removes it too.
rm -rf "$DEST"

echo "Reverb is removed. Your library and settings are still in ~/.config/reverb and ~/Music/Reverb."
