# 02: Windows desktop builds, opens the window, and boots the backend

**What to build:** A Windows `reverb-desktop.exe` that behaves like the app on the other platforms from the first double-click: it launches without a console window, opens the Reverb window, loads the SPA, and serves the API on a loopback port. It stores its database under the Windows per-user config location and its downloads in the user's Music directory, and a second launch while the first is running is refused with a clear "another instance is running" message instead of becoming a second database writer. Windows CI compiles the tagged binary and runs the boot and smoke suites so this stays true.

**Blocked by:** 01 (isolate platform-varying desktop code behind OS-specific files)

**Status:** ready-for-agent

- [ ] `GOOS=windows GOARCH=amd64` with the desktop build tags produces `reverb-desktop.exe`, and the exe is built as a GUI application: launching it does not open a console window.
- [ ] On Windows the app opens the Wails window and the SPA loads over the local API; the API answers on a random `127.0.0.1` port and the port reaches the frontend.
- [ ] The database lands in the Windows per-user config location and `~/Music/Reverb` is created for downloads, with `REVERB_DB` and `REVERB_DOWNLOAD_DIR` overrides still winning.
- [ ] A second instance started while the first holds the lock fails fast with an "another instance is running" error; the lock is released when the first exits, including after a crash or force quit.
- [ ] A GitHub Actions Windows job compiles the desktop binary and runs the desktop and embedded-library Go tests on Windows as part of the standard CI checks.
- [ ] A documented Windows build command exists, is the one CI uses, and works for a contributor on Windows.
- [ ] `make check` and the existing macOS/Linux desktop builds are unaffected.
