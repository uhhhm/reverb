# Reverb Desktop (Wails)

Wails v2 desktop wrapper for Reverb. Sibling to `cmd/reverb`, shares `internal/*`.

## Prerequisites

- Go 1.23+
- Node 22+
- Wails v2 (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
- Linux deps: `libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev pkg-config`
- macOS: Xcode
- Windows: Windows 10 1803 or newer and the WebView2 runtime (preinstalled on
  Windows 11 and current Windows 10). No C toolchain is needed — the Windows
  build is pure Go.

## Commands

```bash
make desktop        # build dist/reverb-desktop (requires web build)
make desktop-dev    # wails dev -projectdir ./desktop (hot reload via Vite :5173)
make desktop-deps   # fetch ffmpeg, Navidrome, Deno, Python, spotDL and yt-dlp
make desktop-windows # cross-compile dist/reverb-desktop.exe from macOS or Linux
make package-linux  # dist/Reverb-linux-<arch>.tar.gz install bundle (needs desktop-deps)
make package-mac    # dist/stage/Reverb.app and dist/Reverb-macOS-<arch>.zip (on macOS)
```

On Windows, build the SPA into `internal/api/dist` (PowerShell, from the
repository root) and then run the same script CI and the release workflow run:

```powershell
npm ci --prefix web
npm run build --prefix web
if (Test-Path internal/api/dist) { Remove-Item -Recurse -Force internal/api/dist }
Copy-Item -Recurse web/dist internal/api/dist
bash desktop/build/windows/build.sh dev
```

`build.sh` is the one place the Windows build flags live. `-H windowsgui` puts
the binary in the GUI subsystem; without it Windows opens a console window
behind the app on every launch. `webkit2_41` is a Linux tag and must not be
passed. `go run ./desktop/tools/verify-windows-artifact -exe
dist/reverb-desktop.exe` checks both from the built PE, and with `-zip` checks a
release archive holds exactly `reverb-desktop.exe` and decompresses clean.

Config is in `desktop/wails.json` — frontend `../web`, build `npm run build`, dev server `http://localhost:5173`, fallback `index.html` for BrowserRouter.

Build assets: `desktop/build/appicon.png` (from `web/public/logo.png`), `build/darwin/Info.plist`, `build/linux/reverb-desktop.desktop`, `build/windows/icon.ico`. The icon reaches the Windows exe through the committed `desktop/rsrc_windows_amd64.syso`, which the Go linker picks up on any Windows build; regenerate it from the PNG with `desktop/build/windows/make-icon-resource.sh`. CI fails the build if the icon stops being linked in.
Bundled tools are fetched into `desktop/tools/` (gitignored) per arch.

Data dirs (desktop mode): DB via `internal/desktop.ResolveDesktopDB()` — XDG on
Linux, Application Support on macOS, `%AppData%\reverb` on Windows — and
downloads in `Music/Reverb` under the user's home directory. `REVERB_DB` and
`REVERB_DOWNLOAD_DIR` override both on every platform.

One instance owns a data directory at a time. The second launch fails with
"another instance is running" rather than becoming a second writer on the same
database; the lock is held by the operating system on an open file, so a crash
or a force quit releases it.

## Install bundles and the bundled Python

The in-tree `desktop/tools/python` venv from `make desktop-deps` links to the
`python3` that built it and only works where it was made. Install bundles carry
a relocatable runtime instead, built by `desktop/tools/build-python-runtime.sh`
for the host OS and arch (macOS and Linux, amd64 and arm64): a
python-build-standalone interpreter with spotDL and yt-dlp in `python/`, and
path-relative `bin/spotdl` and `bin/yt-dlp` wrappers that run them with
`python -I -m`, so the script's own directory and the user's `PYTHONPATH` never
reach `sys.path`. `PY_VERSION`, `PBS_RELEASE`, `SPOTDL_VERSION` and
`YTDLP_VERSION` override the pins. The wrappers sit outside `python/` because
the daily yt-dlp upgrade rewrites `python/bin/yt-dlp` with an absolute shebang.
The step builds in a scratch directory, moves the result and runs the tools from
the new location, so a leaked build path fails the build.

Desktop startup exports that interpreter as `REVERB_YTDLP_PYTHON`, and the daily
`pip install --upgrade yt-dlp` runs only there. Without a bundled interpreter
the upgrade is skipped; it never touches a `python3` on `PATH`.

`make package-linux` produces `Reverb-linux-<arch>.tar.gz` holding
`reverb-desktop`, `bin/` (ffmpeg, Navidrome, Deno and the wrappers), `python/`,
the icon, a desktop entry template and `install.sh`/`uninstall.sh`. It checks
the extracted tarball with `desktop/tools/verify-bundle.sh`, which runs
`TestInstalledBundle`: every tool must resolve from inside the bundle through
the app's own lookup and run, with none of them on `PATH`. `BINARY=path`
packages an already-built binary instead of building one.

`Reverb/install.sh` installs per user, without root, into
`~/.local/share/reverb-app` (`$XDG_DATA_HOME` is honoured), and writes
`~/.local/share/applications/reverb-desktop.desktop` with absolute `Exec` and
`Icon` paths, so Reverb appears in the KDE and GNOME launchers. The install
directory is not `reverb-desktop`: WebKitGTK keeps the window's storage in
`~/.local/share/<program name>`, and the program name is pinned to
`reverb-desktop` (it is also the Wayland app ID the desktop entry is matched
by). Running `install.sh` again upgrades in place by swapping the whole
directory; it refuses while Reverb, including its background runtime, is
running. `uninstall.sh` in the install directory removes the app, the desktop
entry and the icon. Neither touches `~/.config/reverb` or `~/Music/Reverb`. The
updater replaces only `reverb-desktop` inside the install, which the user owns;
the tools beside it stay. An AppImage is not offered because the updater cannot
swap a binary inside its read-only image.

On the NVIDIA driver, WebKitGTK's DMA-BUF renderer cannot allocate its buffers
and the window stays white, so the app sets `WEBKIT_DISABLE_DMABUF_RENDERER=1`
when the `nvidia` kernel module is loaded, unless the variable is already set.

## Windows

`reverb-desktop.exe` opens the window, serves the SPA and answers the API on a
random `127.0.0.1` port, stores its database under `%AppData%\reverb` and its
downloads under `%USERPROFILE%\Music\Reverb`, and refuses a second instance
while the first holds the lock. The `windows` CI job builds the binary and runs
the desktop, desktop-paths, embedded-library and child-process tests on every
push.

Every per-OS seam has a Windows implementation — the single-instance lock, the
background control channel, child-process handling, bundled-tool lookup and the
updater's install step. Windows CI exercises the boot and background-control
round trips, including shutdown acknowledgement after the database lock is
released and cleanup when background startup stalls. It also fetches and smoke
tests the Windows ffmpeg, Navidrome, Deno, Python, spotDL and yt-dlp bundle.
`desktop.yml` publishes `reverb-desktop-<version>-windows-amd64.zip`, which is
the asset the updater picks up.

ARM64 Windows is out of scope: there is no arm64 artifact and no arm64 bundled
tool set, and an amd64 build runs under emulation without the download stack
being exercised there.

Installing is unzipping: put `reverb-desktop.exe` wherever it should live and
run it. There is no installer and nothing is written outside `%AppData%\reverb`
and `%USERPROFILE%\Music\Reverb`. Windows SmartScreen warns on first launch
because the binary is unsigned — **More info** → **Run anyway**.

On Windows, `make desktop-deps` uses a relocatable python-build-standalone
runtime rather than a machine-wide Python. The `spotdl.exe` and `yt-dlp.exe`
launchers locate that runtime relative to themselves, so the whole `bin` plus
`python` tree can move into an installed app unchanged. Desktop startup exports
that interpreter as `REVERB_YTDLP_PYTHON`; the daily yt-dlp upgrade therefore
updates the bundled environment and never requires `python3` on `PATH`.

## Background sync

Closing the window keeps sync running by default. After shutting down the window's
backend, Reverb launches the same executable with `--background`, without starting
Wails or a webview. Reopening Reverb stops that process and waits for it to release
the database and bundled library before starting the desktop backend. Only one
backend owns the database at a time. Playback stops when the window closes;
pending downloads and sync continue in the background.

In **P2P → Background sync**, turn off **Keep syncing after closing the window**
to restore close-to-quit behaviour. **Quit Reverb and stop sync** (also in the
Reverb menu, Cmd/Ctrl+Q) stops everything. To stop an already-backgrounded copy
without opening the window, run `reverb-desktop --stop-background` (with the same
`--db` or `REVERB_DB` override, if used).

No login item or system service is installed. Open Reverb once after signing in
to make sync available; both computers must be awake and reachable. A crash or
force quit does not automatically restart Reverb.

The background process runs at reduced CPU priority. It reuses the existing
incremental metadata sync and file scan, which skips hashing unchanged files;
it also runs the bundled library and download workers. There is no hidden
webview, UI polling, or additional copy of the backend. Closing/reopening entails
a brief backend restart and device reconnection.

Background shutdown controls use a filesystem Unix socket next to the database;
they are not exposed on the HTTP API and requests carrying a browser `Origin`
are refused. Unix restricts the socket directory to `0700` and the socket to
`0600`; Windows inherits the per-user AppData directory's ACL. Existing
paired-device authentication and HTTP Host/Origin guards still apply. The close
preference is saved locally in `desktop.json`. Startup diagnostics go to
`background.log` next to the database; if background startup fails, Reverb
terminates and reaps that copy before reporting the failure. Files over 5 MiB
are rotated on the next background launch.

## Releases and auto-update

The app checks GitHub Releases (`--update-repo`, default `uhhhm/reverb`) at
start and every six hours, downloads a newer stable release in the background,
verifies it, and installs it only when the user presses **Restart now**. Local
builds report version `dev` and never update.

A release becomes installable through `.github/workflows/desktop.yml`, which
runs when a GitHub release is **published**:

1. Tag the commit on `main` (`v1.2.3`) and publish a non-draft release for it.
   A stable release must point at the current `main` commit; a prerelease may
   point anywhere, but `/releases/latest` never serves prereleases, so only
   stable tags reach the updater.
2. The workflow first verifies that `ci.yml` succeeded for that commit. If CI
   has not finished yet the run fails; re-run it with **Run workflow** and the
   tag once CI is green.
3. It builds `reverb-desktop-<version>-<os>-<arch>.zip` for macOS and Linux
   (amd64 and arm64) plus Windows amd64, and attaches all five to the release.
   Each zip holds the bare `reverb-desktop` executable — `reverb-desktop.exe`
   on Windows — which is the only file the updater swaps; the bundled tools
   beside the app are left as they are.
4. On each Linux runner it also wraps that same binary in the install bundle,
   verifies the extracted bundle's tools as `make package-linux` does, and
   attaches `Reverb-<version>-linux-<arch>.tar.gz` for amd64 and arm64. The
   updater never selects these; they are for first installs.

Between publishing and the assets landing, the updater sees the new tag with
no payload for its platform and re-checks every fifteen minutes instead of
waiting for the next six-hour cycle.

On Windows the swap needs no helper process: an image that is mapped cannot be
deleted or written, but it can be renamed within its volume, so the running
`reverb-desktop.exe` is moved aside and the new build takes its name. When the
old name cannot be reused — a predecessor still exiting, or an earlier
update whose cleanup never ran — the outgoing binary goes to a uniquely named
sibling instead, and both forms are collected on the next boot. A rename the
filesystem refuses outright (antivirus, a shell holding the folder) rolls back
to the working binary.
