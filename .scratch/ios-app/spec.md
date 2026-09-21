Status: ready-for-agent

# iOS app

A Reverb app for iPhone that is a full, if reduced, Device. Terms are defined in
`CONTEXT.md` (Device, Pairing, Offline set, Download, Delegated request, Radio,
Mix). Architecture decisions are in `docs/adr/0003-mobile-is-a-reduced-device.md`
and `docs/adr/0004-ios-distribution-via-sideloading.md`.

## Problem Statement

Reverb exists only on computers. Away from the desk the owner has no way to play
their library, their playlists, or the recommendations Reverb makes for them. A
phone app that only streams from the PC would not help, since the PC is usually
out of reach when the owner is out. Reverb is self-hosted and downloads from
YouTube and Spotify, so it cannot go on the App Store. Owners who self-host
Reverb need to install and keep a sideloaded app working without re-signing and
reinstalling by hand every week.

## Solution

An iOS app that runs the same Go core as the desktop app and pairs with the
owner's other devices as a peer. It keeps its own offline set of playlists, plays
them with no connection, and records plays and edits that sync back on the LAN or
over the owner's own VPN. The app searches, recommends, plays external tracks,
and downloads on its own, using an embedded yt-dlp. Library tracks that are not on
the phone stream from a paired device through a Delegated request.

Each release publishes an unsigned IPA and a SideStore/AltStore source. The owner
adds the source once with a free Apple ID. From then on the tool re-signs the app
before it expires and shows new versions as updates.

## User Stories

### Installing and updating

1. As an owner, I want to install Reverb on my iPhone from the project's releases using SideStore or AltStore with a free Apple ID, so that I don't need a paid developer account.
2. As an owner, I want to add Reverb's source to SideStore once, so that new releases appear as updates instead of manual IPA downloads.
3. As an owner, I want SideStore to re-sign Reverb automatically before the 7-day signature expires, so that the app doesn't stop launching.
4. As an owner with a paid developer account, I want to sign the same IPA myself, so that I get a year-long signature.
5. As an owner, I want documentation that walks me through first-time sideloading step by step, so that I can install it without knowing iOS development.
6. As an owner, I want a non-blocking banner when a newer Reverb release exists, so that I know to update without being locked out.
7. As an owner, I want my phone to keep syncing with desktop devices that are a few releases newer, so that a delayed update doesn't break sync.
8. As an owner, I want a clear message when my phone is outside the supported version window, so that I know updating fixes it.
9. As an owner, I want the app's data to survive a missed re-sign, so that re-signing restores everything as it was.

### Pairing

10. As an owner, I want my desktop app to show a pairing QR code, so that I can pair my phone by pointing the camera at it.
11. As an owner, I want the QR code to carry my desktop's LAN and VPN addresses, so that pairing works even where mDNS can't find the device.
12. As an owner, I want to type the pairing code instead of scanning, so that I can pair when the camera isn't convenient.
13. As an owner, I want pairing to keep the same proof of possession as desktop pairing, so that the code never crosses the network.
14. As an owner, I want to see which devices my phone is paired with and when each was last reached, so that I know what it syncs with.
15. As an owner, I want to unpair a device from the phone, so that a lost or retired computer stops syncing with it.

### Sync

16. As an owner, I want playlists, library metadata, plays, Not interested marks, and recommendation settings to sync between my phone and my other devices, so that the phone is the same Reverb.
17. As an owner, I want plays and playlist edits I make while offline to merge when the phone reconnects, so that nothing I do on the go is lost.
18. As an owner, I want concurrent edits on the phone and PC to the same playlist to both survive, so that syncing never silently drops a track.
19. As an owner, I want the phone to sync when I open it and while music is playing, so that it is current whenever I use it.
20. As an owner, I want the phone to sync opportunistically in the background when iOS allows, so that it is often current before I open it.
21. As an owner, I want to see sync status (last sync, in progress, failed, which device), so that "not synced yet" is visible, not mysterious.
22. As an owner, I want to trigger a sync manually, so that I can force it before leaving the house.
23. As an owner, I want the phone to reach my devices over Tailscale or another VPN I run, so that it syncs away from home without a hosted relay.
24. As an owner, I want deletions of playlists and tracks made on any device to reach the phone, so that it doesn't keep showing things I removed.

### Offline set

