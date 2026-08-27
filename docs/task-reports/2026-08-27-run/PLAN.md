# PLAN — Reverb Desktop App (Wails v2) Linux + macOS

## Repo rules (binding)

- **Backend tests:** `go test ./cmd/... ./internal/...` — NEVER `go test ./...` (web/node_modules contains vendored Go)
- **Frontend:** `cd web && npm run test` (vitest) / `npm run e2e` (Playwright hermetic) / `npm run lint` (eslint)
- **Codegen:** `make gen` after editing `internal/store/queries/*.sql` (sqlc -> internal/store/db). Not needed for this plan unless migrations added (none planned).
- **Artifacts in sync:** `internal/api/openapi.yaml` if handlers change; `internal/api/dist` is built SPA for prod tag (`make web` copies web/dist). For `desktop` tag, Wails serves SPA — no dist needed but `make web` still valid.
- **Format:** `gofmt -w` before commit; `gofmt -l ./cmd ./internal ./desktop` must be empty.
- **Lint:** `.golangci.yml` (errcheck, govet, staticcheck...). Deferred Close ignores preconfigured. Run `golangci-lint run` if available; otherwise gate uses `go vet`.
- **Commits:** Conventional Commits `feat(scope): …` with task ID, e.g. `feat(desktop): T3 scaffold`. Commit on `main` only, no branches, no `git add -A`, stage explicit paths, `git status --short` before commit.
- **Production build checks:** `make build` (web + CGO_ENABLED=0 go build -tags prod) and `make web`; for desktop: `go build -tags desktop ./desktop` should typecheck (requires wails when present; stub otherwise).
- **Security:** single-user fork, no new auth bypass; CSP must remain defensive.

**Frozen gate command (run personally before every commit):**

```bash
gofmt -l ./cmd ./internal 2>&1 | grep -q . && echo "gofmt failed" && exit 1; go test ./cmd/... ./internal/... && cd web && npm run lint && npm run test
```

Additional manual gate at start and end: `go vet ./cmd/... ./internal/...` and `cd web && npm run build` (vite) and `CGO_ENABLED=0 go build -tags prod -o /tmp/reverb ./cmd/reverb` sanity.

---

## Value ordering & rationale

Foundations first, then what rests on them. If run ends early, user wakes to working desktop scaffold rather than half-bundled updater.

1. **T1 — Desktop paths** (filesystem contract everyone reads) — no UI impact, parallelizable.
2. **T2 — API embed/CSP isolation** — behind `desktop` build tag, no user impact, unblocks Wails AssetServer.
3. **T3 — Wails scaffold** (wails.json, Makefile, build assets, frontend bridge) — behind tag, no user impact.
4. **T4 — Server lifecycle inside Wails** (port 0, shutdown, deps reuse) — first visible value: window opens.
5. **T5 — Window/quit/single-instance + bundling env wiring** — hardening + bundle contract (no download of binaries in this run; fetch scripts are gitignored).
6. **T6 — Auto-update + hot yt-dlp** (GitHub poll + selfupdate + ytdlp ticker + UpdateBanner) — last, depends on window.
7. **T7 — CI & docs** (desktop.yml matrix, README, docs/deployment).

Every commit green on its own (`desktop` tag isolated).

---

## Frozen vocabulary (from spec + CONTEXT.md)

Use exactly spec terms in identifiers, paths, schema, UI copy, commits. Avoid lists honored.

- **Desktop** build tag: `desktop`
- **Device**: single running instance (laptop or server) — avoid client/node/peer (existing CONTEXT). Desktop adds no new device term.
- Terms from spec: `close→quit`, `auto-update`, `bundle` (ffmpeg/spotDL/Navidrome/deno), `single-instance`, `assetServer fallback`, `XDG` (`os.UserConfigDir`), `REVERB_DOWNLOAD_DIR=~/Music/Reverb`, `REVERB_NAVIDROME_BIN`, `REVERB_SPOTDL_PATH`
- Avoid: `client`, `hub`, `host` for server; do not rename existing `device`/`server`.

