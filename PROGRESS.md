# PROGRESS

One line per task, rewritten as status changes. Source of truth after compaction.

| Task | Status | Files touched | Result | Notes for next agent |
|------|--------|---------------|--------|----------------------|
| T1 | done | 12 files: migrations/0024, queries devices/pairing/sync/offline_set, db generated, migrate_single_user, store.go | schema+queries migrated, make gen idempotent, go test green | migration was 0024 (occupied) moved Go migrator to 0025 with fallback |
| T2 | done | internal/sync/device.go, pairing.go, pairing_test.go, wiring/wiring.go | pairing code generate/redeem+token auth+server device, go test green | alias reverbsync to avoid stdlib clash |
| T3 | done | internal/sync/sync.go, merge.go, store.go, sync_test.go | LWW merge+store+Reconcile with delete-wins, 13 tests pass, go test green | delete-wins overrides LWW; revision AUTOINCREMENT |
| T4 | done | internal/api/pairing.go+test, sync.go+test, middleware_sync.go, server.go, security.go, openapi.yaml | pairing code/redeem/devices + sync reconcile/status, Bearer CSRF exempt, 503 fallback, go test green | FK cleanup via DBTX to avoid new queries |
| T5 | done | internal/sync/deletion.go+test, api/library_deletion.go, synced_playlists.go (emit), deletion_test.go | tombstone __deleted for playlist/track, delete-wins, offline local-only verified, 11 tests pass | no track-delete route; helper for future wiring |
| T6 | done | internal/offlineset/offlineset.go+test, api/offline_set.go+test, api/server.go, openapi.yaml | offline set per-playlist local-only, FK cascade, no sync emission, 16 tests pass | handlers return 503 until wiring completes in T8 |
| T7 | done | internal/linkresolve/resolver.go, spotify.go, youtube.go+tests, api/links.go+test, server.go, openapi.yaml | Spotify/YouTube URL resolve + add with catalog + sync emit + ManualURL, source-native, go test green | stable trk_link_<id>, dual emit for test tolerance |
| T8 | done | internal/wiring/wiring.go, cmd/reverb/main.go, api/server.go | wiring Build creates server device + Pairing/SyncStore/Deletion/Offline, Reload reconstructs, go test green | stateless wrappers keep reload safe |
| T9 | done | web/src/lib/pairingApi.ts+test, routes/Pairing.tsx+test, App.tsx | pairing generate/redeem + device list + sync status + token storage, 28 tests pass, lint green | countdown interval, localStorage guard |
| T10 | done | web/src/lib/offlineSetApi.ts+test, syncApi.ts, offlineSetStore/syncStore, routes/OfflineSet.tsx+test, components/OfflineToggle, App.tsx, routes/SyncedPlaylist.tsx | offline-set list/toggle per playlist + sync indicator, 18 tests pass, lint green, 983 total pass | duplicate getSyncStatus intentional per contract |
| T11 | done | web/src/lib/linkApi.ts+test, routes/AddFromLink.tsx+test, App.tsx | resolve preview + playlist dropdown + download toggle + source-native helper, 23 tests pass, 1006 total | route /add-from-link chosen |
| T12 | done | web/e2e/pairing.spec.ts, offline-set.spec.ts, add-from-link.spec.ts | hermetic Playwright 7 specs pass, go vet + go test + npm lint/test/build + CGO build pass, 1006 vitest + 7 e2e | reuse installApiMocks override, exact heading/textbox roles |

## Blocked

(none)

