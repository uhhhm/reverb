# 08: Windows parity pass and docs truth

**What to build:** A fresh Windows install of the release artifact goes through the whole product, not just boot: open the window, pair a device, search, download a track, play from the local library, close to background, reopen, and self-update to a newer tag. Any deviation from macOS/Linux is either fixed or explicitly documented. The platform docs stop saying Windows is unsupported and describe prerequisites, build, and install for Windows. ARM64 Windows is explicitly out of scope.

**Blocked by:** 03 (Windows background sync), 04 (Windows bundled tools and downloads), 05 (Windows embedded library lifecycle), 06 (Windows self-update), 07 (Windows release artifacts in the desktop workflow)

**Status:** ready-for-human

- [ ] On a clean Windows machine, the zip produced by the release workflow runs the full pass: window, pairing, search, download, local library playback, close-to-background, reopen, and self-update to a newer tag.
- [ ] The same pass succeeds after a force quit and relaunch, with no orphaned library process and no database lock errors.
- [ ] Deviations from macOS/Linux are either resolved in this ticket or recorded as known limitations in the docs.
- [x] The desktop README, the deployment reference, and the root README describe Windows support — prerequisites, build, install — instead of stating there is no Windows build, and the documented update mechanism covers the Windows artifact.
- [ ] `make check` is green and the desktop release workflow completes a dry run with all five artifacts verified.
- [x] ARM64 Windows is recorded as out of scope.

## Comments

Docs pass done: `desktop/README.md`, `docs/deployment.md` and the root README
now describe Windows as a supported desktop platform — prerequisites, build,
install-by-unzip, the published `reverb-desktop-<version>-windows-amd64.zip`
and the rename-based update mechanism — instead of saying no Windows build
exists. ARM64 Windows is recorded as out of scope in `desktop/README.md`,
`docs/deployment.md` and the root README.

Everything else here is the on-hardware pass and cannot be done from CI: the
clean-install run through window/pairing/search/download/playback/background/
reopen/self-update, the same pass after a force quit, and a real dry run of
the desktop workflow. Deviations found there are to be fixed or recorded as
known limitations before this ticket closes.
