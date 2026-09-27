# 09 — Recommendation actions refresh every affected view

**What to build:** When the owner marks or undoes Not interested, or changes Adventurousness or Online recommendations, every affected recommendation view reflects the change without a page reload. Similar artists and tracks, Home shelves, the Mix list, individual Mixes and playlist suggestions share one query and invalidation policy.

Today the mutation handlers invalidate similar artists and tracks but omit the other recommendation surfaces. A successful server-side change therefore leaves cached results visible. Start by introducing the recommendation query policy alongside the first mutation that uses it, then move the remaining recommendation readers and mutations onto it within this ticket.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] Before implementation, enumerate the failure cases and write a failing regression for a successful Not interested mutation with already-populated recommendation caches.
- [ ] All recommendation query identities and their mutation effects have one owner. Callers use that policy rather than reproducing lists of literal query keys.
- [ ] Marking and undoing a track or artist refresh the marks list and every affected recommendation surface. Changing either recommendation setting refreshes every surface whose answer can change.
- [ ] Active affected views refetch after a successful mutation; inactive cached views are invalidated so opening them cannot treat the old answer as current. Unrelated library and download queries are not refreshed by these actions.
- [ ] Failed mutations retain the prior data and existing error feedback. Refreshing a result does not itself start Radio, alter the play queue, save a Mix as a playlist, or regenerate a period's Mix merely to refresh its presentation.
- [ ] Preserve the current transport contract and polling behavior. Reuse generated transport types for documented responses touched by this work; no wholesale rewrite of request wrappers or uploads is required.
- [ ] A browser scenario warms multiple recommendation surfaces, marks a track and an artist, revisits the cached surfaces, undoes a mark, and changes recommendation settings. Assert the visible results and captured requests without a hard reload, including a failed mutation. Use controlled responses to verify invalidation independently of recommendation ranking.
- [ ] Keep the browser trace and a short record of the fixture and exact rerun command as the repeatable verification artifact.
- [ ] Focused frontend checks and `make check` pass. Run `make check-full` if implementation changes behavior across modules; record any unavailable or failing check accurately.
