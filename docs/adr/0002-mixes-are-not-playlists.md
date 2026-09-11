# Mixes are not playlists

Discover Weekly, Release Radar, and Daily Mixes are regenerated views, not managed
playlists. Storing them as playlists would put a churn of replicated edits in the
change log on every refresh (and on every device, per ADR 0001). It would also
blur "playlist" to mean something Reverb made rather than something the owner made.
"Save as playlist" makes an ordinary playlist copy, and that copy is what can join
an offline set.

## Consequences

A Mix has no history. Each refresh replaces the previous one. Features that work on
playlists (offline sets, sync, reordering) apply to a Mix only after it is saved as
a playlist.
