# 08: One module per replicated fact, from local edit to peer apply

**What to build:** Each kind of replicated fact is one deep module. Examples: a track name, crop, quality, loudness, cover, Not interested mark, taste setting, recommendation addition, or track deletion. Each module owns:
- its entity and field names and its wire value type;
- the local command, which writes and then emits;
- the peer apply, which projects without emitting;
- parking a change until its catalog id is bound;
- repointing its rows when catalogue entities merge.

Adding a replicated field then means adding or editing one module plus its migration.

**Today one fact is spread across many packages.** A track rename crosses api → metadata → override → syncemit → catalog → sync → p2p → materialize → override again. Adding a per-track field touches about 10 files in 5 or more packages:
- the constant in `sync/sync.go`;
- an emitter in `metadata` or `api/track_sync.go`;
- a case in the `materialize.applyTrack` switch plus a `With*` option;
- composition-root wiring;
- `ByCatalogID` and backend-id-parked queries;
- the park-until-bound logic, rewritten again;
- `api/decorate.go`;
- `catalog/merge.go` `repointTrackState`;
- `syncemit/backfill.go`.

**The documented invariants aren't enforced.**
- `syncemit` is said to be the one place that writes to the log. But `api/library_deletion.go`, `sync/deletion.go`, `api/links.go`, `linkadd` and `playlistcrdt/emit.go` all call `AppendChange` directly.
- Field names are said to live in `sync.go`. They are also hard-coded as `"__deleted"`, `"track"` and `"playlist"` literals, in SQL (`queries/sync.sql`, `catalog.sql`, `plays.sql`), and in `playlistcrdt`.
- There are five parallel "kind" vocabularies: `sync.Entity*`, `core.Entity*`, `override.Kind*`, `cover.Kind*`, `notinterested.Kind*`.

**The shallow modules this would absorb:**
- `internal/metadata` (96 lines; owns only rename and crop);
- `internal/crop` (a structural clone of `override.Service`, no tests);
- `internal/recommendationevent` (59 lines);
- `api/track_sync.go`;
- the duplicated `CatalogIDForTrack` and `ValidRef` / cover-path helpers.

**What materialize becomes.** It turns from a switch into a registry over the fact modules. `playlistcrdt` is already the right shape (`Publish` diffs against the log; `Apply` rebuilds the entity). Use it as the model.

**Constraints (AGENTS.md):** preserve catalog ids versus backend ids, catalog-first projection, non-emitting peer application, and local-only offline sets. The deepening should make those invariants structural, for example a peer-apply path that has no emitter to call.

**Decision needed (why this is needs-triage).** The review deliberately proposed no interface. Before implementation, grill the design:
- the shape of a fact module's interface;
- whether the registry lives in `sync` or `materialize`;
- how tombstones and `libraryPresent` fit (settled by ticket 06: tracks have no tombstone, and `libraryPresent` is an ordinary last-write-wins fact);
- how SQL-side projections (`libraryPresent`, browsable tracks) are brought in or kept.

The work likely splits into several tickets after that.

**Blocked by:** None. 01 and 02 are done, and 06's decision is made. Implementation slices that come out of the grilling may be blocked by 06, 14, 15 or 16.

**Status:** ready-for-human

- [ ] The interface is designed through grilling (optionally design-it-twice), and the result is recorded in a comment here. New domain terms go into `CONTEXT.md`, and an ADR is written if a load-bearing choice is made.
- [ ] (Ticket 14) A shared two-device test harness replaces the three copies (`notinterested_test.go:20`, `tastesettings_test.go:19`, `playlistcrdt_test.go:19`), and every fact module is tested through it: local edit on A → visible on B, with no echo back to A.
- [ ] (Ticket 15) A track rename made through the HTTP API on device A is visible on device B in an e2e test, and it still passes after the refactor.
- [ ] Nothing outside the fact modules and the `sync` log itself calls `AppendChange`.
- [ ] Entity and field names appear as literals in exactly one place per fact. Ticket 16 moves them into one vocabulary first.
- [ ] `docs/architecture.md` and the comments describe the new shape, replacing the statements that are false today.
- [ ] `go test ./internal/metadata ./internal/api ./internal/materialize ./internal/syncemit ./internal/catalog ./internal/sync ./internal/p2p ./internal/app` and `make check-full` pass.

## Comments

**Triage (2026-09-29): grill first, then split.** This is worth doing: adding a replicated field touches about 10 files in 5 packages. But it is too large for one ticket, and its interface isn't designed yet. Status is `ready-for-human` because the next step is a grilling session with the owner, not implementation.

**Split out now.** These three tickets pay off whatever interface the grilling settles on:
- **14**: one two-device harness replacing the three copies.
- **15**: an HTTP rename e2e test from device A to B, the safety net for the refactor.
- **16**: one vocabulary for entity and field names, and no direct `AppendChange` in `internal/api`.

**Starting position for the grilling.** These are proposals to argue with, not decisions:
- **Where the registry lives:** in `materialize`. `sync` stays a log and conflict resolver that knows nothing about the domain.
- **What the shared interface covers:** the peer side only. That is entity and field names, apply without an emitter in reach, and repointing on a catalogue merge. Each module's local command stays its own concrete API, since commands differ too much to share a signature.
- **Parking until a catalog id is bound:** handled once by the registry, not rewritten per module.
- **Tombstones and `libraryPresent`:** settled by 06. Tracks have no tombstone, and `libraryPresent` is one more last-write-wins fact.
- **SQL projections:** browsable tracks and `libraryPresent` are still open. Either keep them in SQL and have the owning module document the SQL as its read side, or pull them into the module. Decide during the grilling.
- **Model:** `playlistcrdt` (`Publish` diffs against the log; `Apply` rebuilds the entity).

After the grilling, record the interface here, add new terms to `CONTEXT.md` and write an ADR if a load-bearing choice is made. Then split the migration into per-fact tickets, moving track rename and crop (`metadata` and `crop`) first.

