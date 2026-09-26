# Architecture review, September 2026

A whole-codebase review for shallow modules and leaky seams. These tickets cover
the bugs it found and the candidates rated Strong. The other candidates are
listed at the end so a later review does not start from zero.

Architecture vocabulary (module, interface, depth, seam, adapter, leverage,
locality) follows the `codebase-design` skill. Domain terms follow
[CONTEXT.md](../../CONTEXT.md).

## Tickets

| # | Ticket | Kind | Status |
|---|---|---|---|
| 01 | [Reload builds the same services boot does](issues/01-reload-keeps-boot-wiring.md) | bug + Strong | done |
| 02 | [Add from link joins playlists through the playlist module](issues/02-add-from-link-joins-playlists.md) | bug + Strong | ready-for-agent |
| 03 | [Every player input goes through the player store](issues/03-player-inputs-through-player-store.md) | bug + Strong | ready-for-agent |
| 04 | [External streams use the cookies the yt-dlp adapter saved](issues/04-extstream-uses-ytdlp-cookies.md) | bug | ready-for-agent |
| 05 | [Skipped attempts stop counting as co-occurrence](issues/05-skipped-attempts-leave-co-occurrence.md) | bug | ready-for-agent |
| 06 | [A deleted track that is downloaded again returns to household browsing](issues/06-redownloaded-track-returns.md) | bug | needs-triage |
| 07 | [One listening judgement for play recording and Radio steering](issues/07-one-listening-judgement.md) | Strong | needs-triage |
| 08 | [One module per replicated fact, from local edit to peer apply](issues/08-replicated-fact-modules.md) | Strong | needs-triage |

## Candidates not ticketed yet

- **Track identity module** (Worth exploring). The "owned" predicate (`Status == MatchInLibrary && LibraryTrackID != ""`) is repeated at about 13 sites. About 10 artist-and-title keys each normalise differently. `trackref` dedup helpers have no production callers. There are two catalog-id minting policies.
- **Handlers as a thin adapter** (Worth exploring). `api.Deps` has 63 fields and handlers carry 84 nil-guards. Device removal, library deletion, upgrade policy and playlist covers are orchestrated inside handlers. There are 39 bespoke test-server builders and none of them boot `app.Build`.
- **Download job lifecycle as a state machine** (Worth exploring). `download.Manager` exports 23 methods. "Completion pending" is stored as a prefix on the job's `Error` string. Terminal transitions are written out by hand 15 times.
- **Recommendation surface policy at one exit** (Worth exploring). Not interested filtering happens at about 35 call sites across the recommend surfaces.
- **Frontend data module** (Worth exploring). There are 28 `*Api.ts` pass-throughs, 7 bypass `api.ts`, and generated OpenAPI types are barely used. The app has 114 literal query keys, and invalidation is copy-pasted across dialogs.
- **Projection runner split from the change log** (Speculative). `sync.MergePolicy` and `sync.SyncQuerier` each have a single adapter.
- **Adapter capabilities declared, not sniffed** (Speculative). About 25 type-assertion sites exist. `ErrNoChapterSupport` is unreachable because `download.Manager` always implements `ListChapters`.
