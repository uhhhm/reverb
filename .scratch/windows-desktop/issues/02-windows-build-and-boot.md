# 02: Windows desktop builds, opens the window, and boots the backend

**What to build:** A Windows `reverb-desktop.exe` that behaves like the app on the other platforms from the first double-click: it launches without a console window, opens the Reverb window, loads the SPA, and serves the API on a loopback port. It stores its database under the Windows per-user config location and its downloads in the user's Music directory, and a second launch while the first is running is refused with a clear "another instance is running" message instead of becoming a second database writer. Windows CI compiles the tagged binary and runs the boot and smoke suites so this stays true.

**Blocked by:** 01 (isolate platform-varying desktop code behind OS-specific files)

**Status:** done

- [x] `GOOS=windows GOARCH=amd64` with the desktop build tags produces `reverb-desktop.exe`, and the exe is built as a GUI application: launching it does not open a console window.
- [ ] On Windows the app opens the Wails window and the SPA loads over the local API; the API answers on a random `127.0.0.1` port and the port reaches the frontend.
- [x] The database lands in the Windows per-user config location and `~/Music/Reverb` is created for downloads, with `REVERB_DB` and `REVERB_DOWNLOAD_DIR` overrides still winning.
- [x] A second instance started while the first holds the lock fails fast with an "another instance is running" error; the lock is released when the first exits, including after a crash or force quit.
- [x] A GitHub Actions Windows job compiles the desktop binary and runs the desktop and embedded-library Go tests on Windows as part of the standard CI checks.
- [x] A documented Windows build command exists, is the one CI uses, and works for a contributor on Windows.
- [x] `make check` and the existing macOS/Linux desktop builds are unaffected.

## Comments

Implemented. The per-OS seams from 01 now have Windows implementations:
`internal/childproc` (hidden console, own process group, below-normal priority
class, CTRL_BREAK for orderly shutdown, `TerminateProcess` for the escalation,
image-name identity check), the single-instance lock (`LockFileEx` on one byte
far past the pid text, so the losing instance can still read the owner's pid),
the background control channel (AF_UNIX, supported on Windows 10 1803+), bundled
tool naming (`Scripts`, PATHEXT), and the updater platform file.

`internal/desktop`'s path tests went through a per-OS `setConfigHome`/`setHome`
seam: they previously set `HOME`/`XDG_CONFIG_HOME`, which Windows ignores, so on
Windows they would have resolved — and written to — the developer's real
`%AppData%`.

The GUI-subsystem requirement is checked in CI by reading the PE subsystem field
of the built binary rather than by eye. The single-instance criterion gets a test
that re-execs the test binary, so the Windows job exercises the OS lock across
two processes rather than the in-process map beside it.

`Terminate` was kept working rather than stubbed. A stub would compile and even
pass the reaping tests, because those escalate to `Kill` after a grace period —
but Navidrome corrupts its index when it is cut off mid-write, and leaving the
one call the childproc contract calls "the only sanctioned first move" broken on
a platform Reverb now builds for is not a floor worth standing on. Its
CTRL_BREAK path is nonetheless not exercised in CI: a console-mode `go test`
process cannot borrow a second console, so `Terminate` there falls back to
`Kill`. Covering it properly belongs to 05, which owns the stop and orphan-reap
tests.

Not verified on a Windows machine: the window opening and the SPA loading over
the local API. Every other criterion is covered by the Windows CI job. That
checkbox stays open until someone runs the binary; 08 is the parity pass that
closes it.