25. As an owner, I want to mark playlists as offline on the phone, so that their tracks are downloaded to it.
26. As an owner, I want the phone's offline choices to be independent of my laptop's, so that each device keeps what suits its storage.
27. As an owner, I want offline tracks to play with no network and no reachable peer, so that I have music on a plane.
28. As an owner, I want tracks added to an offline playlist on another device to arrive on the phone at its next sync, so that the offline copy stays current.
29. As an owner, I want tracks removed from an offline playlist to be removed from the phone's storage, so that space isn't wasted.
30. As an owner, I want to see how much storage the offline set uses, per playlist and in total, so that I can decide what to keep.
31. As an owner, I want new downloads to stop with a clear message when the phone is full, so that nothing is evicted without my say.
32. As an owner, I want offline files excluded from iCloud backup, so that my phone backups don't carry my music library.
33. As an owner, I want each offline track's download progress visible, so that I know whether a playlist is ready before I leave.

### Playback

34. As an owner, I want playback to continue with the screen locked and the app in the background, so that it behaves like a normal music app.
35. As an owner, I want lock-screen and Control Center controls with artwork, so that I can control playback without opening the app.
36. As an owner, I want headphone and car controls (play, pause, next, previous, seek) to work, so that I can use Reverb hands-free.
37. As an owner, I want playback to pause for calls and resume after them, so that it follows iOS conventions.
38. As an owner, I want the same queue behaviour as on desktop, so that the phone doesn't surprise me.
39. As an owner, I want to play library tracks that aren't on the phone by streaming them from a paired device, so that my whole library is reachable when a device is.
40. As an owner, I want tracks that can't be played right now (not on the phone, no peer reachable) to be marked, so that I don't tap into silence.
41. As an owner, I want my plays on the phone to count toward my taste profile and stats, so that recommendations reflect everything I listen to.
42. As an owner, I want Radio on the phone to behave like Radio on desktop, including skip steering, so that it learns during the session.

### Search, recommendations, external playback

43. As an owner, I want to search Deezer and Spotify from the phone without a peer, so that search works anywhere with internet.
44. As an owner, I want Home shelves and Mixes on the phone, so that discovery isn't desktop-only.
45. As an owner, I want Home to show the last Mixes with an offline note when there's no internet, so that the screen is never empty.
46. As an owner, I want to play a search result or recommendation that isn't in my library, so that I can listen before deciding to keep it.
47. As an owner, I want external playback to work on the phone without a peer, so that discovery works on cellular away from home.
48. As an owner, I want to mark a track or artist Not interested from the phone, so that I can reject a recommendation where I hear it.
49. As an owner, I want to save a Mix as a playlist from the phone, so that I can keep one I like.

### Downloads and Add from link

50. As an owner, I want to download a track or album on the phone, so that I can grow my library away from my computer.
51. As an owner, I want a track downloaded on the phone to play on the phone at once, so that I don't wait for a sync.
52. As an owner, I want the phone to keep a download until a paired device confirms it holds the file, so that the canonical library never loses it.
53. As an owner, I want to see which downloads are still pending upload, so that I know not to delete the app yet.
54. As an owner, I want a phone download to leave the phone after it's uploaded unless it's in an offline playlist, so that storage isn't used for music I didn't ask to keep there.
55. As an owner, I want to paste or share a Spotify or YouTube link into Reverb, so that Add from link works from the phone.
56. As an owner, I want to add a link's tracks to a playlist from the phone, so that the playlist updates on every device.
57. As an owner, I want the phone's yt-dlp to update itself when YouTube breaks it, so that external playback recovers without a new app release.
58. As an owner, I want the phone to refuse a yt-dlp update not signed by the Reverb release pipeline, so that it never runs code from an unknown source.

### Library and playlists

59. As an owner, I want to browse artists, albums, and tracks of my library on the phone, so that I can find things the way I do on desktop.
60. As an owner, I want to create, rename, reorder, and delete playlists on the phone, so that I can manage playlists anywhere.
61. As an owner, I want to add and remove tracks from playlists on the phone, so that edits sync to all my devices.
62. As an owner, I want library views on the phone to update after incoming syncs, so that I'm not looking at stale data.

### Desktop parity boundaries

