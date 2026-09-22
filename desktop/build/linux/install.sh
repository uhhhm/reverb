#!/usr/bin/env bash
# Install Reverb for the current user, from an extracted Reverb-linux-*.tar.gz:
#
#   Reverb/install.sh
#
# The app goes to ${XDG_DATA_HOME:-~/.local/share}/reverb-app, which the user
# owns, so the in-app updater can replace the binary later. Not reverb-desktop:
# that is where the window's WebKit keeps its storage (named for the program),
# which must survive upgrades and uninstalls. A desktop entry
# pointing at it is written to the user's applications directory, which puts
# Reverb in the launcher. No root is needed and nothing outside the user's home
# is touched.
#
# Running it again over an existing install upgrades it in place. The library,
# settings and downloads live elsewhere (~/.config/reverb, ~/Music/Reverb) and
# are never touched. Remove with uninstall.sh in the install directory.
set -euo pipefail

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA="${XDG_DATA_HOME:-$HOME/.local/share}"
DEST="$DATA/reverb-app"
APPS="$DATA/applications"
ENTRY="$APPS/reverb-desktop.desktop"

die() { echo "install.sh: $*" >&2; exit 1; }

[ "$(id -u)" != 0 ] || die "run this as your own user, not root: it installs into your home directory"
for f in reverb-desktop bin python reverb.png reverb-desktop.desktop uninstall.sh; do
  [ -e "$SRC/$f" ] || die "$SRC is not an extracted Reverb bundle (no $f)"
done
# A desktop entry has no way to write either inside a quoted Exec path.
case "$DEST" in
  *$'\n'*|*%*) die "the install path $DEST contains a newline or %; set XDG_DATA_HOME to a plainer path" ;;
esac

# A running Reverb is using the files about to be replaced. The background
# runtime counts: closing the window leaves it running to keep syncing.
running_from() {
  local exe
  for exe in /proc/[0-9]*/exe; do
    exe="$(readlink "$exe" 2>/dev/null)" || continue
    case "$exe" in "$1"/*) return 0 ;; esac
  done
  return 1
}
if [ -d "$DEST" ] && running_from "$DEST"; then
  die "Reverb is running; quit it (Reverb menu -> Quit Reverb and stop sync) and run install.sh again"
fi

# Assemble the new copy beside the old one, then swap directories, so a failure
# part-way leaves the previous install working.
NEW="$DEST.new.$$"
OLD="$DEST.old.$$"
# Interrupted between the two renames below, the previous install is put back.
cleanup() {
  rm -rf "$NEW"
  if [ ! -e "$DEST" ] && [ -e "$OLD" ]; then mv "$OLD" "$DEST"; fi
}
trap cleanup EXIT
mkdir -p "$DATA"
rm -rf "$NEW"
mkdir "$NEW"
cp -pR "$SRC/reverb-desktop" "$SRC/bin" "$SRC/python" "$SRC/reverb.png" "$SRC/uninstall.sh" "$NEW/"

if [ -e "$DEST" ]; then
  mv "$DEST" "$OLD"
fi
if ! mv "$NEW" "$DEST"; then
  [ -e "$OLD" ] && mv "$OLD" "$DEST"
  die "could not move the new copy into $DEST"
fi
rm -rf "$OLD"

mkdir -p "$APPS"
# Desktop entry values are strings with their own escapes; Exec additionally
# needs the path quoted, with the shell's special characters escaped.
entry_string() { local s=${1//\\/\\\\}; printf '%s' "$s"; }
entry_exec() {
  local s=$1
  s=${s//\\/\\\\}; s=${s//\"/\\\"}; s=${s//\`/\\\`}; s=${s//\$/\\\$}
  s="\"$s\""
  entry_string "$s"
}
exe="$DEST/reverb-desktop"
{
  while IFS= read -r line; do
    case "$line" in
      Exec=*)    printf 'Exec=%s\n' "$(entry_exec "$exe")"
                 printf 'TryExec=%s\n' "$(entry_string "$exe")" ;;
      TryExec=*) ;;
      Icon=*)    printf 'Icon=%s\n' "$(entry_string "$DEST/reverb.png")" ;;
      *)         printf '%s\n' "$line" ;;
    esac
  done < "$SRC/reverb-desktop.desktop"
} > "$ENTRY.tmp"
mv "$ENTRY.tmp" "$ENTRY"

# The launchers watch this directory themselves; these only make the change
# visible at once.
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database -q "$APPS" || true
fi
for kbuild in kbuildsycoca6 kbuildsycoca5; do
  if command -v "$kbuild" >/dev/null 2>&1; then
    "$kbuild" >/dev/null 2>&1 || true
    break
  fi
done

echo "Reverb is installed in $DEST"
echo "Open it from your app launcher, or run: \"$exe\""
echo "To remove it: \"$DEST/uninstall.sh\""