---

## Frozen interface contracts

### 1. Desktop filesystem contract (T1 owns, everyone reads)

```go
// internal/desktop/paths.go — no wails import, pure stdlib, testable with env override.
package desktop

// ResolveDesktopDB returns SQLite path for desktop mode.
//   macOS: ~/Library/Application Support/Reverb/reverb.db
//   linux: ~/.config/reverb/reverb.db  (XDG via os.UserConfigDir)
// Falls back to "./data/reverb.db" if UserConfigDir errors.
// On first launch, if legacy ./data/reverb.db exists and desktop path does not, caller may copy (helper provided).
func ResolveDesktopDB() string

// ResolveDesktopDownloadDir returns ~/Music/Reverb, creating it if missing (mkdir 0755).
func ResolveDesktopDownloadDir() string

// ResolveDesktopDataDir returns directory containing DB (Dir(ResolveDesktopDB())).
func ResolveDesktopDataDir() string

// MaybeMigrateLegacyDB copies ./data/reverb.db -> desktop DB if desktop DB missing and legacy exists. No overwrite.
func MaybeMigrateLegacyDB() error
```

Env overrides for tests: `REVERB_DB` if set wins over XDG (so existing config.Load still authoritative — desktop/main.go will set os.Setenv before wiring).

No DB schema change.

### 2. API embed/CSP contract (T2)

```go
// internal/api/embed_desktop.go
//go:build desktop
package api
func (s *Server) embeddedSPA() http.Handler { return http.NotFoundHandler() } // Wails AssetServer serves SPA

// internal/api/embed_prod.go build tag becomes: //go:build prod && !desktop
// internal/api/embed.go build tag becomes: //go:build !prod && !desktop

// internal/api/static.go
func (s *Server) spaHandler() http.Handler {
  if s.deps.Desktop { return s.embeddedSPA() } // new field, skips Vite proxy even in dev=false
  if s.deps.Dev { /* proxy */ }
  return s.embeddedSPA()
}

// internal/api/security.go
const contentSecurityPolicyDesktop = contentSecurityPolicy + "; connect-src 'self' wails: http://localhost:* ws://localhost:*"
// or conditional add when deps.Desktop

// internal/api/server.go
type Deps struct {
  // existing ... plus:
  Desktop bool // true when built with -tags desktop (set by desktop/main.go)
}
```

```ts
// web/src/lib/realtime.ts — honor injected port when running inside Wails
private url(): string {
  // if window.__REVERB_PORT__ set (number), use ws://127.0.0.1:<port>/api/v1/ws
  // else same-origin as today
}
declare global { interface Window { __REVERB_PORT__?: number; __WAILS__?: boolean } }
```

### 3. Wails scaffold contract (T3)

```
desktop/
  main.go        // composition root — reuses internal/wiring, internal/api; sets env before wiring
  app.go         // type App struct + Wails lifecycle hooks
  frontend.go    // //go:build desktop — optionally embeds web/dist or delegates to AssetServer
  wails.json     // wails v2 config
  build/
    appicon.png  // 1024 from web/public/logo.png
    darwin/Info.plist
    linux/app.desktop
  tools/         // gitignored, fetched by make desktop-deps (shell scripts)
```

```json
// desktop/wails.json (frozen)
{
  "name": "Reverb",
  "outputfilename": "reverb-desktop",
  "frontend:dir": "../web",
  "frontend:build": "npm run build",
  "frontend:install": "npm install",
  "frontend:dev:watcher": "npm run dev",
  "frontend:dev:serverUrl": "http://localhost:5173",
  "assetServer:frontend:fallback": "index.html"
}
```

```make
# Makefile additions (frozen names)
desktop: web
	go build -tags desktop -ldflags "-X main.version=$(VERSION)" -o dist/reverb-desktop ./desktop

desktop-dev:
	wails dev -projectdir ./desktop

desktop-deps: # fetch ffmpeg static + navidrome per TARGETARCH (tools/fetch-*.sh)
```