63. As an owner, I want adapter configuration, metadata editing, crop, portable-name migration, and Stats to stay on desktop, so that the phone UI stays focused.
64. As an owner, I want the effects of desktop-only actions (renamed tracks, crops) to show on the phone, so that desktop-only features still reach it.

### Maintainer

65. As the maintainer, I want the phone to run the same Go core as desktop, so that sync, pairing, and recommendations are implemented once.
66. As the maintainer, I want the iOS app's API client generated from the OpenAPI spec, so that contract drift fails a check.
67. As the maintainer, I want queue and Radio policy to live in the Go core, so that desktop, iOS, and later Android can't drift.
68. As the maintainer, I want CI to build the unsigned IPA and publish the SideStore source with each release, so that releasing iOS is not a manual step.
69. As the maintainer, I want core logic testable on Linux, so that most work doesn't need the Mac.
70. As the maintainer, I want the design to leave room for an Android app on the same core, so that it doesn't need a rewrite.

## Implementation Decisions

### Shape

- The phone is a Device (ADR 0003). It runs the shared composition root with a phone profile:
  - the local-files library adapter instead of Navidrome/Subsonic;
  - in-process download tools instead of bundled executables;
  - no desktop-only services such as the background agent or desktop updater.
- The core is packaged for iOS with gomobile. The gomobile surface covers lifecycle only: start with a data directory, report the loopback port, and stop. Everything else goes over the core's existing loopback HTTP and WebSocket API.
- The UI is native SwiftUI. It uses a Swift client generated from the OpenAPI spec (swift-openapi-generator), regenerated and drift-checked by the existing contracts targets beside the TypeScript client.
- The Swift layer owns only the platform:
  - AVPlayer playback of URLs the core provides;
  - the audio session and background audio mode;
  - Now Playing and remote-command integration;
  - camera QR scanning, the share-sheet entry for links, and background refresh scheduling;
  - excluding offline files from iCloud backup.
- The loopback guards stay: the API binds to loopback on the phone, and the existing Host/Origin guards apply. The native client identifies itself the same way a local caller does.

### New or changed modules

- **Local-files library adapter**: a pure-Go `library` adapter over a directory of files. It indexes tags and serves streams and art. It is registered explicitly at the composition root and passes the `library` conformance suite. On the phone it holds the offline set plus pending-upload downloads. Android and headless devices can also use it.
- **Offline set, per device**: per-playlist selection, stored locally and never replicated, as today. Adds:
  - fetching and pruning files on the phone through the existing P2P file sync;
  - storage accounting, per playlist and in total;
  - a storage-full state that stops fetching and never evicts silently.
- **Delegated request**: a new versioned libp2p protocol, authenticated by existing peer trust. v1 carries one request type: stream a library track by catalog id. The phone picks the target in this order: the always-on Server if reachable, otherwise the most recently reached paired device. Streams resolve through the peer's library adapter; the phone never needs backend ids.
- **Pending upload**: a Download made on the phone is kept locally and flagged until a paired device confirms it holds the file (the confirmation comes through the existing file-manifest exchange). After that, it is removed unless it belongs to an offline playlist. Pending uploads are never pruned and are exposed through the API for the sync status screen.
- **In-process download tools**:
  - yt-dlp and spotdl run in an embedded Python, with QuickJS as yt-dlp's JavaScript runtime and ffmpeg linked in;
  - both sit behind a "Python runner" interface, so the same downloader adapters run against host Python on Linux for tests;
  - the adapters register through the existing downloader registry and keep source-native quality;
  - external playback uses the same resolver path desktop uses.
- **yt-dlp self-update**: the release pipeline signs yt-dlp packages. The phone downloads a new package and activates it only after verifying the signature. The previous package is kept until the new one works.
- **Core-owned player policy**:
  - queue state and Radio session policy move from the web player into a core service with an HTTP/WebSocket API; that policy is three tracks ahead, refill seeds, skip = "played under half", artist steering, no third consecutive track by one artist, prewarm, and end conditions;
  - the desktop SPA and iOS become thin players that report position, skips, and completions and play what the core says is next;
  - this is a desktop refactor and ships before Radio on iOS.
