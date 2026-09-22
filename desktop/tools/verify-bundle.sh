#!/usr/bin/env bash
# Check an extracted install bundle: every tool resolves from inside it through
# the app's own lookup, and runs, with none of them on PATH.
#
#   verify-bundle.sh <path to the bundle's reverb-desktop binary>
#
# Runs TestInstalledBundle, and fails if the test skipped rather than passed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

[ $# -eq 1 ] || { echo "usage: $0 <bundle binary>" >&2; exit 2; }
exe="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"

out="$(cd "$ROOT" && REVERB_BUNDLE_EXE="$exe" go test -count=1 -v -run '^TestInstalledBundle$' ./desktop 2>&1)" || {
  echo "$out" >&2
  exit 1
}
grep -v '^=== RUN' <<<"$out"
grep -q -- '--- PASS: TestInstalledBundle' <<<"$out" || { echo "TestInstalledBundle did not run" >&2; exit 1; }