Build tags: desktop builds must compile with `go vet -tags desktop ./desktop/...` and `go test -tags desktop ./internal/...` (trivial — desktop code is separate package).

### 4. Server lifecycle contract (T4)

```go
// desktop/app.go
package main

type App struct {
  ctx    context.Context
  cancel context.CancelFunc
  srv    *http.Server
  ln     net.Listener
  bundle wiring.ServiceBundle
  deps   api.Deps
  port   int // actual 127.0.0.1:0 port
}

func NewApp() *App
func (a *App) OnStartup(ctx context.Context)
func (a *App) OnShutdown(ctx context.Context)
func (a *App) OnBeforeClose(ctx context.Context) bool // return false = allow quit (D3)
func (a *App) GetPort() int // exposed to frontend via runtime binding

// desktop/main.go
func main() {
  // 1. ResolveDesktopDB/DownloadDir, MaybeMigrateLegacyDB, os.Setenv REVERB_DB etc.
  // 2. config.Load(os.Args[1:], os.Getenv) but override Port=0 unless --port set
  // 3. net.Listen("127.0.0.1:0") -> http.Server with api.NewServer(deps).Handler()
  // 4. if wails available: wails.Run(&options.App{OnStartup: a.OnStartup, ...})
  //    else: fallback to plain http serve (so go run ./desktop works without wails)
}
```

Wiring reuse: identical to cmd/reverb/main.go:104 builder call, but desktop/dataDir = ResolveDesktopDataDir(), and Env overrides for REVERB_NAVIDROME_BIN, REVERB_SPOTDL_PATH point to <resources>/bin if bundled files exist.

Port injection: OnDomReady or immediately after listen, `runtime.WindowExecJS` or `window.__REVERB_PORT__ = %d` if wails runtime present; else frontend falls back to same-origin (works in fallback http mode).

### 5. Window/quit/single-instance + bundle contract (T5)

```go
// desktop/singleinstance.go
package main
func AcquireSingleInstanceLock(dataDir string) (release func(), err error) // file lock DataDir/lock, second instance shows window

// desktop/bundle.go
package main
func ResolveBundledTools() (ffmpeg, navidrome, spotdl, deno string) // checks <exeDir>/../Resources/bin or ./desktop/tools/bin, returns "" if not found
// main.go will os.Setenv only if file exists and env not already set (REVERB_*_BIN override honored)
```

Ships: `desktop/tools/fetch-ffmpeg.sh`, `fetch-navidrome.sh`, `fetch-deno.sh`, `setup-python-venv.sh` (gitignored outputs to desktop/tools/bin/). No binary vendored in repo; scripts are the contract. Size budget 150-180MB documented, not enforced in gate.

Window behavior: `OnBeforeClose` returns false (allow quit) per D3. No tray. Single-instance lock file `DataDir/lock`. If lock fails, log and exit 0 or call `runtime.WindowShow` if wails (spec: go-singleinstance file lock, second launch shows window via OnSecondInstanceLaunch).

### 6. Auto-update + yt-dlp contract (T6)

```go
// desktop/updater/updater.go
package updater
type Release struct { Tag, Body string; Assets []Asset }
type Asset struct { Name, URL string }
func LatestRelease(ctx context.Context, repo string) (*Release, error) // unauth GET https://api.github.com/repos/<repo>/releases/latest
func PickAsset(rel *Release, goos, goarch string) *Asset // reverb-desktop-$VERSION-$GOOS-$GOARCH.{zip|deb|AppImage}
func CheckAndEmit(ctx context.Context, currentVersion string) (available bool, tag string) // compares semver

// desktop/updater/ytdlp.go
package updater
func UpgradeYtDlp(ctx context.Context, pythonBin string) error // exec python -m pip install --upgrade yt-dlp, log-only, no restart
```

