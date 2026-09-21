# 14: Core-owned Radio session and Radio on iOS

**What to build:** The Radio session policy moves from the web player into the core player service: three tracks queued ahead, refills seeded from the latest tracks, skip = moving on after playing less than half, artist steering, no third consecutive track by one artist, prewarming, and the end conditions. Desktop and iOS both run Radio from the core. iOS moves from simple queue playback to the core queue.

**Blocked by:** 04, 10, 13

**Status:** ready-for-agent

- [ ] The core runs the Radio session through the player service API; the desktop SPA uses it and its own Radio policy is removed
- [ ] Single-runtime API tests cover refill, skip steering, the two-skips-stop rule, and no third consecutive track by one artist
- [ ] iOS plays from the core queue and reports position, skips, and completions
- [ ] Radio starts on iOS from a track, artist, album, or playlist and steers from skips
- [ ] No owner-visible change to Radio on desktop
