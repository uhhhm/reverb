# 03: Windows background sync

**What to build:** Closing the window on Windows keeps downloads and sync running, exactly like macOS and Linux. Reverb starts the same executable headlessly, below normal priority and with no console window, and the next window stops that process and waits until the database and bundled library are released before starting its own backend. `--stop-background` stops a running background copy, the keep-syncing preference persists across restarts, and the control channel keeps its security posture: it cannot be reached through the loopback HTTP API or a browser.

**Blocked by:** 02 (Windows desktop builds, opens the window, and boots the backend)

**Status:** ready-for-agent

- [ ] With background sync enabled, closing the window leaves a headless process that keeps an in-progress download and sync activity running.
- [ ] Reopening Reverb stops the background process and waits for it to finish shutting down before opening the database or starting the bundled library; no database-locked or port-collision errors appear.
- [ ] With background sync disabled, closing the window quits completely.
- [ ] `reverb-desktop --stop-background` (honouring `--db` or `REVERB_DB`) stops a running background copy.
- [ ] The background process runs at reduced priority and never shows a console window.
- [ ] The control endpoint refuses requests carrying an Origin header and is not exposed through the loopback HTTP API.
- [ ] A background process that fails to become ready within the startup timeout is terminated and its diagnostics are written to the log beside the database.
- [ ] Tests cover the control round-trip and the startup-failure path; macOS/Linux background behaviour is unchanged.