Ticker: OnStartup launches goroutines — `checkUpdate()` on DomReady + every 6h; `upgradeYtDlp()` every 24h. Emits Wails event `update:available` with tag; frontend toast handles it. Apply uses `github.com/creativeprojects/go-selfupdate` or `inconshreveable/go-update` (chosen in T6, default creativeprojects).

Frontend:

```ts
// web/src/components/UpdateBanner.tsx
// props: { tag: string; onDismiss: ()=>void; onUpdate: ()=>void }
// Listens to window.runtime.EventsOn("update:available") or polling GET /api/v1/version vs latest
// web/src/lib/updateApi.ts — thin fetch for version check in fallback mode (no wails)
```

Desktop app binding: `App.CheckUpdate() (string, error)` and `App.ApplyUpdate() error` exposed via Wails binding (or plain http handler `/api/v1/desktop/update` when not in wails mode, guarded by Desktop flag).

### 7. CI & docs contract (T7)

```yaml
# .github/workflows/desktop.yml (frozen shape, parallels release.yml)
name: desktop
on: push: tags v*
jobs:
  build:
    strategy: { matrix: { os: [macos-14, ubuntu-22.04], arch: [amd64, arm64] } }
    steps: [setup-go, setup-node, wails build -platform $GOOS/$GOARCH -ldflags "-X main.version=$TAG"]
    artifacts: reverb-desktop-$VERSION-$GOOS-$GOARCH.{zip,deb,AppImage}
```

Docs: `docs/deployment.md` new section "Desktop (Wails)" + `README.md` `make desktop` row + `CONTEXT.md` device line update. OpenAPI unchanged (no new routes for desktop v1).

---

## Tasks (numbered, dependencies, file ownership, acceptance)

### T1 — Desktop paths (XDG, Music dir, legacy migration)
- **Owns:** `internal/desktop/paths.go`, `internal/desktop/paths_test.go`
- **Off limits:** `desktop/*`, `internal/api/*`, `web/*`, `Makefile`
- **Depends on:** nothing
- **Work:** Implement ResolveDesktopDB/DownloadDir/DataDir + MaybeMigrateLegacyDB as per contract §1. Must handle darwin vs linux via os.UserConfigDir (already XDG on linux). Tests cover XDG, $HOME fallback, AlreadyExists no-overwrite, legacy copy, darwin path shape. No wails import.
- **Acceptance:** `go test ./internal/desktop/... -v` passes (6+ cases); `gofmt -l` empty; `go test ./cmd/... ./internal/...` passes.

