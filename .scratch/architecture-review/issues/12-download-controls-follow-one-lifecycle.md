# 12 — Download controls follow one lifecycle

**What to build:** Download controls and background work agree on the job's lifecycle. Completion, failure, cancellation, retry, shutdown and restart follow one set of transition rules for synchronous and asynchronous downloaders. The persisted job and the state shown after an event or reconnect agree, and a late result cannot resurrect a canceled or cleared attempt.

Ticket 11 establishes explicit completion-pending state and its durable completion path. Build on that owner rather than adding a second state machine. Keep downloader execution, polling and library scanning behind their existing seams; this ticket centralizes lifecycle decisions and their persistence/publication effects, not every responsibility of the download manager.

**Blocked by:** 11 — Download completion survives recording failures and restart.

**Status:** done

- [x] Before implementation, enumerate legal transitions and competing actions, including completion versus cancellation, stale progress, late asynchronous polls, retry versus an old attempt, and shutdown during work. Write controlled, repeatable regressions before changing transition code.
- [x] One transition owner enforces state preconditions and consistently updates status, progress, errors, attempt counters and timestamps. Synchronous workers, asynchronous submission/polling, retry, cancellation and restart recovery use it instead of writing terminal transitions independently.
- [x] Successful transitions persist before publishing their corresponding state. Persistence failure cannot publish a terminal success that a subsequent job-list request contradicts. Preserve the later completion notification that enriches a completed job with its library and catalog addressing.
- [x] Canceling queued or active work preserves the existing downloader-specific cancellation behavior. A callback or poll from a canceled, superseded or cleared attempt cannot restore that attempt to running or completed, overwrite a retry's state, or recreate a removed row.
- [x] Retry retains the originating request, quality, sections, attribution and any supplied manual URL, increments attempts consistently, and dispatches on the correct synchronous or asynchronous lane. Explicit retry may start a new attempt; stale work from the prior attempt may not control it.
- [x] Shutdown and restart preserve the documented distinctions among queued work, interrupted synchronous work, externally running asynchronous work and completion-pending output. Preserve pause/resume, dedup joining, fallback order, pacing and automatic retry limits.
- [x] Clear and Clear finished remove only eligible terminal jobs. Ticket 11's output-retention and no-redownload guarantees remain intact, and public job statuses remain compatible with desktop and iOS consumers.
- [x] Application scenarios coordinate completion/cancel races with barriers rather than timing guesses, restart during retry, and deliver stale asynchronous results after cancellation and clearing. Verify database state, recorded events and HTTP job-list results; a reconnect observes the same final state.
- [x] Keep a machine-readable transition/event report and exact rerun command as the repeatable verification artifact. Include both downloader lanes and the outcomes of the competing actions.
- [x] Focused lifecycle, application and transport checks, `make check` and `make check-full`, including relevant race tests, pass. Run contract generation and drift checks if transport data changes; record any unavailable or failing check accurately.
