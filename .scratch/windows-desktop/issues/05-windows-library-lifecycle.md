# 05: Windows embedded library lifecycle

**What to build:** The built-in Navidrome library behaves on Windows like it does elsewhere: it starts with the app, serves the music folder to the SPA, stops when Reverb quits, and releases its fixed port promptly. A force-quit cannot leave an orphaned Navidrome behind: the next launch detects and reaps it before starting its own child, using a safe identity check so a reused process id is never signalled by mistake.

**Blocked by:** 02 (Windows desktop builds, opens the window, and boots the backend)

**Status:** ready-for-agent

- [ ] On Windows the bundled Navidrome starts with the app and the local library loads and plays in the SPA.
- [ ] Quitting Reverb stops the child Navidrome gracefully and port 4533 is released before the process exits.
- [ ] After a forced termination of Reverb, the next launch reaps the orphaned Navidrome instead of failing to bind the port or serving a stale library.
- [ ] The process-identity check performed before reaping never signals a process that merely reused the recorded pid.
- [ ] Tests cover stop and orphan-reap behaviour of the platform helpers; macOS/Linux supervisor behaviour is unchanged.
