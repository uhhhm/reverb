# 10: Embedded Python on iOS and external playback

**What to build:** The iOS app embeds Python, QuickJS (yt-dlp's JavaScript runtime), and ffmpeg behind the Python runner. Search results and recommendations outside the library then play on the phone, including on cellular with no peer.

**Blocked by:** 08, 09

**Status:** done

- [x] iOS Python runner implementation, with yt-dlp, QuickJS, and ffmpeg bundled in the app
- [x] Confirm yt-dlp supports QuickJS as a JavaScript runtime before building; record the result in the ticket
- [x] A search result or recommendation outside the library plays on a phone with no paired device reachable
- [x] Prewarming of upcoming external tracks works on the phone
- [x] The IPA size increase is measured and recorded in the ticket

## Comments

**QuickJS (2026-09-24).** yt-dlp 2026.08.19 lists `quickjs` among its JavaScript runtimes (`--js-runtimes`: deno, node, quickjs, bun; deno is the default) and accepts QuickJS-ng, recommending 0.12.0 or later. It runs `qjs --script FILE` as a subprocess and reads the answer from stdout, and it starts ffmpeg and ffprobe as subprocesses too. iOS cannot spawn processes, so the launcher (`internal/pyrun/embedded/launcher.py`) intercepts `subprocess.Popen` for those three names and runs them in-process through `_reverb_native` (`ios/native`): QuickJS-ng 0.17.0 with its own `console`, and FFmpeg 9.0.2's `ffmpeg` and `ffprobe` programs, whose writable globals sit in their own sections and are restored before each run. The launcher also makes QuickJS yt-dlp's default runtime. Verified on a Mac through the embedded runner: yt-dlp reports `JS runtimes: quickjs-ng-0.17.0`, and a real resolve with the `mweb` client solves YouTube's n challenge with QuickJS (`make test-pyembed` with `REVERB_TEST_NETWORK=1`).

**IPA size (2026-09-24).** Unsigned Release IPA, zip -9: 16.0 MB before, 44.1 MB after (+28.1 MB). Installed app: 48 MB before, 129 MB after (+81 MB): Python's standard library 23 MB (its test suite, IDLE, Tk, ensurepip and pydoc data left out, 85 MB less), stdlib extension frameworks and Python.framework 23 MB, yt-dlp and its dependencies 30 MB with bytecode, and FFmpeg plus QuickJS in the app binary.

**spotDL is not bundled.** spotDL 4.5's default Spotify client (SpotipyFree → spotapi) and its SoundCloud provider need `curl_cffi` and Pillow, native packages with no iOS build, and it imports them at startup. The phone bundles and registers yt-dlp only; the in-process spotDL adapter stays tested on Linux and is registered there when the runner supplies the module. Album downloads on the phone therefore go through yt-dlp track by track (ticket 11).

**Playback format.** AVPlayer cannot open WebM, often YouTube's best audio, so the phone resolves `bestaudio[ext=m4a]/bestaudio` (`extstream.AppleFormat`). Checked on the iPhone 17 simulator with no paired device: a Deezer search result played, and skipping to the next result started within two seconds because it had been prewarmed.
