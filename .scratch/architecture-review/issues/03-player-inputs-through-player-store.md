# 03: Every player input goes through the player store

**What to build:** A seek from the lock screen, media keys or the OS media overlay counts exactly like a seek from the Reverb UI. The core is told it was a seek, so the jump is not counted as listening.

Today `AppShell` builds a hybrid adapter for `startMediaSession` (`web/src/components/AppShell.tsx:59-67`). Play, pause and seek go straight to `audioEngine`, while next and previous go to `usePlayer`. `usePlayer.seekMs` reports the seek to the core first (`reportProgress(true, ms)`, `web/src/lib/playerStore.ts:129`). The media-session path skips that step, so the core's listening judgement (`internal/player/listening.go`) sees a forward jump. A jump under 5 s counts as heard, and Radio's separate counter does the same.

**The deepening:** the player store is the only interface that players and OS controls touch. The engine plays audio and nothing else.
- `mediaSession` takes the `usePlayer` actions.
- `mediaSession.ts:4`'s claim that `AudioEngine` satisfies the interface is corrected.
- `nowPlaying.ts` reads what's playing through the store, not the engine.

**Related cleanups in the same module:**
- Queue state is split three ways: the engine's `QueueState` snapshot, the store's `radio` flag, and the module-level `lastQueue` in `playerStore.ts`. Keep one snapshot.
- `audioEngine.getState()` allocates new `queue` and `upNext` arrays on every 250 ms tick, which re-renders `CinemaView` and `NowPlayingPanel`. Keep references stable when the queue has not changed.
- `realtimeWiring.ts` ignores `player.queue` notices. A playing player catches up through its once-a-second progress answer, but a paused one never sees a queue change made by another request (for example a Radio refill landing after pause). Refetch the session's queue on a notice for this session.
- `QueueTransport.radioEnded` is never called (`web/src/lib/playerApi.ts:36`). Delete it or use it.

**Blocked by:** None

**Status:** done

- [x] Write a failing test first. Driving the media-session seek handler reports a sample with `seeking: true` to the queue transport before the engine seeks, with the same test fakes `playerStore.test.ts` already uses.
- [x] Nothing outside `playerStore.ts` calls `engine.play`, `engine.pause` or `engine.seekMs`.
- [x] A paused player shows a queue change delivered by a `player.queue` notice for its session.
- [x] A Playwright scenario in `web/e2e/playback.spec.ts` starts a track and fires the media-session `seekto` action, then pauses, resumes and seeks again. The requests captured through `web/e2e/mocks.ts` show every seek reaching `POST /player/{session}/progress` with `seeking: true` before playback continues. The run keeps its trace as the artifact.
- [x] On the core side, a `player.Service` test feeds the resulting sample sequence and asserts that the seeked-over span is not counted as heard.
- [x] `cd web && npx vitest run src/lib/audioEngine.test.ts src/lib/playerStore.test.ts src/lib/mediaSession.test.ts` and `make check-web` pass.

**Notes from implementation.**
- The seek sample is built synchronously at the target, but it is sent through the store's serial request chain, so it reaches the transport on the next tick, after `engine.seekMs` has run. What the core relies on holds: the seek sample reaches it before any sample from the new position. The unit test and the Playwright scenario assert that ordering.
- `QueueTransport.radioEnded` is deleted from the web player. The core's `POST /player/{session}/radio-ended` route stays because it is part of the published contract.
- The engine keeps its queue arrays while an answer has the same revision and the same entries. Revision alone is not enough, because a session the core recreates starts counting again.
- The Playwright trace is written for every test in `web/e2e/playback.spec.ts` (`test-results/*/trace.zip`). The captured player requests are attached as `player-requests.json`.
