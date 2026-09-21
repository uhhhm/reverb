# 09: Python runner and in-process downloaders (Linux)

**What to build:** A Python runner interface that the yt-dlp and spotdl downloader adapters (and external stream resolution) use in place of subprocesses. On Linux it is backed by host Python, so everything runs and is tested before any iOS work. This is the core half of ADR 0003's embedded download tools.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] Python runner interface with a host-Python implementation
- [ ] In-process yt-dlp and spotdl downloader adapters, registered through the existing downloader registry, pass the existing `download` conformance suite on Linux
- [ ] External stream resolution works through the runner
- [ ] Downloads keep source-native quality
- [ ] The phone profile selects these adapters, and the desktop profile keeps its bundled-executable adapters
