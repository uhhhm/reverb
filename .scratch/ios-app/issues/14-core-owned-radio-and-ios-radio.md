# 14: Core-owned Radio session and Radio on iOS

**What to build:** The Radio session policy moves from the web player into the core player service: three tracks queued ahead, refills seeded from the latest tracks, skip = moving on after playing less than half, artist steering, no third consecutive track by one artist, prewarming, and the end conditions. Desktop and iOS both run Radio from the core. iOS moves from simple queue playback to the core queue.

**Blocked by:** 04, 10, 13

**Status:** done

- [x] The core runs the Radio session through the player service API; the desktop SPA uses it and its own Radio policy is removed
- [x] Single-runtime API tests cover refill, skip steering, the two-skips-stop rule, and no third consecutive track by one artist
- [x] iOS plays from the core queue and reports position, skips, and completions
- [x] Radio starts on iOS from a track, artist, album, or playlist and steers from skips
- [x] No owner-visible change to Radio on desktop

## Implementation

`internal/player/radio.go` runs the session inside the core queue, started by
`POST /player/{session}/radio` and steered by `POST /player/{session}/progress`
samples (entry, `playId`, position, duration, playing, seeking). It ports the
web policy: three ahead, refill seeds from the latest unseeded tracks, skip =
left under half heard, finish/repeat pull and skip push by artist and seed,
two skips block an artist, no third in a row, and re-ranking puts back as
many Radio tracks as it took, behind the listener's own. Lookups run in the
background unless nothing is playing, so no request waits on one; the core
prewarms the next two external tracks. The SPA's `RadioSession` is gone; it
and iOS report progress about once a second during Radio and before leaving a
track. iOS starts Radio from a track, album, playlist, artist or search/Home
result.

Tests are single-runtime API tests in `internal/api/player_radio_test.go`,
including a whole session driven as a player does
(`REVERB_RADIO_E2E_REPORT=<file>` writes its steps). The iOS paths are built
for the simulator but were not exercised on a device. Offline Radio on the
phone finds no local tracks (its catalogue lacks bindings for offline copies),
tracked separately.
