# 22: Quality CLI and ListenBrainz source can hang forever on a stalled request

**What to build:** Bound the ListenBrainz source and the `recommend-quality` CLI so a stalled or slow HTTP endpoint cannot hang an evaluation run indefinitely.

**Blocked by:** none

**Status:** ready-for-agent

Evidence (verified at `5cd26ea`):

- `internal/recommend/listenbrainz/source.go:55` — `New()` sets `client: http.DefaultClient`, which has no timeout.
- `cmd/recommend-quality/main.go:24,35` — `main` and `run` use `context.Background()`, so the CLI has no cancellation deadline.
- `internal/recommend/quality/quality.go:174` — `RecordSource` reaches the new source through that unbounded path, so `-record-cache` can block on a stalled request. Production recommendation calls are bounded by the 8s request context; the CLI is not.

Acceptance criteria:

- The ListenBrainz client or each request carries a timeout that does not depend on `http.DefaultClient`.
- The quality CLI bounds the evaluation run (per-request or whole-run deadline) and returns a clear error on timeout instead of hanging.
- Optional: honour `429` / `Retry-After` in `getJSON` instead of only fixed spacing.
- Add a test using a stalled `httptest` server that asserts the call returns a timeout error within the deadline.

- [ ] HTTP timeout no longer relies on `http.DefaultClient`
- [ ] CLI run is bounded; timeout returns an error
- [ ] Stalled-server test asserts bounded failure
- [ ] `go test ./internal/recommend/listenbrainz/... ./cmd/recommend-quality/...` passes
