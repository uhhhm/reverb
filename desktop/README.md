# Reverb Desktop (Wails)

Wails v2 desktop wrapper for Reverb. Sibling to `cmd/reverb`, shares `internal/*`.

## Prerequisites

- Go 1.23+
- Node 22+
- Wails v2 (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
- Linux deps: `libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev pkg-config`
- macOS: Xcode

## Commands

```bash
make desktop        # build dist/reverb-desktop (requires web build)
make desktop-dev    # wails dev -projectdir ./desktop (hot reload via Vite :5173)
make desktop-deps   # fetch ffmpeg static + navidrome per TARGETARCH (tools/fetch-*.sh)
```

Config is in `desktop/wails.json` — frontend `../web`, build `npm run build`, dev server `http://localhost:5173`, fallback `index.html` for BrowserRouter.

Build assets: `desktop/build/appicon.png` (from `web/public/logo.png`), `build/darwin/Info.plist`, `build/linux/app.desktop`.
Bundled tools are fetched into `desktop/tools/` (gitignored) per arch.

Data dirs (desktop mode): DB via `internal/desktop.ResolveDesktopDB()` (XDG), downloads `~/Music/Reverb`.

## Background sync (macOS and Linux)

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

Background shutdown controls use a Unix socket in a `0700` directory next to
the database, with socket mode `0600`; they are not exposed on the HTTP API.
Existing paired-device authentication and HTTP Host/Origin guards still apply.
The close preference is saved locally in `desktop.json`. Startup diagnostics go
to `background.log` next to the database; if background startup fails, check that
file. Files over 5 MiB are rotated on the next background launch.