- **Pairing QR payload**: the desktop pairing screen renders a QR code with a versioned payload of the pairing code plus the responder's reachable multiaddrs (LAN and VPN). The phone redeems it through the existing mutual challenge-response. Typed-code redemption stays as a fallback.
- **Version compatibility**:
  - pairing, sync, file, cover, and Delegated request protocols keep their version in the libp2p protocol id;
  - a declared support window (a number of minor releases) states which older protocol versions a release still serves;
  - the core reports the newest known release, and a peer's version, through the API;
  - the phone shows a non-blocking update banner, and a clear message when outside the window.
- **Background behaviour**: sync runs on foreground, during playback, and in iOS background refresh windows. Offline-set fetching runs only while the app is active or playing. No keep-alive tricks.

### Distribution

- CI builds an unsigned IPA on a macOS runner for each release. It publishes the IPA and a SideStore/AltStore source JSON (app metadata, versions, download URL) as release assets at a stable URL.
- Documentation covers two paths: SideStore with a free Apple ID (primary) and self-signing with a paid account.

### Delivery order

1. Go core on iOS, local-files adapter, QR pairing, sync, offline set playback, minimal SwiftUI app.
2. Local search and recommendations, Delegated request streaming, the remaining mobile screens.
3. Embedded Python, yt-dlp, spotdl, QuickJS, and ffmpeg: external playback, downloads, Add from link, pending upload, yt-dlp self-update.
4. Player policy moved into the core (desktop refactor), then Radio on iOS.
5. Release pipeline: IPA, SideStore source, update banner, compatibility window.

## Testing Decisions

- Good tests exercise external behaviour through the highest seam: the core's HTTP API and the libp2p wire between real runtimes. They don't reach into internal tables or helper functions. A test should fail only when owner-visible behaviour breaks.
- **Main seam: multi-runtime e2e harness.** The existing two-device test boots full runtimes from the shared composition root, pairs them over loopback libp2p, and drives them through HTTP. Extend it with a phone-profile runtime and cover:
  - pairing from a QR payload, and by typed code;
  - a desktop playlist marked offline on the phone arriving there, then playing with the desktop runtime stopped;
  - plays, playlist edits, and Not interested marks made on the phone while the desktop runtime is stopped, converging after restart;
  - streaming a non-offline track through a Delegated request, and the "unavailable" answer when no peer is reachable;
  - the pending-upload lifecycle: download on the phone, the desktop confirms it holds the file, the phone prunes it, or keeps it if it's in an offline playlist;
  - the storage-full state stopping fetches without evicting anything;
  - a runtime speaking an older protocol version inside the window still syncing, and one outside it being reported, not silently ignored.
- **Player policy** is tested on a single runtime through its new API: queue advance, Radio refill and steering, and skip semantics. The existing web player tests are the prior art for the behaviours to carry over.
- **Conformance suites:**
  - the local-files adapter passes the existing `library` conformance suite;
  - the in-process yt-dlp and spotdl downloaders pass the existing `download` conformance suite on Linux against host Python, through the Python runner interface.
- **yt-dlp self-update** tests verify that an unsigned or wrongly signed package is refused, and that a failed activation leaves the previous package in use.
- **Contracts:** the generated Swift client is drift-checked alongside the TypeScript client.
- **Swift layer:** only XCUITest smoke tests on macOS (launch, pair with a test runtime, play an offline track, lock-screen metadata present). The Swift layer stays thin enough that core tests carry correctness.

## Out of Scope

- The App Store, TestFlight, and EU alternative marketplaces.
- A hosted relay or any Reverb-run service for reaching devices away from home.
- Offline selection by album or single track (per-playlist only, as on desktop).
- Adapter configuration, metadata editing, crop, portable-name migration, and Stats on the phone.
- Keeping the core alive in the background beyond what iOS grants.
- The Android app (the design only leaves room for it).
- iPad-specific layouts, CarPlay, widgets, and Apple Watch.
- Accounts or per-person profiles; the product keeps one household owner.

## Further Notes

- Building needs macOS and Xcode. The maintainer has a Mac for development, and CI uses macOS runners for releases.
- Embedding Python, QuickJS, and ffmpeg adds roughly 50–100 MB to the IPA. That is acceptable for a sideloaded app, but worth tracking.
- QuickJS was chosen because it is an interpreter (sideloaded apps can't use JIT) and yt-dlp can use it as a JavaScript runtime. Confirm yt-dlp's current list of supported runtimes before starting stage 3.
- Facts about signing lifetimes, SideStore, and Python's iOS support are as of 2026-09 and should be rechecked when the release work starts.
