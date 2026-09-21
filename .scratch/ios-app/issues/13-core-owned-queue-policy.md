# 13: Core-owned queue policy (desktop refactor)

**What to build:** Queue state and advance logic move from the web player into a core service with an HTTP/WebSocket API. The desktop SPA becomes a thin player: it reports position, skips, and completions, and plays what the core says is next. Desktop behaviour is unchanged. This is the prefactor for Radio on iOS (ADR 0003).

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] A core player service owns the queue (play, enqueue, next, previous, reorder, clear) through an API described in OpenAPI
- [ ] The desktop SPA drives playback from the core queue, and the queue logic it replaces is removed
- [ ] Single-runtime API tests cover the queue behaviours the existing web player tests cover
- [ ] No owner-visible change on desktop; `make check-full` passes
