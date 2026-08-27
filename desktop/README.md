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
