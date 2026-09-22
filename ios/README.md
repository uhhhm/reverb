# Reverb for iPhone

A native SwiftUI app around the same Go core as the desktop (ADR 0003). The
core runs inside the app as a phone-profile Device: it pairs, syncs, keeps the
offline set, and serves its API and streams on 127.0.0.1. The Swift layer only
does what iOS alone can: playback through AVPlayer, the audio session, the lock
screen and remote controls, the camera, and backup exclusion.

## Layout

| Path | What |
| --- | --- |
| `project.yml` | XcodeGen spec; `make ios-project` writes `Reverb.xcodeproj` from it |
| `Reverb/` | The app: `Core/` starts and stops the Go core, `Player/` plays the core's queue, `Views/` |
| `ReverbKit/` | Swift package with `ReverbAPI`, the client generated from OpenAPI |
| `ReverbUITests/` | XCUITest smoke test: launch, pair with a test runtime, play an offline track |
| `Frameworks/` | `Reverbcore.xcframework`, built by `make ios-core` (not checked in) |

The Go side is `mobile/reverbcore`: `Start(dataDir)` returns the loopback port,
`Port()`, and `Stop()`. Nothing else crosses gomobile.

## Building

Needs macOS with Xcode 16 or later, the Go version in `go.mod`, and XcodeGen
(`brew install xcodegen`). From the repository root:

```bash
make ios-core
```

```bash
make ios-project
```

Then open `ios/Reverb.xcodeproj`. The first build asks you to trust the
`OpenAPIGenerator` package plugin, which generates the API client. To run on a
phone from Xcode, set your team under Signing & Capabilities; releases are
unsigned IPAs that SideStore signs (ADR 0004).

## The API client

`ReverbKit/Sources/ReverbAPI/openapi.yaml` is the subset of
`internal/api/openapi.yaml` the app calls, written by `make contracts`, which
also names each operation. swift-openapi-generator turns it into `Client` at
build time. `make contracts-check` fails when the subset is stale, so a change
to an operation the app uses cannot land without refreshing it. To call a new
operation, add it to `iosOperations` in `tools/contracts/generate.mjs` and run
`make contracts`.

## Testing

Everything in the core is tested on Linux with `make check`. That includes
`cmd/reverb-testpeer`'s `TestAppFlowAgainstTestPeer`, which drives the linked
core through the same calls the app makes. On a Mac, the UI smoke test runs on
a simulator against the test peer:

```bash
make ios-test
```

`make ios-test` builds the core and the project, starts `reverb-testpeer` on
127.0.0.1:47300 (the simulator shares the Mac's loopback), and runs the
XCUITest. Set `IOS_DESTINATION` for another simulator. To run the test from
Xcode instead, start `make ios-testpeer` first.

## Behaviour worth knowing

- The data directory is `Application Support/Reverb`. Its `music/` folder holds
  the offline set and is excluded from iCloud and device backups; the database
  is backed up.
- Audio keeps playing locked and in the background (`UIBackgroundModes`
  `audio`). A call pauses playback and it resumes afterwards if it was playing;
  unplugging headphones pauses.
- iOS may reclaim the core's listening socket while the app is suspended. When
  the app comes back to the foreground it checks the core answers and starts it
  again if not.
- Scanning a pairing QR code with the system Camera opens the app through the
  `reverb://` URL scheme and pairs.
