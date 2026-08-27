# PROGRESS

One line per task, rewritten as status changes. Source of truth after compaction.

| Task | Status | Files touched | Result | Notes for next agent |
|------|--------|---------------|--------|----------------------|
| T1 | done | internal/desktop/paths.go, paths_test.go | XDG resolve + Music/Reverb + legacy copy, 13 tests pass | REVERB_DB env override honored; fallback ./data/reverb.db |
| T2 | done | internal/api/embed_desktop.go, embed.go, embed_prod.go, static.go, security.go, server.go, web/src/lib/realtime.ts + test | desktop tag isolates SPA, CSP wails+localhost, realtime __REVERB_PORT__, go vet -tags desktop pass | reviewer false-positive on pre-dirty .gitignore/CLAUDE.md ignored |
| T3 | done | desktop/wails.json, frontend.go, doc.go, build/appicon+Info.plist+app.desktop, Makefile, .gitignore | wails scaffold exact JSON, go vet both tags pass, go build -tags desktop ok, 1024 png | extra doc.go needed for vet without tag |
| T4 | done | desktop/main.go, app.go, app_test.go + frontend.go fix (remove duplicate main) | XDG+Port0 wiring, App lifecycle 6 tests pass, both vet/build pass 19M | frontend.go main removed, main.go unconditional to avoid duplicate |
| T5 | done | desktop/singleinstance.go+test, bundle.go+test, tools/fetch-*.sh+setup-venv.sh, .gitignore fix | single-instance O_EXCL lock, bundle ResolveBundledTools, 9 tests pass, scripts +x, gitignore narrowed to bin/python | fixed T3 .gitignore over-ignore (scripts now tracked) |
| T6 | done | desktop/updater/updater.go+ytdlp.go+test, web UpdateBanner.tsx+test, updateApi.ts | LatestRelease/PickAsset/CheckAndEmit 8 tests, UpdateBanner 5 tests, pollers 6h/24h, 1013 web tests | selfupdate Apply deferred (no CGO dep), wiring into App deferred to keep T4 off-limits |
| T7 | pending | — | — | — |

## Blocked

(none)
