# 01: Reload builds the same services boot does

**What to build:** After an adapter change, downloads keep minting catalog ids and managed-playlist edits keep replicating. Both previously stopped silently.

The previous boot and reload paths assembled the `wiring.ServiceBundle` differently. `app.build()` called `builder.Build` and then patched the boot bundle:
- `bundle.Manager.SetCanonicalMinter(catalogSvc)` (`internal/app/build.go:336`)
- `bundle.Sync.WithEmitter(playlistProjection)` (`internal/app/build.go:610`)
- the completion and linked hooks, which are set on both the Builder and the boot manager (`build.go:509-520`)

`ServiceReloader.Reload` (`internal/app/reload.go:115`) only called `builder.Build`, so anything added by those patches was missing from the new bundle:
- **Catalog ids.** `wiring.go:867` constructs a new `download.Manager` without a minter, and `Manager.mintAndStoreCanonicalID` returns `""` for a nil minter (`internal/download/manager.go:512`).
- **Playlist replication.** The new `playlistsync.Service` has no emitter, so `publish` becomes a no-op (`internal/playlistsync/emit.go:24`).

The deepening: `wiring.Builder` is the only place a bundle is assembled. `build.go` supplies each dependency to the Builder once and never patches a bundle after `Build` returns. Reload then produces a bundle wired identically to boot's.

Additional reload fixes:
- linkadd reads a provider for the live manager; HTTP no longer calls `SetDownloader` on each request.
- Delegated covers and `publishLibrary` use `Reloader.Current()`, including its nil-library state.
- `Pairing`, `SyncStore`, and `Deletion` are built once in the composition root; the Builder retains one boot-mode `Supervisor`.
- Dead nil-fallbacks and duplicate materializer construction are removed.

**Blocked by:** None

**Status:** done

- [x] A regression test, written first and failing on `main`, boots through `app.Build`, triggers an adapter reload, then downloads a track and edits a managed playlist. It asserts the job carries a catalog id and the playlist edit is in the change log.
- [x] The e2e version of that scenario runs on two devices (`internal/app/sync_e2e_test.go` style). After an adapter reload on device A, a playlist edit made on A reaches device B.
- [x] `build.go` sets no field and calls no setter on a `ServiceBundle` member after `builder.Build` returns. Every per-bundle attachment lives in the Builder.
- [x] linkadd reaches the live download manager without the per-request `SetDownloader` call.
- [x] Services that do not depend on adapter configuration are built once, not on every reload.
- [x] `go test ./internal/wiring ./internal/app ./internal/api` and `make check-full` pass.

## Verification

[Repeatable scenarios and check results](../verification-01.md).
