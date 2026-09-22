#!/usr/bin/env bash
# Put first-install assets at the top of a GitHub release while preserving the
# release's existing generated or hand-written notes below the managed block.
set -euo pipefail

[ $# -eq 1 ] || { echo "usage: $0 <tag>" >&2; exit 2; }
tag="$1"
version="${tag#v}"
repo="${GH_REPO:-${GITHUB_REPOSITORY:-}}"
[ -n "$repo" ] || { echo "GH_REPO or GITHUB_REPOSITORY is required" >&2; exit 2; }

start='<!-- reverb-install-assets:start -->'
end='<!-- reverb-install-assets:end -->'
body="$(gh release view "$tag" --repo "$repo" --json body --jq .body)"
body="$(awk -v start="$start" -v end="$end" '
  $0 == start { skip=1; next }
  $0 == end { skip=0; next }
  !skip { print }
' <<<"$body")"
base="https://github.com/${repo}/releases/download/${tag}"
block="$start
## Install Reverb

- **Windows (64-bit):** [Reverb-${version}-windows-amd64.zip](${base}/Reverb-${version}-windows-amd64.zip)
- **macOS Apple Silicon:** [Reverb-${version}-macOS-arm64.zip](${base}/Reverb-${version}-macOS-arm64.zip)
- **macOS Intel:** [Reverb-${version}-macOS-x86_64.zip](${base}/Reverb-${version}-macOS-x86_64.zip)
- **Linux (64-bit Intel/AMD):** [Reverb-${version}-linux-amd64.tar.gz](${base}/Reverb-${version}-linux-amd64.tar.gz)
- **Linux ARM64:** [Reverb-${version}-linux-arm64.tar.gz](${base}/Reverb-${version}-linux-arm64.tar.gz)

The lowercase `reverb-desktop-*.zip` files are automatic-update payloads; do not install them by hand.
$end"
notes="$(mktemp)"
trap 'rm -f "$notes"' EXIT
printf '%s\n\n%s\n' "$block" "${body#${body%%[!$' \t\r\n']*}}" >"$notes"
gh release edit "$tag" --repo "$repo" --notes-file "$notes"
