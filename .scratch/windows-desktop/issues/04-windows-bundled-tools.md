# 04: Windows bundled tools and downloads

**What to build:** The Windows app is self-contained the same way the other platforms are: the same dependency fetch pulls the Windows builds of ffmpeg, Navidrome, and deno plus a relocatable Python runtime carrying spotDL and yt-dlp, and the app finds them at startup and wires the services to them. A download on Windows completes through spotDL, the yt-dlp fallback, deno-backed extraction, and ffmpeg post-processing, with no console windows flashing, and the daily yt-dlp hot-upgrade uses the bundled Python rather than a system `python3`.

**Blocked by:** 02 (Windows desktop builds, opens the window, and boots the backend)

**Status:** ready-for-agent

- [ ] The dependency fetch step on Windows retrieves the Windows builds of ffmpeg, Navidrome, and deno and installs spotDL and yt-dlp into a bundled Python runtime.
- [ ] On Windows the app locates bundled executables under their Windows names and the bundled Python's script location, and services receive the same environment variables as on macOS/Linux.
- [ ] The PATH prepend that lets spotDL invoke ffmpeg and yt-dlp by name works with the Windows path separator and does not duplicate entries.
- [ ] A Spotify download, a yt-dlp download, and deno-backed extraction succeed on Windows; downloaded files are post-processed with ffmpeg and appear in the library folder.
- [ ] No console window appears while bundled tools run.
- [ ] The automatic yt-dlp upgrade uses the bundled Python and succeeds without a system Python installation.
- [ ] Tool resolution and the existing tests on macOS/Linux are unchanged.
