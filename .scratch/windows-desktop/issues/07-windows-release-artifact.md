# 07: Windows release artifacts in the desktop workflow

**What to build:** Publishing a release produces a Windows zip beside the four existing macOS/Linux artifacts: `reverb-desktop-<version>-windows-amd64.zip` containing a `reverb-desktop.exe` that carries the Reverb icon and is a GUI application. The zip is integrity-checked and attached to the release by the same publish job, whose artifact-count gate is updated. Contributors on Windows get a documented way to build the same binary locally.

**Blocked by:** 02 (Windows desktop builds, opens the window, and boots the backend)

**Status:** done

- [x] The desktop release workflow includes a Windows amd64 build on a Windows runner using the desktop build tags.
- [x] The artifact is named `reverb-desktop-<version>-windows-amd64.zip`, contains `reverb-desktop.exe`, and its zip integrity is verified before upload.
- [x] The exe embeds the Reverb icon and launches without a console window.
- [x] The publish job attaches the Windows artifact alongside the existing four and its expected-artifact gate matches the new count.
- [x] The Windows build command is documented for local contributors and reuses the same tags and version stamping as the other platforms.
- [x] The existing four artifacts and the macOS codesign step are unaffected.

## Comments

`desktop.yml` gained a `build-windows` job on `windows-2025` rather than a
matrix row, since the SPA embed and packaging differ from the bash matrix step.
It builds through `desktop/build/windows/build.sh`, which is now the single
source of the Windows build flags — `make desktop-windows` and the CI `windows`
job run it too — packages `reverb-desktop-<version>-windows-amd64.zip`, and
raises the publish gate to five artifacts.

Both assertions go through `desktop/tools/verify-windows-artifact`, a small Go
program rather than PowerShell: `-exe` reads PE subsystem 2 and the
root of the `.rsrc` directory for `RT_ICON`/`RT_GROUP_ICON`, and `-zip` reads the single
entry to the end so `archive/zip` checks its CRC. Both were exercised locally
against a cross-compiled binary, including the negative cases — a
console-subsystem exe, a build with the `.syso` removed, and a zip with a
flipped byte all fail.

The icon comes from a committed `desktop/rsrc_windows_amd64.syso`, so every
Windows build carries it with no extra tooling;
`desktop/build/windows/make-icon-resource.sh` regenerates it from
`build/appicon.png`.

The dry run of the workflow itself is ticket 08's box, not this one.
