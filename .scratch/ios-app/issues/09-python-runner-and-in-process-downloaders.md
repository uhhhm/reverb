# 09: Python runner and in-process downloaders (Linux)

**What to build:** A Python runner interface that the yt-dlp and spotdl downloader adapters (and external stream resolution) use in place of subprocesses. On Linux it is backed by host Python, so everything runs and is tested before any iOS work. This is the core half of ADR 0003's embedded download tools.

**Blocked by:** None (can start immediately)

**Status:** done

- [x] Python runner interface with a host-Python implementation
- [x] In-process yt-dlp and spotdl downloader adapters, registered through the existing downloader registry, pass the existing `download` conformance suite on Linux
- [x] External stream resolution works through the runner
- [x] Downloads keep source-native quality
- [x] The phone profile selects these adapters, and the desktop profile keeps its bundled-executable adapters
