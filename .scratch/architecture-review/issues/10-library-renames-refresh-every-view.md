# 10 — Library renames refresh every affected view

**What to build:** Renaming a track, album or artist, individually or in a batch, updates every affected library list, detail view and managed playlist without reloading the app. Rename dialogs and incoming library updates use the same library query and invalidation policy.

Today each dialog maintains its own invalidation list, and those lists disagree with each other and with realtime library updates. Introduce the shared policy through one complete rename flow first, then migrate the remaining rename flows and their readers within this ticket. Recommendation queries are a separate domain, so this work can start independently of ticket 09.

**Blocked by:** None — can start immediately.

**Status:** done

- [x] Before implementation, enumerate the failure cases and write a failing regression for a rename with affected list, detail and playlist results already cached.
- [x] Library list and detail query identities and rename invalidation have one owner. Individual track, album and artist renames, batch renames, and existing relevant library-update events use that policy.
- [x] Successful renames refresh all views that show the changed display metadata, including artist and album details and managed playlist contents. Inactive affected queries are invalidated as well as active ones.
- [x] Cascading album and artist display renames remain visible on their tracks. Clearing an override restores the library's original name. Backend IDs, catalog IDs, original-name identity keys and source file tags keep their existing meanings.
- [x] Failed writes retain truthful cached data and error feedback. A partially successful batch refreshes the changes that were actually applied without presenting rejected changes as saved.
- [x] Realtime library invalidation continues to cover its existing download, sync and browsing consumers. This ticket does not require a new event protocol or changes to the play queue.
- [x] A browser scenario warms a library list, artist detail, album detail and managed playlist containing the same tracks; performs individual and batch renames, including a partial failure; clears an override; and delivers an incoming library update. Navigating among those views shows the expected names without a hard reload.
- [x] Keep the browser trace and a short record of the fixture and exact rerun command as the repeatable verification artifact.
- [x] Focused frontend checks and `make check` pass. Run `make check-full` if implementation changes behavior across modules; record any unavailable or failing check accurately.

Verification: [fixture, regression evidence, checks and trace rerun](../verification-10.md).
