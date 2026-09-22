# 04: iOS walking skeleton

**What to build:** A first iOS app on a real phone. The Go core is packaged with gomobile, which exposes lifecycle only (start with a data directory, report the loopback port, stop). A native SwiftUI app talks to the core through a Swift client generated from OpenAPI. The owner pairs by scanning the desktop's QR code or typing the code, sees playlists, marks one offline, and plays it with no network, including in the background with lock-screen controls.

**Blocked by:** 02, 03

**Status:** ready-for-human

Implemented on Linux; needs the maintainer's Mac for the items that only Xcode can confirm. The app, the XcodeGen project and the XCUITest are written but have not been compiled or run: run `make ios-test` (see `ios/README.md`), then check the device-only items on a real phone. `TestAppFlowAgainstTestPeer` covers the core side of the smoke flow on Linux.

The Swift client is generated at build time by the swift-openapi-generator plugin; `make contracts-check` checks its input, the app's OpenAPI subset. XCUITest cannot read Now Playing, so lock-screen metadata is checked by hand on a device.

- [ ] The Xcode project builds on macOS and links the gomobile-packaged core, which starts and stops with the app's lifecycle
- [ ] A Swift client is generated from the OpenAPI spec and drift-checked by `make contracts-check` beside the TypeScript client
- [ ] Pairing works by scanning the desktop QR code with the camera, and by typed code as a fallback
- [ ] Playlists list; a playlist can be marked offline, and fetch progress and storage use are visible
- [ ] Offline tracks play in airplane mode through AVPlayer with simple queue playback (play, next, previous, seek)
- [ ] Playback continues when the phone is locked or the app is in the background; lock screen, Control Center, and headphone controls work; playback pauses for calls and resumes after
- [ ] Offline files are excluded from iCloud backup
- [ ] XCUITest smoke test: launch, pair with a test runtime, play an offline track
