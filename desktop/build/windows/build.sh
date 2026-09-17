#!/usr/bin/env bash
# The single source of the Windows desktop build flags: `make desktop-windows`,
# the `windows` CI job and the release workflow all come here.
#
# webkit2_41 is a Linux tag and must not be passed. -H windowsgui puts the
# binary in the GUI subsystem, which is what stops a console window opening
# behind the app; verify-windows-artifact asserts it on the built PE.
#
# Building the SPA into internal/api/dist is the caller's job (`make web`, or
# the workflows' own step).
set -euo pipefail
cd "$(dirname "$0")/../../.."

GOOS=windows GOARCH=amd64 go build -tags desktop,production \
  -ldflags "-H windowsgui -X main.version=${1:-dev}" \
  -o dist/reverb-desktop.exe ./desktop
