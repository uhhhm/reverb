# Plan: Reverb Desktop App (Wails v2) — Linux + macOS

**Date:** 2026-08-27
**Status:** Approved — ready to implement
**Decisions:** D1 `linux+macOS`, D2 `B` bundle `ffmpeg`/`spotDL`/`Navidrome`/`deno` (embedded Python), D3 `close→quit`, D4 `auto-update` via GitHub Releases polling (`selfupdate`, unsigned v1), D5 keep web+Docker.

---

## 1. Goal / Non-goals

**Goal:** double-click desktop app, no `192.168.x.x:8090`. Same Go monolith runs locally, serves SPA on `127.0.0.1:0`, sync/downloads run while window is open. Installed via `dmg` (macOS) / `deb`+`AppImage` (Linux) with auto-update.

> Note on `close→quit`: with this choice sync/downloads stop when window closes. This satisfies “no LAN IP” but not “sync in background when window closed.” If background sync is later wanted, the only change is `OnBeforeClose → Hide to tray` + `Quit` via menu/`Cmd+Q` (1 line).

**Non-goals (v1):**
- Windows support
- Code signing/notarization (macOS Gatekeeper → user right-click Open for v1)
- Replacing Docker/web deployment (`cmd/reverb/main.go:39` and `Makefile:18` keep working)

---

## 2. Resolved Decisions

| # | Question | Resolution | Rationale |
|---|----------|------------|-----------|
| **D1** | OS priority | `linux` + `macOS` | Covers 95% of desktop users. Linux needs `libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev`; macOS needs Xcode. |
| **D2** | Bundle `ffmpeg`/`spotDL`/`Navidrome`? | **B — bundle.** Embed per-OS `ffmpeg` static, `navidrome 0.62.0` binary (same as `Dockerfile:84`), embedded Python venv with `spotDL 4.5.0` + `yt-dlp` (floating) + `deno` ( `Dockerfile:40` ). | Self-contained double-click. User installs nothing. Size ~150–180MB. |
| **D3** | Close behavior | `close→quit` (`runtime.Quit`) | Simple, matches browser. No daemon/tray persistence. |
| **D4** | Auto-update | Poll `GitHub Releases` on startup + every 6h; `github.com/creativeprojects/go-selfupdate` in-place. Unsigned for v1. | Wails has no built-in updater. `selfupdate` works on linux+mac without Sparkle. |
| **D5** | Keep web mode | Yes | `desktop/` is sibling to `cmd/reverb`, shares `internal/*`. `make build` unchanged. |
| **D6.1** | Python bundling | Embedded Python (venv inside `Resources`/`lib`) | Hot-update `yt-dlp` without app rebuild (see §7). |
| **D6.2** | Auto-update impl | `a` — GitHub poll + `selfupdate` | 5× less work than Sparkle/AppImageUpdate. |
| **D6.3** | `yt-dlp` freshness | Hot-update separately from app version | YouTube breaks every weeks; `Dockerfile:66` floats `yt-dlp` for same reason. |
| **D6.4** | Signing | Unsigned v1 | No Apple cert yet. Gatekeeper workaround documented. |
| **D6.5** | Data dir | XDG for desktop (`os.UserConfigDir`) | Desktop `~/Library/Application Support/Reverb/reverb.db` (mac) / `~/.config/reverb/reverb.db` (linux) + `~/Music/Reverb` for `REVERB_DOWNLOAD_DIR`. Docker stays `/data/reverb.db`. Migration: copy if legacy `./data/reverb.db` exists on first launch. |

---

## 3. Findings (current architecture)

- **Composition root** `cmd/reverb/main.go:39` is explicit registry + `wiring.NewBuilder(libraryReg, searchReg, downloaderReg, st.Q(), st, bus, clock, getenv, filepath.Dir(cfg.DBPath))` `main.go:104` . `api.NewServer(deps)` `main.go:265` listens on `:%d cfg.Port` `main.go:271`. All seams are reusable as library.
- **SPA embed** `internal/api/embed_prod.go:12` `//go:embed all:dist` + `Makefile:18` `CGO_ENABLED=0 go build -tags prod`. `embed.go:1` stub for `!prod`. `static.go:13` proxies `localhost:5173` when `Dev`.
- **DB** `modernc.org/sqlite v1.34.1` `go.mod:13` pure Go — `CGO_ENABLED=1` (required for Wails WebKit) does **not** break DB.
- **Background** `events.Bus` `internal/events/bus.go:1` + `download.Manager.Start` `internal/download/manager.go:325` + `playlistsync.Scheduler 15m` `main.go:168` + `scrobble.RunWorker 30s` `main.go:191` — all detached from WS. With `close→quit` they stop with the process.
- **Sync** `internal/sync/store.go:1` is pull `POST /sync` + `sync_revision` LWW. Needs polling if background sync desired (not in v1 with quit).
- **Router** `web/src/main.tsx:1` uses `BrowserRouter` — requires history fallback (`embed_prod.go:29` `r2.URL.Path="/"`) or AssetServer fallback.