### T2 — API embed/CSP isolation for desktop tag
- **Owns:** `internal/api/embed_desktop.go` (new, //go:build desktop), edits to `internal/api/embed.go`, `embed_prod.go` (add `&& !desktop`), `internal/api/static.go`, `internal/api/security.go`, `internal/api/server.go` (add Desktop bool), `web/src/lib/realtime.ts` (port injection), `web/src/lib/realtime.test.ts` (if exists, else new minimal test)
- **Off limits:** `desktop/*`, `internal/desktop/*`, `Makefile`, `web/src/routes/*`
- **Depends on:** nothing (can parallel with T1)
- **Work:** Implement build-tag isolation so `go build -tags desktop` uses NotFoundHandler for SPA (Wails serves), prod+!desktop keeps embed, !prod&&!desktop keeps stub. Add Desktop bool to Deps, make spaHandler respect it. Extend CSP with wails: http://localhost:* ws://localhost:* when Desktop. Extend realtime.ts to prefer window.__REVERB_PORT__. Keep existing tests green.
- **Acceptance:** `go test ./internal/api/... -run TestSecurity -v` passes with desktop tag; `go vet -tags desktop ./internal/api/...` passes; `go test ./cmd/... ./internal/...` passes without tag; `cd web && npm run test -- src/lib/realtime` passes; `gofmt -l` empty.

### T3 — Wails scaffold (wails.json, Makefile, build assets, frontend bridge)
- **Owns:** `desktop/wails.json`, `desktop/frontend.go`, `desktop/build/**`, `Makefile` (desktop targets), `.gitignore` (desktop/tools), `desktop/README.md` (optional stub)
- **Off limits:** `desktop/main.go`, `desktop/app.go`, `internal/*`, `web/*` (except reading web/public/logo.png)
- **Depends on:** T1, T2 (needs Desktop bool contract stable)
- **Work:** Create desktop/ dir with wails.json per contract §3, frontend.go with //go:build desktop embedding fallback, build assets (appicon placeholder, Info.plist, .desktop), Makefile targets desktop/desktop-dev/desktop-deps, gitignore for desktop/tools/bin. No wails library import yet — scaffold must compile with plain Go (`go vet ./desktop/...` passes even without wails installed by guarding wails imports with build tag where needed). Verify `go vet -tags desktop ./desktop/...` passes with stub.
- **Acceptance:** `go vet ./desktop/...` passes without tag; `go vet -tags desktop ./desktop/...` passes; `make desktop` fails gracefully if wails not installed but `go build -tags desktop ./desktop` typechecks (or reports missing wails with clear error, not cryptic); `gofmt -l` empty.

### T4 — Server lifecycle inside Wails (port 0, graceful shutdown)
- **Owns:** `desktop/main.go`, `desktop/app.go`, `desktop/app_test.go`
- **Off limits:** `internal/api/*` (read-only), `internal/desktop/*` (read-only), `web/*`, `Makefile`, `.github/*`
- **Depends on:** T1, T2, T3
- **Work:** Implement main.go composition root reuse (ResolveDesktopDB, MaybeMigrateLegacyDB, os.Setenv, config.Load override Port=0, dataDir wiring, net.Listen 127.0.0.1:0, http.Server via api.NewServer(deps).Handler(), same as cmd/reverb/serve.go newHTTPServer logic but driven by Wails lifecycle not signal.Notify). App struct with OnStartup (open store, wiring.Build, Supervisor.Start, Manager.Start, Scheduler, Scrobble.RunWorker, serve), OnShutdown (cancel, srv.Shutdown 15s, Supervisor.Shutdown, Manager.Stop), OnBeforeClose allow quit, GetPort binding. Provide fallback path when wails not present (plain http serve on 127.0.0.1:port for `go run ./desktop`). Inject port to frontend via window.__REVERB_PORT__ if runtime present. Wire REVERB_NAVIDROME_BIN etc via ResolveBundledTools helper (stub in this task, real logic in T5).
- **Acceptance:** `go test ./desktop/... -run TestApp -v` passes (lifecycle/shutdown/port); `go test ./cmd/... ./internal/...` still passes; `gofmt -l` empty; manual: `go run ./desktop -- --port 0` starts and serves /api/v1/health on 127.0.0.1:XXXXX (tested via net.Listen mock).

### T5 — Window/quit/single-instance + bundled tools env wiring
- **Owns:** `desktop/singleinstance.go`, `desktop/bundle.go`, `desktop/tools/fetch-*.sh`, `desktop/tools/setup-python-venv.sh`, edits to `.gitignore` if needed for tools/bin
- **Off limits:** `desktop/main.go`, `desktop/app.go` (read-only after T4 — T5 must not edit them; instead bundle/singleinstance are called from main.go via functions already stubbed in T4), `internal/*`, `web/*`
- **Depends on:** T4
- **Work:** Implement single-instance file lock on DataDir/lock (allan-simon/go-singleinstance or pure flock via syscall), AcquireSingleInstanceLock, release on shutdown, second instance shows window. Implement ResolveBundledTools checking exe-relative Resources/bin and desktop/tools/bin, returning env paths if executables exist. Provide fetch scripts for ffmpeg (johnvansickle/evermeet), navidrome (navidrome_0.62.0 per Dockerfile:84), deno (denoland), python venv (spotdl 4.5.0 + yt-dlp floating). Scripts are not executed in gate; they are verified by existence + chmod +x and by unit test that ResolveBundledTools returns correct when files present (temp dir). Update .gitignore for desktop/tools/bin.
- **Acceptance:** `go test ./desktop/... -run TestSingleInstance|TestBundle -v` passes; `gofmt -l` empty; `go test ./cmd/... ./internal/...` passes.

### T6 — Auto-update + hot yt-dlp
- **Owns:** `desktop/updater/updater.go`, `desktop/updater/updater_test.go`, `desktop/updater/ytdlp.go`, `web/src/components/UpdateBanner.tsx`, `web/src/components/UpdateBanner.test.tsx`, `web/src/lib/updateApi.ts` (optional)
- **Off limits:** `desktop/main.go`, `desktop/app.go`, `internal/*`, `.github/*`
- **Depends on:** T4 (needs App lifecycle to hook tickers; but can be developed in parallel with T5)
- **Work:** Implement LatestRelease (unauth GitHub API), PickAsset (GOOS/GOARCH), semver Compare (x/mod/semver), checkUpdate ticker 6h + on DomReady, emit Wails event `update:available` or call App.CheckUpdate binding. Add App methods CheckUpdate/ApplyUpdate (wails binding) using github.com/creativeprojects/go-selfupdate or go-update (choose one, vendor). Implement UpgradeYtDlp 24h ticker (exec pythonBin -m pip install --upgrade yt-dlp, log only). Frontend UpdateBanner toast with "Download & Restart" calls ApplyUpdate then runtime.Quit. Provide tests for semver, PickAsset, ticker logic with injected clock/http.
- **Acceptance:** `go test ./desktop/updater/... -v` passes (semver/asset/ticker); `cd web && npm run test -- src/components/UpdateBanner` passes; `go test ./cmd/... ./internal/...` passes; `gofmt -l` empty.

### T7 — CI & docs
- **Owns:** `.github/workflows/desktop.yml`, `README.md` (add desktop section), `docs/deployment.md` (add Desktop section), `CONTEXT.md` (device includes desktop note if needed)
- **Off limits:** `desktop/*`, `internal/*`, `web/*` (except reading)
- **Depends on:** T3, T4, T6 (needs to document actual make targets + update flow)
- **Work:** Parallel to release.yml matrix for macos-14/ubuntu-22.04 amd64/arm64 (or amd64 only for linux v1). Document Linux deps `libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev`, mac Xcode, Gatekeeper right-click Open, `make desktop`, `wails dev`, XDG paths, bundled tools. Keep web+Docker docs intact.
- **Acceptance:** `gofmt -l` empty; `go test ./cmd/... ./internal/...` passes; `cd web && npm run lint` passes; yaml validates (actionlint if available, else syntactic).

---

## Dependency graph

```
T1 ─┐
    ├─> T3 ──> T4 ──> T5
T2 ─┘          ├─> T6 ──> T7
               └──────────> T7
```
Parallel batches (max 3):
- Batch 1: T1, T2
- Batch 2: T3 (needs T1+T2)
- Batch 3: T4 (needs T3), plus T5/T6 can start after T4 — but T5 and T6 parallel after T4
- Batch 4: T7

If Batch 1 both pass, remaining chain is linear through T3->T4; T5 and T6 can run in parallel after T4.

---

## Interface change policy

Frozen contracts above. If a contract turns wrong, update PLAN.md, log in DECISIONS.md with alternatives rejected and reversal, and re-dispatch completed tasks that depended on it. If >2 completed tasks would need redo, mark new work blocked and keep shipped interface.

---

## Artifacts & verification (gate per task listed above)

Final gate before first commit and at end of plan:

```bash
gofmt -l ./cmd ./internal ./desktop 2>&1
go vet ./cmd/... ./internal/...
go vet -tags desktop ./desktop/... ./internal/...
go test ./cmd/... ./internal/... 
go test -tags desktop ./desktop/... 2>&1 | head
cd web && npm run lint && npm run test && npm run build
CGO_ENABLED=0 go build -tags prod -o /tmp/reverb ./cmd/reverb && ls -lh /tmp/reverb
```
