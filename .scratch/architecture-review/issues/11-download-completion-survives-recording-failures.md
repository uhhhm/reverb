# 11 — Download completion survives recording failures and restart

**What to build:** Once a Download has produced its output, a failure to record required completion bookkeeping leaves a recoverable job holding that output. Automatic recovery, retry and a repeated Add request reuse the existing output, including after restart, rather than acquiring it again. Pending-upload protection remains in force until the required durable record succeeds.

Today completion-pending is encoded as a prefix in the job's human-readable error. Recovery, retry, cancellation and clearing depend on recognizing that text. Replace that hidden lifecycle state with explicit persisted state while preserving the existing public job statuses and recovery behavior. Make this completion path coherent first; ticket 12 then extends the transition owner to the rest of the lifecycle.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] Before implementation, enumerate failures around output persistence, completion bookkeeping, final job persistence and restart. Write regressions at the download manager's interface against real job persistence, using controlled downloader and completion dependencies.
- [ ] Completion-pending is represented durably and independently of error wording. Its transition owns output retention, required bookkeeping, persistence and publication ordering; changing a displayed error cannot alter retry or cancellation behavior.
- [ ] Existing persisted jobs using the old completion-error prefix migrate safely and idempotently. They retain their output, originating request, attribution and downloader reference. Other running or failed jobs are not mistaken for completed output.
- [ ] A persisted completion-pending job survives manager restart. Automatic recovery, manual Retry and a repeated Add request retry bookkeeping without starting or submitting another download. Cancellation, clearing one job and clearing finished jobs cannot discard it while required recording remains incomplete.
- [ ] If both bookkeeping and a job-row update fail, retain the output and pending work in the running process and retry persistence. Do not report completion or claim restart durability for state that has not been persisted.
- [ ] Bookkeeping effects are idempotent when the hook succeeds but the final job update fails. Repeated or overlapping retries produce no duplicate pending-upload or recommendation-add records. Completion publication and library linking happen only after required recording and the terminal job update succeed.
- [ ] Desktop and phone Downloads retain compatible HTTP and WebSocket job statuses, and frontend/iOS decoding continues to work. Any necessary additional transport data is defined in the contract and regenerated; no new public lifecycle status is required merely to store the internal phase.
- [ ] An application scenario uses a real database and a real temporary output file, fails completion recording, attempts Retry, repeated Add and clearing, persists the pending state, restarts the Device, and then recovers. Assert that the output remains, the downloader ran once, and the recovered job reaches completion with the required durable record. Also cover an asynchronous downloader's existing reference.
- [ ] Keep a machine-readable verification report with job transitions, downloader invocation count and output/record assertions, plus the fixture and exact rerun command. Never include credentials or private music in the artifact.
- [ ] Focused download, persistence, application and transport checks, `make check` and `make check-full` pass. Generated SQL and contracts pass their drift checks when changed; record any unavailable or failing check accurately.
