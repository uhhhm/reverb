# 07: Windows release artifacts in the desktop workflow

**What to build:** Publishing a release produces a Windows zip beside the four existing macOS/Linux artifacts: `reverb-desktop-<version>-windows-amd64.zip` containing a `reverb-desktop.exe` that carries the Reverb icon and is a GUI application. The zip is integrity-checked and attached to the release by the same publish job, whose artifact-count gate is updated. Contributors on Windows get a documented way to build the same binary locally.

**Blocked by:** 02 (Windows desktop builds, opens the window, and boots the backend)

**Status:** ready-for-agent

- [ ] The desktop release workflow includes a Windows amd64 build on a Windows runner using the desktop build tags.
- [ ] The artifact is named `reverb-desktop-<version>-windows-amd64.zip`, contains `reverb-desktop.exe`, and its zip integrity is verified before upload.
- [ ] The exe embeds the Reverb icon and launches without a console window.
- [ ] The publish job attaches the Windows artifact alongside the existing four and its expected-artifact gate matches the new count.
- [ ] The Windows build command is documented for local contributors and reuses the same tags and version stamping as the other platforms.
- [ ] The existing four artifacts and the macOS codesign step are unaffected.
