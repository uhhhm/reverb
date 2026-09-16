# 01: Isolate platform-varying desktop code behind OS-specific files

**What to build:** No user-visible change on macOS or Linux, but the desktop app's platform seams become separate implementations instead of unix assumptions embedded in shared code. Today the single-instance lock, background process spawning, the background control channel, updater process liveness and relaunch, the Navidrome child lifecycle, bundled-tool naming, and child-process console behaviour all assume a POSIX environment; extracting them while keeping the current implementations behaviourally identical is what lets the Windows tickets land without destabilising the shipping platforms. Each seam documents the contract a Windows implementation must satisfy, so the Windows work is a checklist rather than a re-read of unix code.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] macOS and Linux behaviour is unchanged: the desktop, updater, and embedded-library test suites pass and `make check` is green.
- [ ] The single-instance lock, background process spawn/priority/termination, background control channel, updater process liveness and relaunch, Navidrome stop/reap, bundled-tool resolution, and child-process console suppression are each isolated behind a per-OS seam with a unix implementation.
- [ ] Shared code no longer contains syscalls or external commands that exist only on POSIX (for example flock, Setsid, `nice`, `ps`, SIGTERM); those live in the versioned implementations.
- [ ] Each seam documents the semantics a Windows implementation must provide — atomicity, security posture, shutdown guarantees — not just the current unix mechanics.
- [ ] No test is weakened or skipped to make the split land.
