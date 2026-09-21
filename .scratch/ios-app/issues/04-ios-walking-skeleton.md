# 04: iOS walking skeleton

**What to build:** A first iOS app on a real phone. The Go core is packaged with gomobile, which exposes lifecycle only (start with a data directory, report the loopback port, stop). A native SwiftUI app talks to the core through a Swift client generated from OpenAPI. The owner pairs by scanning the desktop's QR code or typing the code, sees playlists, marks one offline, and plays it with no network, including in the background with lock-screen controls.

**Blocked by:** 02, 03

**Status:** ready-for-agent

- [ ] The Xcode project builds on macOS and links the gomobile-packaged core, which starts and stops with the app's lifecycle
- [ ] A Swift client is generated from the OpenAPI spec and drift-checked by `make contracts-check` beside the TypeScript client
- [ ] Pairing works by scanning the desktop QR code with the camera, and by typed code as a fallback
- [ ] Playlists list; a playlist can be marked offline, and fetch progress and storage use are visible
- [ ] Offline tracks play in airplane mode through AVPlayer with simple queue playback (play, next, previous, seek)
- [ ] Playback continues when the phone is locked or the app is in the background; lock screen, Control Center, and headphone controls work; playback pauses for calls and resumes after
- [ ] Offline files are excluded from iCloud backup
- [ ] XCUITest smoke test: launch, pair with a test runtime, play an offline track
