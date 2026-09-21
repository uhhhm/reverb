# 01: Shared relocatable Python runtime build

**What to build:** The step that produces a relocatable Python runtime carrying spotDL and yt-dlp — a python-build-standalone interpreter with console-script entry points rewritten as path-relative wrappers — stops being private to the macOS packaging script and becomes a shared build step that macOS and Linux both call. The macOS `.app` keeps working exactly as before, and on Linux the same step yields a Python tree that still runs spotDL and yt-dlp after being moved to an unrelated directory. The in-tree development venv is unchanged; it stays the developer path, not the shipped one.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] One shared build step produces the relocatable runtime for the host OS/arch (macOS amd64/arm64, Linux amd64/arm64), with pinned Python, python-build-standalone and spotDL versions overridable by environment variable.
- [ ] macOS packaging uses the shared step and still produces a `.app` whose bundled spotDL, yt-dlp and ffmpeg pass their version checks.
- [ ] On Linux, the runtime copied to a fresh directory (and the original deleted) runs `spotdl --version` and `yt-dlp --version` through its wrappers.
- [ ] The wrappers keep their own directory off `sys.path`, so a script never shadows its package.
- [ ] The app's bundled-Python lookup finds the runtime in the bundle layout, so the daily yt-dlp hot-upgrade targets it and never a system Python.
