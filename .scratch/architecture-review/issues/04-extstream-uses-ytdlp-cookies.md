# 04: External streams use the cookies the yt-dlp adapter saved

**What to build:** The cookies.txt the owner pastes into the yt-dlp adapter settings is used both for downloads and for playing external tracks.

The two paths disagree today:
- The yt-dlp adapter writes the file to `<UserConfigDir>/reverb/ytdlp-cookies.txt` (`internal/download/ytdlp/adapter.go:170-176`).
- extstream reads `<UserConfigDir>/yt-dlp/cookies.txt` (`internal/extstream/extstream.go:161-168`), and its comment says it shares the adapter's file.

So a YouTube stream that needs cookies (age-gated, or throttled without sign-in) plays for downloads but fails when streamed.

The path belongs to one owner. The adapter package exposes where its cookies live, and extstream asks it, instead of each package rebuilding the path.

**Also worth checking while here.** For `source == "youtube"` extstream still searches by artist and title (`extstream.go:349`), while `ytdlp.buildQuery` uses the video id directly (`ytdlp/adapter.go:225-237`). Streaming a YouTube result by its id would stop it landing on a different upload.

**Blocked by:** None

**Status:** done

- [x] Write a failing test first. After the adapter persists cookies, the extstream runner is invoked with `--cookies` pointing at that same file.
- [x] One function defines the cookies path, and both packages use it.
- [x] Resolving a YouTube external track uses its video id rather than a text search. If that is left out, record why in a comment on this ticket.
- [x] `go test ./internal/extstream ./internal/download/...` passes.

**Notes from implementation.**
- A YouTube result always resolves its own video id. A video id or URL stored for it by an earlier artist-and-title search is ignored, because that search may have landed on another upload.
- A YouTube result whose video no longer resolves (pulled or region-locked) fails. It does not fall back to a text search, because any hit would be a different upload. Other sources still fall back from a stale stored id to searching.