---

## 4. Architecture

```
desktop/
  main.go          # desktop composition root (reuses internal/wiring, internal/api)
  app.go           # wails App struct: OnStartup/Shutdown, Update, OpenDownloads
  frontend.go      # //go:build desktop — go:embed all:../web/dist bridge (or delegate)
  wails.json       # wails v2 config: frontend ../web, buildCommand npm run build
  build/
    appicon.png    # from web/public/logo.png + icons/icon-512.png
    darwin/        # Info.plist, dmg background
    linux/         # .desktop file
  tools/           # per-OS ffmpeg, navidrome, deno, python venv (gitignored, fetched by make)
web/               # unchanged, wails dev → Vite :5173
internal/api/
  embed_desktop.go # //go:build desktop — no HTTP SPA serve (Wails AssetServer serves)
  security.go      # CSP: add wails: http://localhost:* ws://localhost:* when desktop
```

**Runtime flow:**
1. `desktop/main.go` resolves `Config` via `internal/config.Load` but overrides `Port=0` (or `--port` if set), `DBPath = resolveDesktopDB()` (XDG), `REVERB_DOWNLOAD_DIR = ~/Music/Reverb` (create if missing), `REVERB_NAVIDROME_BIN = <resources>/navidrome`, `REVERB_SPOTDL_PATH = <resources>/python/bin/spotdl`, `REVERB_NAVIDROME_BIN` etc. via `os.Setenv` before `wiring.NewBuilder`.
2. `net.Listen("127.0.0.1:0")` → `http.Server` with `api.NewServer(deps).Handler()` + `serveWithShutdown` `cmd/reverb/serve.go:23` but driven by Wails lifecycle, not `signal.Notify`.
3. Wails window loads `http://127.0.0.1:$port` (simplest, reuses auth cookie + existing CSP) **or** serves `web/dist` via `AssetServer` and proxies `/api/*` + `/ws` to Go port. V1: navigate to `http://localhost:$port` — one line, no proxy.
4. `App.OnShutdown` → `cancel()` → `Manager.Stop()` + `Supervisor.Shutdown` + `srv.Shutdown(15s)`; `OnBeforeClose` → allow quit (D3).
5. Downloads land in `~/Music/Reverb` which is also `MusicDir(getenv)` `internal/library/embedded/naviconfig.go:21` for built-in Navidrome scan dir.

---

## 5. Implementation Phases

### Phase 0 — Prereqs (0.5d)

- Install Wails: `go install github.com/wailsapp/wails/v2/cmd/wails@latest` (require `v2.9+`, Go 1.23+, Node 22+).
- Linux deps: `sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev pkg-config`
- Verify `CGO_ENABLED=1 go test ./internal/...` passes with `modernc.org/sqlite`.
- Spike: `wails init -n tmp -t react` inspect `wails.json` shape, then delete.

### Phase 1 — Scaffold (1d)

- `mkdir desktop && wails init` with `frontend ../web`:
  ```json
  // desktop/wails.json
  { "name":"Reverb","outputfilename":"reverb-desktop",
    "frontend:dir":"../web","frontend:build":"npm run build",
    "frontend:dev:watcher":"npm run dev","frontend:dev:serverUrl":"http://localhost:5173",
    "assetServer:frontend:fallback":"index.html" }
  ```
- `desktop/main.go`: copy `cmd/reverb/main.go:39-140` wiring; add `resolveDesktopDB()` (XDG) + tools env wiring. Export `version` via `//go:embed` or `-ldflags "-X main.version"`.
- `internal/api/embed_desktop.go`:
  ```go
  //go:build desktop
  package api
  func (s *Server) embeddedSPA() http.Handler { return http.NotFoundHandler() } // Wails serves SPA
  ```
  Guard `embed_prod.go` with `//go:build prod && !desktop`, `embed.go` with `//go:build !prod && !desktop`.
