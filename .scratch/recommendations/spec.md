# Recommendations

Spotify-style recommendations for Reverb, as good as open data sources allow. Terms
are defined in `CONTEXT.md` (Recommendation, Taste profile, Not interested, Radio,
Mix, Adventurousness, Tracked artist). Architecture decisions are in
`docs/adr/0001-recommendations-generated-per-device.md` and
`docs/adr/0002-mixes-are-not-playlists.md`.

## Vision

- **Goal:** discovery is the headline feature, and rediscovery of the library also
  matters. Recommendations may be outside the library. They play on demand via
  external playback and enter the library only through Add to playlist (no
  download) or Download. Both count as positive taste signals.
- **Surfaces:** Radio, similar artists and tracks on detail pages, "For you" Home
  shelves, Release Radar, Discover Weekly, playlist suggestions, Daily Mixes.
- **Taste profile:** one per household. Inputs are plays (skip, completion,
  repeat, recency), library and playlist additions, and not-interested marks.
  Not-interested marks sync to every device.
- **Candidate sources:** Last.fm (similar tracks and artists), ListenBrainz
  (similar artists and recordings), and Deezer (related artists, artist radio,
  releases). Spotify's recommendation endpoints are unavailable to new apps.
  Sources agreeing on a track is a strong signal. Sound fingerprints from a
  bundled audio model are a later signal.
- **Data sharing:** by default only seed artists and tracks are sent out. Linked
  Last.fm history is used when present. Uploading listens to ListenBrainz is
  opt-in. The "Online recommendations" switch disables all online lookups.
- **Generation:** each device generates its own recommendations (ADR 0001). Mixes
  refresh on a schedule (Discover Weekly on Monday, Release Radar on Friday, Daily
  Mixes daily, all at local midnight) with a seed shared per period. A refresh
  replaces the previous Mix (ADR 0002).
- **Behaviour:**
  - Offline, recommendations fall back to the library, and shelves show their
    last results.
  - One Adventurousness slider on top of per-surface defaults.
  - Radio steers away from what you skip within the session.
  - Every recommendation shows a short reason.
  - Tracked artists are inferred from plays and library, with no Follow button.
- **Filters:** not interested; for discovery surfaces, tracks already owned or
  recently played; duplicate versions of the same recording; live, cover, remix,
  and karaoke versions unless the seed is one; limits on repeating an artist.
- **Quality:** a test that hides recent plays and checks whether they were
  predicted, run whenever ranking changes, plus skip and add rates for
  recommendations on the Stats page.
- **Offline sets:** a Mix goes offline only by saving it as a playlist first.
