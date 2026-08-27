# Reverb

Reverb is a personal music app that runs on several devices at once. Every device
is the same self-hosted application; they stay in agreement by syncing against one
always-on server. It unifies an existing music library, online search, and
one-click downloads in one UI.

## Language

### Devices & topology

**Device**:
Any single running instance of Reverb (a laptop or the server).
_Avoid_: Client, node, peer

Device now includes desktop app (Wails) — same binary, local server on 127.0.0.1:0.

**Server**:
The always-on device that holds the canonical library and is the sync rendezvous
for the other devices.
_Avoid_: Hub, host

**Canonical library**:
The authoritative copy of the music collection (files plus metadata), which lives
on the server. Every device's view is a copy or subset of it.

**Pairing**:
The act of granting a laptop a token to sync with the server, done by entering a
one-time pairing code shown on the server's admin UI.

**Offline set**:
The subset of the library a laptop keeps locally so it can be played with no
internet connection. Managed per-playlist.
_Avoid_: Offline library, cache

### Downloads

**Download**:
Acquiring a track or album from an external source (Spotify, YouTube) at the
source's best available quality. Runs on whichever device is chosen, but the
result always syncs to the canonical library.
_Avoid_: Import, fetch

**Add from link**:
The action of pasting a URL (Spotify, YouTube, ...), having Reverb resolve it to a
track/album, and adding it to a playlist and/or the library.
_Avoid_: Paste link, URL import

**Source-native quality**:
Storing a downloaded file exactly as the source provides it (Spotify AAC, YouTube
opus), never transcoding. The quality rule is "best available, 256kbps or as high
as the source offers."

### Sync

**Sync**:
Reconciling library contents, playlists, and metadata edits between devices.
Bidirectional; edits merge per-field, most-recent-write wins.
_Avoid_: Mirror, replicate

**Deletion**:
Removing a playlist or a track from the canonical library, which propagates to
every device. Distinct from removing a track from a device's offline set, which is
local-only.