- `Makefile` new targets:
  ```make
  desktop: web; go build -tags desktop -ldflags "-X main.version=$(VERSION)" -o dist/reverb-desktop ./desktop
  desktop-dev: wails dev -projectdir ./desktop
  desktop-deps: # fetch ffmpeg static + navidrome per TARGETARCH
  ```

### Phase 2 — Server lifecycle inside Wails (1d)

- Replace `signal.Notify` `main.go:275` with Wails hooks:
  ```go
  type App struct { ctx context.Context; cancel func(); srv *http.Server; ln net.Listener; bundle wiring.ServiceBundle }
  func (a *App) OnStartup(ctx context.Context) { // open store, Build, Supervisor.Start, Manager.Start, Scheduler, Scrobble
    a.ctx, a.cancel = context.WithCancel(context.Background())
    // ... same as main.go:52-191
    a.ln, _ = net.Listen("tcp","127.0.0.1:0")
    a.srv = newHTTPServer(api.NewServer(deps).Handler()) // reuse cmd/reverb/serve.go:14 or duplicate
    go a.srv.Serve(a.ln)
  }
  func (a *App) OnShutdown(ctx context.Context){ a.cancel(); a.srv.Shutdown(ctx); a.bundle.Supervisor.Shutdown(ctx); a.bundle.Manager.Stop() }
  ```
- Inject port to frontend: `runtime.WindowExecJS(a.ctx, fmt.Sprintf("window.__REVERB_PORT__=%d", port))` or just `wails WindowNavigate http://127.0.0.1:$port` on DomReady.

### Phase 3 — Window / Quit / Single-instance (0.5d)

- `OnBeforeClose` → return `false` (allow quit) per D3. No `WindowHide`.
- Single-instance: `github.com/allan-simon/go-singleinstance` file lock on `DataDir/lock`. Second launch → `runtime.WindowShow` via `OnSecondInstanceLaunch`.
- `BrowserRouter` fix: either set `assetServer:frontend:fallback` to `index.html` in `wails.json` or patch `web/src/main.tsx` to use `HashRouter` when `window.__WAILS__` truthy. Prefer fallback (no code churn).
- CSP: `internal/api/security.go` add `connect-src 'self' wails: http://localhost:* ws://localhost:*` when `desktop` build.

### Phase 4 — Bundle B: ffmpeg / Navidrome / Python venv (1.5d)

- **ffmpeg:** `desktop/tools/fetch-ffmpeg.sh` downloads static `ffmpeg` per-OS (`johnvansickle.com` linux, `evermeet.cx` mac). `desktop/build` embeds via `go:embed` or copies to `Contents/Resources/bin` at `wails build`. Wire `utils.FfmpegPath` env if needed.
- **Navidrome:** reuse `Dockerfile:84` URL `navidrome_${VERSION}_linux_${ARCH}.tar.gz` for linux; `darwin` variant for mac. Honor `REVERB_NAVIDROME_BIN` override.
- **Deno:** copy from `denoland/deno:bin` equivalent release tarball.
- **Python venv:** vendor minimal `python3` (mac: `python.org` embed, linux: `deadsnakes` or `uv` managed) + `pip install spotdl==4.5.0 yt-dlp` into `desktop/tools/python`. At runtime `REVERB_SPOTDL_PATH` points there. Size budget 150–180MB.
- Icons: convert `web/public/logo.svg` → `desktop/build/appicon.png` (1024), generate `icon.icns`/`icon.ico`.
- Test: `handleTestAdapter` `internal/api/adapters.go:309` should green for `spotdl` with bundled path.

### Phase 5 — Auto-update (1d)

- **Library:** `github.com/creativeprojects/go-selfupdate` or `github.com/inconshreveable/go-update`.
- **Poller:** on `OnDomReady` + every `6h` ticker:
  ```go
  func (a *App) checkUpdate() {
    rel, _ := github.LatestRelease("uhhhm/reverb") // unauthenticated
    if semver.Compare(rel.Tag, version) <=0 { return }
    asset := pickAsset(rel, runtime.GOOS, runtime.GOARCH) // reverb-desktop-darwin-universal.zip etc.
    runtime.EventsEmit(a.ctx, "update:available", rel.Tag)
  }
  ```
