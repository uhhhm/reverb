# 02: Add from link joins playlists through the playlist module

**What to build:** When the owner uses Add from link and picks a managed playlist, the track shows up in that playlist on this device and on every paired device. Today it most likely appears in neither.

**Why it fails today.** linkadd writes playlist membership into the change log by hand (`internal/linkadd/service.go:492-505`): a `track:<catalogId>` field whose value is a bare catalog-id string, plus a `tracks` field. playlistcrdt expects a different format for membership fields: `track:<digest of MemberKey>` holding a JSON `member{Present, Order, Entry}` value. Its `readState` silently drops any value it can't parse (`internal/playlistcrdt/playlistcrdt.go:108`). So these changes replicate forever and are never applied.

The only other route is `DownloadRequest.AddToPlaylistID` (`linkadd/service.go:319`). That id gets passed to the library backend's `AddTracksToPlaylist` (`download/manager.go:689`), but it's a managed-playlist id, not a Subsonic playlist id. The two id spaces don't match.

**Duplicate implementation in the handler.** `internal/api/links.go:214-487` keeps a second, legacy copy of linkadd (the "Fallback for tests without planner"). It writes the same broken format and has its own copy of download planning (`linkDownloadRequests`, `links.go:560-606`). `links_test.go` pins the broken rows.

**The deepening:**
- Delete the fallback. It's pure duplication.
- linkadd adds to a managed playlist only through `playlistsync` (`AddTracks`), so membership reaches the log through `playlistcrdt.Publish`, like every other playlist edit.

**Also verify, possibly fixed by the same change.** linkadd mints fixed `trk_link_<source>_<id>` catalog ids and inserts them with no aliases (`linkadd/service.go:171-185`, `449-489`). The download manager mints through `catalog.CanonicalFor`, which looks up aliases (`internal/catalog/canonical.go:44`). A link-added track that is later downloaded therefore probably ends up with two catalog ids. If a test confirms this, mint through `catalog.CanonicalFor` instead.

**Blocked by:** None

**Status:** done

- [x] Write a failing e2e test first, on two paired devices. On device A, Add from link a YouTube or Spotify track into a managed playlist, then let the download complete. The track appears in that playlist's detail on A and, after sync, on B, exactly once and in the order it was added.
- [x] Add from link with a batch of links into one playlist gives every track in the playlist on both devices.
- [x] Nothing in `internal/linkadd` or `internal/api` calls `SyncStore.AppendChange` for a playlist.
- [x] The legacy fallback in `api/links.go` and its test fixtures are gone. The handler has one path.
- [x] A link-added track that is later downloaded resolves to a single catalog id, confirmed by a test and fixed if it fails.
- [x] `go test ./internal/linkadd ./internal/api ./internal/playlistsync ./internal/playlistcrdt ./internal/app` passes.

**Notes from implementation.**
- The e2e test is `internal/app/linkadd_e2e_test.go`. A phone whose yt-dlp stub really downloads is paired with a desktop. One link and then a batch of three are added into a playlist that already holds a track. The test checks order and state on both devices, and checks that each download's catalog id matches the link's.
- The double catalog id was confirmed. The link minted `trk_link_youtube_<id>` and the download minted a fresh `trk_…`. Tracks and albums now mint through `catalog.CanonicalFor`, so the external alias joins them. Playlist links keep a local `pl_link_` row because a catalog does not hold playlists. Existing `trk_link_` rows are left as they are.
- `DownloadRequest.AddToPlaylistID` is no longer set by add-from-link. It is still accepted by `POST /downloads`, where it names a library backend playlist.
- A batch plans its links concurrently. Each playlist then takes its links in one `AddTracks` edit, in batch order, and downloads are queued in batch order. Separate concurrent edits would have lost each other's tracks.
- An album or playlist link joins a playlist as its tracks. A desktop lists them with `WithCollectionListing` and still downloads the link whole. Without a lister, the add is refused with 422 before anything is written.
- A chapter split joins a playlist as the video, once. A chapter has no source id of its own that a paired device could stream.
- Adding to a mirrored (`mode = synced`) playlist is refused with 409 before any download is queued.
