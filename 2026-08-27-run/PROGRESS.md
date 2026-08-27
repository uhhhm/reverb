# PROGRESS

One line per task, rewritten as status changes. Source of truth after compaction.

| Task | Status | Files touched | Result | Notes for next agent |
|------|--------|---------------|--------|----------------------|
| T1 | done | internal/desktop/paths.go, paths_test.go | XDG resolve + Music/Reverb + legacy copy, 13 tests pass | REVERB_DB env override honored; fallback ./data/reverb.db |
| T2 | done | internal/api/embed_desktop.go, embed.go, embed_prod.go, static.go, security.go, server.go, web/src/lib/realtime.ts + test | desktop tag isolates SPA, CSP wails+localhost, realtime __REVERB_PORT__, go vet -tags desktop pass | reviewer false-positive on pre-dirty .gitignore/CLAUDE.md ignored |
| T3 | pending | — | — | — |
| T4 | pending | — | — | — |
| T5 | pending | — | — | — |
| T6 | pending | — | — | — |
| T7 | pending | — | — | — |

## Blocked

(none)
