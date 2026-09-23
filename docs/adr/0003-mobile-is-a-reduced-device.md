# Mobile is a reduced Device

The iOS app is a Device, not a remote control. It runs the same Go core through
gomobile (SQLite, change log, CRDT sync, libp2p pairing and file sync), pairs as a
peer, and keeps its own offline set, plays, and edits while no other device is
reachable. A thin client would be simpler, but it would have nothing when the PC is
out of reach, and it would need a second way for offline plays and edits to reach
the change log.

It is reduced because iOS cannot run the desktop's bundled tools as subprocesses:

- **Library backend**: a pure-Go local-files library adapter, registered at the
  composition root and held to the `library` conformance suite, serves the offline
  set. There is no Navidrome on the phone.
- **Downloads and external playback**: yt-dlp and spotdl run in-process in an
  embedded Python, with QuickJS as yt-dlp's JavaScript runtime and ffmpeg linked
  in. The phone downloads new yt-dlp packages itself and uses one only after
  verifying a signature from the release pipeline, since YouTube breaks yt-dlp
  more often than the app is re-signed.
- **Library tracks outside the offline set** are streamed from a peer through a
  Delegated request, since the files live on another device. Search,
  recommendations, and Mixes use HTTP sources and run on the phone.

The UI is native SwiftUI, a mobile subset of the desktop (no adapter
configuration, metadata editing, crop, portable-name migration, or Stats). It talks
to the core over the core's loopback HTTP and WebSocket API through a Swift client
generated from `internal/api/openapi.yaml`. gomobile starts and stops the core and
passes it a data directory, and hands back a port and a per-launch secret the
loopback API requires on every request, since other apps on the phone can reach
a loopback port. The Spotify secret copied from a paired desktop also crosses
the binding, into the Keychain, so it is never served over loopback HTTP. A
later Android app follows the same shape with a generated Kotlin client.

Queue and Radio policy move out of the web player into the Go core, so the desktop
SPA and iOS are thin players over one implementation rather than two that drift
apart.

The phone is reachable on the LAN and over a VPN the owner runs (such as
Tailscale), using stored multiaddrs as desktop devices already do. Reverb runs no
relay. iOS suspends the core in the background unless audio is playing, so the
phone syncs when opened, while playing, and in background refresh windows, and it
shows its sync status.

## Consequences

- A Download made on the phone plays at once and stays on the phone, marked pending
  upload, until a peer confirms it holds the file. After that the file stays only
  if it belongs to an offline playlist. A pending upload is never evicted.
- The phone's offline set is chosen per playlist, like the desktop's, and stays
  local-only. Its files are excluded from iCloud backup. When storage is full, new
  downloads stop and the app says so; nothing is evicted silently.
- Moving player policy into the core is a desktop refactor that Radio on iOS
  depends on.
- Building the app needs macOS and Xcode. Logic that lives in the Go core stays
  testable on Linux, which is a reason to keep the Swift layer thin.
