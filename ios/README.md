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
| `ReverbShare/` | The share extension: a link shared from another app opens Add from link as `reverb://add?url=…` |
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

## Installing on an iPhone

Reverb is not on the App Store (ADR 0004). Each release attaches an unsigned
`Reverb.ipa`, and stable releases update a SideStore/AltStore source at a stable
URL:

```text
https://github.com/uhhhm/reverb/releases/download/ios-source/source.json
```

### SideStore with a free Apple ID (recommended)

A free Apple ID signs apps for 7 days; SideStore re-signs them on the phone
before they expire, with no computer after setup.

1. Follow SideStore's own install guide (sidestore.io) once. It installs
   SideStore with your Apple ID and a pairing file from a computer, and has
   you enable Developer Mode on the iPhone.
2. In SideStore, open **Sources**, tap **+**, and add the source URL above.
3. Open the Reverb source and tap **Free** (or **Get**) to install Reverb.
4. Keep SideStore's background refresh on, or open SideStore once a week, so
   it re-signs Reverb before the 7 days run out.

A free Apple ID can sign three apps at once. Reverb uses two of them: the app
and its share extension (Add from link). SideStore counts itself as well.

New releases appear under SideStore's **Updates**; the app also shows a banner
when one exists. A missed re-sign stops Reverb from launching but keeps its
data: re-signing restores it as it was. Deleting the app deletes the phone's
database, offline set and any Downloads still pending upload.

AltStore works the same way with the same source, using AltServer on a
computer on your network to re-sign.

### Signing it yourself with a paid developer account

A paid Apple Developer account signs for a year. Download `Reverb.ipa` from the
release and re-sign it with your team, for example with Xcode's
`xcodebuild -exportArchive` from a local archive, or a tool such as
`fastlane resign`. Register two App IDs (`<your prefix>.reverb` and
`<your prefix>.reverb.share`) and sign the extension in `PlugIns/` too. You can
instead build from source: set your team under Signing & Capabilities and run
on the device from Xcode. Reinstall each new release the same way.

### Staying compatible

The phone often runs an older release than desktops, which update themselves.
Each release still serves the protocol versions of the release before it
(`p2p.SupportWindow`), so a phone one minor release behind keeps pairing,
syncing, fetching files and covers, and streaming. Devices lists each paired
device's Reverb version. A device outside the window is named there and in a
banner, with what to update; nothing is dropped silently.

### Publishing a release

`.github/workflows/release.yml` builds the IPA on a macOS runner for every
published release (`make ios-native`, `make ios-core`, `make ios-project`, then
an unsigned `xcodebuild archive` zipped into `Payload/`). `MARKETING_VERSION`
comes from the tag. For stable releases, `scripts/ios-source.mjs` adds the
version to the previous `source.json` (keeping the last ten, newest first) and
the job uploads it to the `ios-source` channel release. That release is a
prerelease so it never becomes "latest", which the desktop updater and the
phone's banner read. The phone's banner offers a release only once its IPA is
attached.

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
- Search results and recommendations can be downloaded from their context
  menu, and Add from link (Playlists, the share sheet, or `reverb://add?url=`)
  adds a Spotify or YouTube link to a playlist and/or downloads it; an album or
  playlist link is added track by track. A Download lands in the phone's
  library folder as M4A where the source has it, and plays at once. It stays
  on the phone, listed under Devices as waiting to upload, until a paired
  device's manifest shows it holds the file; then it is removed unless an
  offline playlist names it. Nothing removes a Download before that.
- The share extension cannot open the app from every host app; when it cannot,
  it copies the link and says to paste it in Add from link.
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

### Signed yt-dlp updates

The phone checks the `ytdlp-channel` GitHub release on launch and at most once
per day on foreground activation. Devices → External playback shows the loaded
yt-dlp version and offers a manual check. The package is a pure-Python yt-dlp
wheel; its dependencies, native tools and interpreter remain bundled in the IPA.
An update needing newer dependencies must wait for a compatible IPA.

`internal/ytdlpupdate` verifies an Ed25519 signature over the exact manifest
bytes, then the archive SHA-256, before extracting or executing anything. The
manifest carries format `1`, a monotonic release sequence and the Python version
string. Extraction rejects traversal, symlinks and native extensions and bounds
both compressed and expanded size. Activation waits for Python jobs, replaces
cached yt-dlp imports, builds its extractor registry and instantiates YoutubeDL
with QuickJS. An import/probe failure restores the old imports and search path.
The active marker is replaced atomically only after that succeeds. The previous
signed archive is kept; startup re-verifies archives and falls back to it (then
the bundle) if the current one is corrupt or incompatible. This is a local
compatibility probe, not a guarantee that every upstream website works.

The dedicated `.github/workflows/ytdlp-release.yml` workflow can publish fixes
without building an IPA. Dispatch it on `main` with the reviewed PyPI version and
wheel SHA-256. It checks the hash, signs the wheel, publishes immutable release
assets, and refreshes the three channel assets. A check during publication may
fail signature/digest verification; the phone retains its current version and a
manual retry works after publication finishes.

The app embeds `mobile/reverbcore/ytdlp-public-key.txt`. Before the first release,
configure the `ytdlp-release` GitHub environment and its `YTDLP_SIGNING_KEY` secret
with the value in the ignored, mode-0600 `.env.ytdlp-signing` generated for this
checkout. Keep a secure backup: replacing the public key requires a new IPA.
Never commit or upload the private key as a release asset. The signing command
refuses a private key that does not match the committed public key. To provision
a *new* trust root intentionally, `go run ./cmd/reverb-ytdlp-sign -keygen -out
.env.ytdlp-signing` creates the environment file without overwriting one.

Repeatable verification (macOS with the native dependencies built):

```sh
REVERB_YTDLP_E2E_REPORT=/tmp/reverb-ytdlp-e2e.json make test-pyembed
```

The E2E test uses the real release signer, HTTP delivery, updater and embedded
CPython. It updates a real yt-dlp package while a Python job is active, rejects
unsigned/wrongly signed/tampered releases, rolls back a broken import, installs a
second version and restores it after recreating the updater. The JSON report is
written only when all assertions pass. It uses an ephemeral test key.