- **UI:** toast in `web/src/components/UpdateBanner.tsx` (new) with `Download & Restart` → `selfupdate.Apply(bytes)` → `runtime.Quit`.
- **Channel:** `stable` only for v1 (tag `v*`). No `beta` track.
- **Hot `yt-dlp`:** separate ticker `24h`:
  ```go
  exec.Command(pythonBin, "-m","pip","install","--upgrade","yt-dlp").Run()
  ```
  Log-only, no restart. Honors `Dockerfile:66` floating logic.
- **CI artifact naming:** `reverb-desktop-$VERSION-darwin-amd64.zip`, `...-darwin-arm64.zip`, `...-linux-amd64.deb`, `...-linux-amd64.AppImage`.

### Phase 6 — CI & Docs (0.5d)

- `.github/workflows/desktop.yml` parallels `release.yml`:
  ```yaml
  matrix: { os: [macos-14, ubuntu-22.04], arch: [amd64, arm64] }
  steps: [setup-go, setup-node, wails build -platform $GOOS/$GOARCH -ldflags "-X main.version=$TAG"]
  ```
- `docs/deployment.md` new section “Desktop (Wails)” with Linux deps + mac right-click Open.
- `README.md` add `make desktop` + `wails dev`.
- `CONTEXT.md` add `Device` now includes “desktop app (Wails) — same binary, local server on 127.0.0.1:0”.

---

## 6. File / Build Changes Summary

| Path | Action | Notes |
|------|--------|-------|
| `desktop/*` | new | `main.go`, `app.go`, `wails.json`, `build/` |
| `desktop/tools/*` | new, gitignored | fetched ffmpeg/navidrome/deno/python venv |
| `internal/api/embed_desktop.go` | new | `//go:build desktop` |
| `internal/api/embed_prod.go` | edit | add `&& !desktop` to build tag |
| `internal/api/embed.go` | edit | add `&& !desktop` |
| `internal/api/security.go` | edit | CSP for `wails:` + `localhost:*` |
| `web/src/lib/realtime.ts:48` | edit | honor `window.__REVERB_PORT__` for WS URL |
| `internal/api/static.go:13` | edit | skip Vite proxy when `desktop` |
| `Makefile` | edit | `desktop`, `desktop-dev`, `desktop-deps` |
| `.github/workflows/desktop.yml` | new | build matrix |
| `docs/plans/wails-desktop.md` | this file | — |

No changes to `internal/wiring`, `internal/store`, `cmd/reverb` beyond sharing.

---

## 7. Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Linux `CGO`/`WebKit` missing | Document deps; `CGO_ENABLED=1` tested; fallback message if `wails build` fails. `modernc` stays pure Go. |
| `BrowserRouter` deep-link 404 | `wails.json` `assetServer:frontend:fallback:index.html` or `HashRouter` behind `__WAILS__` flag. |
| Large bundle (~150MB) | Expected for B; `AppImage`/`dmg` compression; future: strip Python to `uv` micro. |
| `yt-dlp` staleness | Hot-upgrade ticker `24h` separate from app update. |
| macOS Gatekeeper block (unsigned) | Docs: right-click Open → Open; future: add Apple cert + `gon` notarize. |
| `close→quit` loses background sync | Documented; future toggle to `Hide to tray` is 1-line `OnBeforeClose → WindowHide`. |
| Port collision | `127.0.0.1:0` random, never `0.0.0.0`; no firewall prompt. |

---

## 8. Verification

- **Dev:** `wails dev --projectdir ./desktop` window opens, `GET /api/v1/health` 200, Search Everywhere SSE, WS `realtime.ts:83` reconnect, downloads to `~/Music/Reverb`, scan flips to in-library, closing window quits, reopen resumes.
- **Build:** `make desktop` produces binary <200MB, `wails build` for `darwin/universal` + `linux/amd64` in CI.
- **Regression:** `go test ./cmd/... ./internal/... && cd web && npm run test` green; `make build` Docker still builds.
- **Update:** mock `GITHUB_TOKEN` release, toast appears, `selfupdate` applies, restart shows new `version` at `/api/v1/version`.
- **yt-dlp:** stop `python -m pip show yt-dlp`, trigger ticker, version bumps without app restart.

---

## 9. Rollout Order

1. Phase 1+2 scaffold behind `desktop` tag — no user impact.
2. Phase 3 window/quit — manual QA linux+mac.
3. Phase 4 bundle tools — size audit.
4. Phase 5 auto-update + hot `yt-dlp`.
5. Phase 6 CI + docs, tag `v0.x-desktop` prerelease.

