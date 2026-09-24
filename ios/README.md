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
| `ReverbTests/` | Native AVPlayer regressions: decoded duration, variable-bitrate MP3, heard audio against the clock after seeks, queue races, failure recovery and skipping, interruptions and completion |
| `ReverbUITests/` | XCUITest smoke test: launch, pair with a test runtime by typed code or a confirmed pairing link, play an offline track |
| `native/` | The embedded download tools: `fetch.sh` fetches CPython for iOS, FFmpeg and QuickJS-ng at pinned checksums, `build.sh` builds them into `libreverbnative` and installs the hash-pinned `requirements.txt` (yt-dlp), and `install-python.sh` is the build phase that puts Python into the app |
| `Frameworks/` | `Reverbcore.xcframework` from `make ios-core`, and `Python.xcframework` and `ReverbNative.xcframework` from `make ios-native` (not checked in) |

The Go side is `mobile/reverbcore`: `Start(dataDir)` returns the loopback port,
`Port()`, `Secret()`, and `Stop()`. `ConfigurePython(home, packages)` names the
bundle's Python before `Start`. Other apps on the phone can reach a
loopback port, so each start makes a random secret, kept in memory only, and
the core refuses every request without it in `X-Reverb-Secret`. `LocalCore`
sends it with generated operations, raw requests (`request(_:)`) and AVPlayer
streams (`streamHeaders`); `AsyncImage` cannot, so covers load through
`CoverImage`. `CopySpotifyCredentials()` and `SetSpotifyCredentials(id, secret)`
also cross gomobile so the Spotify secret is never exposed through loopback
HTTP, and `InspectPairPayload(link)` reads a pairing link without dialling it;
other operations use the generated API client.

## Building

Needs macOS with Xcode 16 or later, the Go version in `go.mod`, XcodeGen
(`brew install xcodegen`), and Python 3.14 (`brew install python@3.14`), which
installs the bundled packages and compiles them. From the repository root,
first the embedded download tools (several minutes the first time; later runs
reuse the build):

```bash
make ios-native
```

Then the core, which links them:

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

Everything in the core is tested on Linux with `make check`. The embedded
Python runner, with in-process ffmpeg, ffprobe and QuickJS, is tested on a Mac
against its Python 3.14 and the bundled yt-dlp; `REVERB_TEST_NETWORK=1` adds a
real YouTube resolve that solves the JavaScript challenge with QuickJS:

```bash
make test-pyembed
```
 That includes
`cmd/reverb-testpeer`'s `TestAppFlowAgainstTestPeer`, which drives the linked
core through the same calls the app makes. On a Mac, native playback regressions and the UI smoke test run on
a simulator against the test peer:

```bash
make ios-test
```

`make ios-test` builds the core and the project, starts `reverb-testpeer` on
127.0.0.1:47300 (the simulator shares the Mac's loopback), and runs the
native tests and XCUITest. The smoke test checks clock progress, duration,
pause, seek, resume and completion while the paired device is stopped. Set `IOS_DESTINATION` for another simulator. To run the test from
Xcode instead, start `make ios-testpeer` first.

For a faster playback-only loop after building the core and generating the project:

```bash
xcodebuild test -project ios/Reverb.xcodeproj -scheme Reverb \
  -destination 'platform=iOS Simulator,name=iPhone 17' \
  -skipPackagePluginValidation -only-testing:ReverbTests
```

## Behaviour worth knowing

- The data directory is `Application Support/Reverb`. Its `music/` folder holds
  the offline set and is excluded from iCloud and device backups; the database
  is backed up.
- After pairing, unpairing, and each foreground sync, the phone copies an enabled
  Spotify source's credentials from a paired desktop over its trusted P2P
  connection. They stay in this device's Keychain (not in sync or backups) and
  reach the core in memory, so phone search can use Spotify without a connected
  desktop. The copy is forgotten once a paired desktop answers without
  credentials or no device is paired. Unpairing cannot recall a copy already
  made; rotate the Spotify secret for that. The Keychain item belongs to the
  signing team, so re-signing under another Apple ID drops it until the next
  sync. Deezer requires no credentials.
- Audio keeps playing locked and in the background (`UIBackgroundModes`
  `audio`). A call pauses playback and it resumes afterwards if it was playing
  and iOS says it should; unplugging headphones pauses.
- Home, Library, Search, Playlists, and Devices are native views over the
  phone's loopback API. Library tracks play locally or through a reachable
  paired device. Search results and recommendations outside the library
  stream from their source: the core resolves them with the bundled yt-dlp,
  asking for M4A since AVPlayer cannot open WebM, and proxies the audio. The top
  search results and the next two tracks in the queue are resolved ahead of
  time, so they start at once.
- yt-dlp, the standard library and FFmpeg add about 28 MB to the IPA and 81 MB
  installed. yt-dlp and its JavaScript challenge solver run in the app's own
  Python; ffmpeg, ffprobe and QuickJS, which yt-dlp would start as processes,
  run in-process. spotDL is not bundled (see ADR 0003).
- The app syncs when it enters the foreground and every five minutes while
  audio is playing. It also schedules a bounded `BGAppRefreshTask`; iOS decides
  whether and when that task runs, and Reverb uses no keep-alive workaround.
- iOS may reclaim the core's listening socket while the app is suspended. When
  the app comes back to the foreground it checks the core answers and starts it
  again if not. A track that fails to load, such as one played from the lock
  screen after that, is loaded once more after the same check.
- A track AVPlayer cannot open is skipped with a message: WebM is the one
  format Reverb indexes that iOS cannot play; the downloaders write MP3, M4A and
  Ogg Opus. Three failures in a row stop playback rather than walk the queue.
- Scanning a pairing QR code with the system Camera opens the app through the
  `reverb://` URL scheme. Any app or web page can open such a link, so a link,
  like a code scanned in the app, first shows the device's peer ID and
  addresses, each marked LAN/VPN or public, with a warning when none is local.
  Nothing is dialled or redeemed until Pair is tapped; Cancel or dismissing the
  sheet discards it. A typed code pairs directly.
