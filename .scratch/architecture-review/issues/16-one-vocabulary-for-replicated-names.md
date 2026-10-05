# 16 — Replicated entity and field names come from one vocabulary

**What to build:** Code that writes or reads the change log names entities and fields through the constants in `internal/sync/sync.go`, never through string literals. Code outside the fact services no longer appends to the log directly.

**Today:**
- `internal/api/library_deletion.go` and `internal/sync/deletion.go` spell out `"__deleted"`, `"track"` and `"playlist"` literals even though `reverbsync.FieldDeleted` and the `Entity*` constants exist. `internal/sync/store.go` compares against `"__deleted"` on six lines.
- `internal/api/library_deletion.go` calls `SyncStore.AppendChange` directly, for the file tombstone, `libraryPresent = false` and the playlist-tombstone fallback, instead of going through `syncemit` or `DeletionService`.
- SQL in `internal/store/queries/sync.sql`, `catalog.sql` and `plays.sql` repeats the same names as literals. sqlc cannot take Go constants.

This is the mechanical part of ticket 08. It is split out so 08's design work starts from one vocabulary. It deliberately does not merge the five parallel "kind" vocabularies (`sync.Entity*`, `core.Entity*`, `override.Kind*`, `cover.Kind*`, `notinterested.Kind*`); that belongs to 08's interface design.

**Blocked by:** 06 (it removes the track tombstone path in `library_deletion.go`)

**Status:** done

- [x] No Go file outside `internal/sync/sync.go` contains the literals `"__deleted"`, or an entity or field name used as a `SyncChange` `EntityType` or `Field`. Tests may use literals where the test is about the wire format.
- [x] `internal/api` does not call `AppendChange`. The library-deletion writes go through `syncemit` (or `DeletionService` for the playlist tombstone), with the same entities, fields and values as today.
- [x] A Go test fails if the SQL literals disagree with the constants. For example, it runs each affected query against a log written through the constants and checks the rows it returns. This makes a renamed constant with stale SQL a test failure instead of a silent miss.
- [x] `docs/architecture.md` states where names live and who may append to the log, replacing the claims that are false today.
- [ ] `go test ./internal/api ./internal/sync ./internal/syncemit ./internal/materialize ./internal/app`, `make gen-check` and `make check` pass. Everything passes except `make gen-check`, which needs a machine where sqlc builds (see below).

## Comments

- `internal/sync/store.go` and `deletion.go` use `FieldDeleted` and `EntityPlaylist`. The managed-playlist field names (`FieldCoverURL` … `FieldCreatedAt`, `FieldPlaylistMemberPrefix`) moved from `playlistcrdt` into `sync.go`; `playlistcrdt`'s exported names alias them, so its callers are unchanged.
- Library deletion goes through the new `syncemit.EmitLibraryRemoval`. It writes the file tombstone, then `libraryPresent = false`, and returns `ErrUnavailable` or `ErrNoIdentity` so the handler keeps its previous behaviour: skip, 503 or 500. The playlist-tombstone fallback is gone: `NewServer` builds a default `DeletionService` from the `SyncStore`, the same way it defaults `SyncEmit`. `internal/api` no longer calls `AppendChange`.
- `TestQueriesReadTheLogInItsVocabulary` (`internal/sync`) runs `ListDeletedFileHashes`, `ListUnprojectedPlays`, `ListDeletedPlays` and `ListBrowsableCatalogTracks` against a log written through the constants. Renaming `FieldDeleted`, `FieldRecord` or `FieldLibraryPresent` makes it fail. The `kind = 'track'` filters read `catalog_entity.kind`, not the log, so the test leaves them alone.
- `make gen-check` cannot run on this Mac: sqlc's bundled pg_query fails to compile against the current Xcode SDK (`strchrnul`), on a clean HEAD as well. No SQL or generated file changed. Every other `make check` target passes.
- Library removal and the playlist tombstone are now authored under `syncemit`'s and `DeletionService`'s device resolvers rather than the handler's. All three resolve through `reverbsync.AuthorDeviceID` against the same database, so the author is the same device as before.
