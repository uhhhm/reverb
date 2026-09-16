# 08: Windows parity pass and docs truth

**What to build:** A fresh Windows install of the release artifact goes through the whole product, not just boot: open the window, pair a device, search, download a track, play from the local library, close to background, reopen, and self-update to a newer tag. Any deviation from macOS/Linux is either fixed or explicitly documented. The platform docs stop saying Windows is unsupported and describe prerequisites, build, and install for Windows. ARM64 Windows is explicitly out of scope.

**Blocked by:** 03 (Windows background sync), 04 (Windows bundled tools and downloads), 05 (Windows embedded library lifecycle), 06 (Windows self-update), 07 (Windows release artifacts in the desktop workflow)

**Status:** ready-for-agent

- [ ] On a clean Windows machine, the zip produced by the release workflow runs the full pass: window, pairing, search, download, local library playback, close-to-background, reopen, and self-update to a newer tag.
- [ ] The same pass succeeds after a force quit and relaunch, with no orphaned library process and no database lock errors.
- [ ] Deviations from macOS/Linux are either resolved in this ticket or recorded as known limitations in the docs.
- [ ] The desktop README, the deployment reference, and the root README describe Windows support — prerequisites, build, install — instead of stating there is no Windows build, and the documented update mechanism covers the Windows artifact.
- [ ] `make check` is green and the desktop release workflow completes a dry run with all five artifacts verified.
- [ ] ARM64 Windows is recorded as out of scope.
