# 06: A deleted track that is downloaded again returns to household browsing

**What to build:** If the owner deletes a library track and later downloads it again, it shows up again in the library and in household browsing on every device, and edits made to it afterwards replicate.

**What happens today.** Deleting a library track (`DELETE /library/track/{id}`) appends three changes:
1. a file tombstone;
2. `libraryPresent = false` for the catalog id, under the comment "a later library publish sets it again if the track comes back" (`internal/api/library_deletion.go:117-130`);
3. a **track tombstone** (`track/__deleted`) through `emitTrackDeletion` (`library_deletion.go:144`, `178-203`).

The tombstone overrides everything that comes after it, permanently:
- `syncemit.EnsureLibraryMembership`, the download-linked hook, returns early when it sees `__deleted` (`internal/syncemit/library.go:74`). That contradicts `build.go:512-514`, which says a re-downloaded track "enters household browsing at once, including a track deleted earlier".
- `ListBrowsableCatalogTracks` excludes tombstoned tracks (`internal/store/queries/catalog.sql:18-21`).
- Peers reject any later field for a tombstoned entity (`internal/sync/store.go:1208-1220`), so a rename, crop or play of the re-downloaded track does not replicate.

A re-download most likely resolves to the same catalog id through its aliases, so it lands under the tombstone.

**Decision: A** (see Comments). The code held two intents that can't both stand:
- `libraryPresent` is a reversible flag, and the design says a re-download reverses it.
- `internal/syncemit/backfill_test.go:207-213` asserts that a track tombstone hides the track for good.

The options:
- **A (chosen).** Library deletion withdraws the track with `libraryPresent = false` only, with no track tombstone. The catalog identity, its plays and its overrides survive, and a re-download restores it. The owner removed a file, not a recording from history.
- **B.** Keep the tombstone and have a re-download mint a fresh catalog id that does not alias to the tombstoned one. History and overrides stay with the old identity.
- **C.** Add a way to lift a tombstone. This changes delete-wins semantics in `sync`, and every device in the support window must understand it (ADR 0004).

**Blocked by:** None

**Status:** done

- [x] The owner picks A, B or C, and the choice is recorded in a comment on this ticket.
- [x] Write a failing e2e test first, on two paired devices. On device A, delete a library track, download the same track again, and rename it. The track is browsable on both devices and shows the new name on B.
- [x] Deleting a track that is not downloaded again still removes it from household browsing on every device.
- [x] Library deletion appends no track tombstone. A track tombstone already in a log (written before this change) is still honoured: the track stays hidden, and the tombstone case in `internal/syncemit/backfill_test.go` becomes a test of that legacy behaviour.
- [x] `emitTrackDeletion` and `DeletionService.DeleteTrack` are removed if nothing else calls them.
- [x] Comments in `library_deletion.go`, `syncemit/library.go`, `build.go` and `docs/architecture.md` describe the chosen behaviour.
- [x] `go test ./internal/api ./internal/syncemit ./internal/sync ./internal/app` passes.

## Comments

**Decision: A (2026-09-29).** Library deletion withdraws the track with `libraryPresent = false` and stops appending a track tombstone.

- Library deletion is the only thing that writes a track tombstone. `emitTrackDeletion` in `internal/api/library_deletion.go` is the only caller of `DeletionService.DeleteTrack`. Dropping it removes the permanent hide without changing delete-wins in `sync`.
- `libraryPresent = false` already hides the track on every device. The first half of `internal/syncemit/backfill_test.go:195-213` shows it being withdrawn and then returning after a library publish. The tombstone is only extra on top of that.
- `internal/notinterested` made the same call for the same reason. Undo writes null rather than a tombstone, because delete-wins would stop a later re-mark from ever winning. Library membership is reversible in the same way.
- B was rejected because it splits one recording into two identities, leaving plays, taste history and overrides on the old one, and it works against alias matching.
- C was rejected because it changes delete-wins semantics for every device in the support window, for no gain over A.

**Existing tombstones.** Tracks deleted before this change keep their tombstone and stay hidden for good. We accept that and document it rather than writing a migration. A re-download of one of those tracks still does not return.

This decision also settles one of ticket 08's open questions: `libraryPresent` is an ordinary last-write-wins fact, and tracks have no tombstone in the fact-module design.

