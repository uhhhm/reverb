# 04: iOS walking skeleton

**What to build:** A first iOS app on a real phone. The Go core is packaged with gomobile, which exposes lifecycle only (start with a data directory, report the loopback port, stop). A native SwiftUI app talks to the core through a Swift client generated from OpenAPI. The owner pairs by scanning the desktop's QR code or typing the code, sees playlists, marks one offline, and plays it with no network, including in the background with lock-screen controls.

**Blocked by:** 02, 03

**Status:** done

The app builds with Xcode 26, `make ios-test` passes on the iPhone 17 simulator (iOS 26.5), and every item below has been checked by hand on a real iPhone. `TestAppFlowAgainstTestPeer` covers the core side of the smoke flow on Linux.

The Swift client is generated at build time by the swift-openapi-generator plugin; `make contracts-check` checks its input, the app's OpenAPI subset. XCUITest cannot read Now Playing, so lock-screen metadata is checked by hand on a device.

- [x] The Xcode project builds on macOS and links the gomobile-packaged core, which starts and stops with the app's lifecycle
- [x] A Swift client is generated from the OpenAPI spec and drift-checked by `make contracts-check` beside the TypeScript client
- [x] Pairing works by scanning the desktop QR code with the camera, and by typed code as a fallback
- [x] Playlists list; a playlist can be marked offline, and fetch progress and storage use are visible
- [x] Offline tracks play in airplane mode through AVPlayer with simple queue playback (play, next, previous, seek)
- [x] Playback continues when the phone is locked or the app is in the background; lock screen, Control Center, and headphone controls work; playback pauses for calls and resumes after
- [x] Offline files are excluded from iCloud backup
- [x] XCUITest smoke test: launch, pair with a test runtime, play an offline track
