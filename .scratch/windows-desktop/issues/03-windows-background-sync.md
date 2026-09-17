# 03: Windows background sync

**What to build:** Closing the window on Windows keeps downloads and sync running, exactly like macOS and Linux. Reverb starts the same executable headlessly, below normal priority and with no console window, and the next window stops that process and waits until the database and bundled library are released before starting its own backend. `--stop-background` stops a running background copy, the keep-syncing preference persists across restarts, and the control channel keeps its security posture: it cannot be reached through the loopback HTTP API or a browser.

**Blocked by:** 02 (Windows desktop builds, opens the window, and boots the backend)

**Status:** done

- [x] With background sync enabled, closing the window leaves a headless process that keeps an in-progress download and sync activity running.
- [x] Reopening Reverb stops the background process and waits for it to finish shutting down before opening the database or starting the bundled library; no database-locked or port-collision errors appear.
- [x] With background sync disabled, closing the window quits completely.
- [x] `reverb-desktop --stop-background` (honouring `--db` or `REVERB_DB`) stops a running background copy.
- [x] The background process runs at reduced priority and never shows a console window.
- [x] The control endpoint refuses requests carrying an Origin header and is not exposed through the loopback HTTP API.
- [x] A background process that fails to become ready within the startup timeout is terminated and its diagnostics are written to the log beside the database.
- [x] Tests cover the control round-trip and the startup-failure path; macOS/Linux background behaviour is unchanged.

## Comments

Implemented. Windows uses the same headless `--background` runtime and desktop
lifecycle as macOS and Linux. Its user-private AF_UNIX control channel lives
under the data directory, and the window waits for the stop acknowledgement,
which is sent only after the backend has closed and released its single-instance
lock. `--stop-background`, the persisted preference, browser-Origin rejection,
and the non-HTTP control boundary remain shared behavior.

The Windows child-process seam starts the background copy with
`CREATE_NO_WINDOW`, its own process group, and
`BELOW_NORMAL_PRIORITY_CLASS`; a Windows-only contract test pins those flags.
The existing process-level background test now runs unchanged on Windows CI and
covers control readiness, the private channel, lock ownership, acknowledged
shutdown, resource release, and stopping an absent copy.

The startup-failure path now waits for a failed child to exit after requesting
orderly termination, escalates to a hard kill after a grace period, and reaps it
before returning. Its process-level test deliberately stalls before publishing
the control channel, ignores the graceful request, then verifies both that no
process remains and that its diagnostic was retained in `background.log`.
