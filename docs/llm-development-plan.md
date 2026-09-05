# Plan: make Reverb easier to change reliably

This plan is based on inspection of the current checkout on 2026-09-05. The goal is to reduce how much unrelated code an LLM must understand to implement and verify one feature. Desktop is the primary application; server mode remains supported through the shared composition root.

Keep the modular monolith, explicit adapter registration, existing conformance suites, and shared `internal/app` composition root. Improve locality and compile-time feedback incrementally. File size is a discovery signal, not a reason by itself to split a module.

## 1. Make navigation and verification dependable

**Priority: first. Effort: small to medium.**

Evidence:

- `CLAUDE.md` contains valuable invariants in a roughly 17 KB overview, but also stale statements: it says library data is never persisted, despite the catalog and materialized metadata; it lists Go 1.23+, while `go.mod` requires 1.25.7 and CI selects 1.26.5.
- No root `AGENTS.md` exists. Instructions mix architecture, commands, domain rules, and agent-provider configuration.
- `Makefile:test` and the main backend CI test job omit `desktop/...`. Desktop already has boot, Wails-origin, bundled-tool, and updater tests. The separate desktop workflow runs on tags or manual dispatch.
- `make gen-check` exists but is not invoked by the inspected workflows. Frontend CI uses `npm ci`, whereas `make web` uses `npm install`.

Changes:

1. Create a concise root `AGENTS.md` with desktop-first entry points, supported commands, generated-file rules, and links to focused architecture notes. Make `CLAUDE.md` point to the shared guidance, preserving relevant tool-specific instructions without duplicating architecture.
2. Add a small task map: playback → `audioEngine` and `playerStore`; downloads → `download.Manager`; metadata edits → override/crop/cover plus sync emission; pairing → sync and P2P; adapter changes → registry/wiring/app reload. Each entry names the relevant test command.
3. Keep terminology in `CONTEXT.md`; put identity, replication, and runtime lifecycle rules in linked documents close to their owners. Remove obsolete task-number comments as affected code is touched.
4. Provide a fast verification target for formatting checks, Go vet/tests including eligible desktop packages, TypeScript project checks, frontend lint/tests, and generated-code drift. Keep browser tests, race tests, and platform desktop builds as explicit full checks. Reuse these targets in CI.
5. Separate dependency setup from builds; use the lockfile for reproducible frontend installs. Document Go minimum versus the selected development/CI toolchain accurately.

Acceptance: a new contributor can locate a feature owner and its check without reading all of `CLAUDE.md`; ordinary PRs exercise desktop Go tests; sqlc drift fails CI. Native Wails builds remain platform-aware and must not be represented as covered by untagged Go tests.

## 2. Make cross-language contracts explicit

**Priority: second. Effort: medium. Depends on step 1.**

Evidence: HTTP contracts are maintained across `internal/api/openapi.yaml`, Go response shapes, `web/src/lib/types.ts`, and handwritten `*Api.ts` wrappers. `RealtimeEvent` is `{ type: string; payload: unknown }`; `realtimeWiring.ts` casts payloads independently. `trackRef.ts` explicitly mirrors Go matching normalization.

Changes:

1. Audit and complete schemas for one vertical slice first: download requests, responses, and events. Some OpenAPI responses describe behavior without specifying a full response schema; generating types before filling these gaps would give false confidence.
2. Use the existing OpenAPI document as the authoritative HTTP wire contract and generate frontend transport types. Retain small handwritten request/query wrappers and separate UI models where they add behavior. Add representative handler contract tests so generated types cannot merely agree with an incorrect schema.
3. Define WebSocket events as a discriminated union with payload validation at receipt. Specify how malformed frames and unknown topics are handled. Keep event definitions in one schema source and derive language-specific transport types rather than manually maintaining copies.
4. Add shared JSON fixtures for matching normalization and identity encoding, consumed by both Go and TypeScript tests. Preserve deliberate differences; do not force UI queue identity and sync identity into one concept.
5. Extend generation one feature at a time, deleting the superseded handwritten transport declarations. Document and pin generation tools when selected.

Acceptance: a changed download field produces a generation diff and catches incompatible consumers; representative Go responses satisfy the schema; invalid events are rejected before store updates; both languages pass the same normalization fixtures.

## 3. Remove hidden sync dependencies and duplicate mutation paths

**Priority: third. Effort: medium to large. Depends on step 1; can run independently of step 2.**

Evidence:

- `internal/sync/store.go` accepts the pairing `Querier`, then discovers changelog methods through runtime assertions. It includes fallback behavior explicitly retained for old mocks, including appending without HLC.
- `internal/api/track_sync.go` delegates to `SyncEmit` when present but also implements direct changelog writes and author resolution when it is absent.
- Correctness depends on catalog IDs, stable album/artist keys, catalog-first projection, and no re-emission of received changes. These rules already exist and must survive simplification.

Changes:

1. Give sync storage an explicit consumer-owned persistence interface for the operations it actually requires. Keep pairing's interface separate. Make production SQL and test adapters satisfy the same interface; remove mock-only fallback branches after migrating tests.
2. Inventory direct `AppendChange` calls. Consolidate metadata emission through `syncemit`, with an explicit disabled implementation only where non-replicating operation is supported. Confirm callers before removing legacy paths; preserve real persisted-data and peer compatibility.
3. Pilot a metadata-edit module with track rename and crop. Its interface owns local persistence, existing best-effort publication semantics, and change notification. HTTP handlers translate requests and responses. Peer projection uses a distinct non-emitting application path backed by the same local mutation logic.
4. Document identity at the interface. Introduce distinct catalog/backend ID types at high-risk internal seams where this catches actual mistakes; preserve wire strings and stored IDs.

Acceptance: missing persistence operations fail compilation; metadata handler tests need no device-signing setup; a local edit is published through one path; receiving it applies once without echo; catalog adoption and per-field merge tests remain green. Explicitly test that a publication failure preserves the current successful-local-edit behavior.

## 4. Give runtime reload one owner

**Priority: fourth. Effort: medium. Depends on step 1.**

Evidence: construction already lives in `internal/app`, but lifecycle responsibility crosses `app/reload.go` and `api/server.go`. The HTTP-facing `DownloadManager` includes `Stop`, and the server stops the old manager after reload. Resolver and other consumers must see the current services.

Changes:

1. Move service replacement and retirement into the application runtime module. Keep HTTP responsible for requesting reload and reporting its result.
2. Define the reload lifecycle: construct a candidate, validate it, publish the active services consistently, then retire the previous workers. Define failure behavior and the lifetime of requests already using old services.
3. Remove lifecycle methods from request-facing interfaces once no handler owns worker shutdown. Validate required dependencies during construction; preserve explicit optional capabilities for unconfigured adapters and server-only/desktop-only behavior.

Acceptance: failed reload leaves the previous runtime usable; successful reload updates all consumers; old workers stop once; shutdown remains safe during reload. Extend existing `internal/app/reload_test.go` and desktop boot tests, including race coverage.

## 5. Deepen download scheduling behind the existing interface

**Priority: fifth. Effort: large, split across several PRs. Depends on steps 1 and 4.**

Evidence: the approximately 1,800-line `internal/download/manager.go` owns enqueue/dedup, restart recovery, asynchronous polling, adapter selection/fallback, retries, pause/cancel, scan debounce, catalog linking, and completion publication.

Changes:

1. Write a state-transition table from current behavior: triggers, allowed prior states, persistence, events, and cancellation rules. Use existing tests to establish the contract before moving code.
2. Extract pure adapter-selection and retry-policy calculations first. Keep them private to the download module unless a real second consumer needs them.
3. Separate scan/rematch/completion coordination and asynchronous reconciliation internally. Keep one clear owner of job transitions and locking so extraction does not create competing state owners.
4. Preserve the manager's caller-facing interface and existing SQL/test seams. Remove duplicated orchestration as each extraction lands.

Acceptance: focused tests cover dedup joins, cancellation during execution, restart recovery, async submit/poll, fallback ordering, and scan-triggered completion. Race tests pass. A retry-policy change can be understood without reading catalog-linking implementation.

## 6. Concentrate frontend feature state

**Priority: sixth. Effort: medium to large. Depends on steps 1 and 2.**

Evidence: `SyncedPlaylist.tsx` combines server data, download overlays, playback/prewarming, editable settings, uploads, offline-set mutations, and optimistic drag ordering. `realtimeWiring.ts` coordinates several stores and query invalidations. `AudioEngine` is large but already hides substantial playback behavior behind a testable interface.

Changes:

1. Start with a playlist feature module: pure playable-track/coverage derivation, a hook that owns mutations and optimistic updates, and focused view components. Keep route loading and navigation visible in the route file.
2. State ownership explicitly: TanStack Query owns fetched records; Zustand owns playback and live coordination; component state owns transient forms. Where download event overlays duplicate fetched data intentionally, specify reconciliation and reset rules.
3. Centralize feature query keys and invalidation rules so mutation handlers and realtime events share them. Test reconnect and late-response behavior as well as initial rendering.
4. Move only the files belonging to the first extracted feature into a feature directory. Expand when another concrete task benefits; avoid a repository-wide import rewrite.
5. Leave `AudioEngine`'s external interface intact. Extract private queue-order or retry calculations only when a change demonstrates a locality benefit; preserve Wails media-origin, seek, and recovery behavior.

Acceptance: playlist editing can be tested without rendering the full route; download completion/reconnect reconciles the visible playlist correctly; reorder rollback and playlist switching do not retain stale optimistic state; existing playback tests remain green.

## Delivery and measurement

Implement step 1 first, then use the download contract in step 2 and explicit sync storage in step 3 as bounded pilot PRs. Keep mechanical moves separate from behavior changes. Each later PR states the interface it simplifies, deletes the superseded implementation, and names the behavior tests protecting it.

Before and after the pilots, try three representative tasks: add a download status field, add a replicated metadata field, and change playlist reorder behavior. Record files needed to understand the change, distinct places that encode the same fact, files edited, and time to run the relevant checks. Favor reductions in repeated knowledge and setup over arbitrary line-count targets.

Do not introduce a generic repository layer, dependency-injection framework, universal feature registry, or new state-management library for this effort. Preserve the single-owner product model, transport security guards, offline-set locality, identity semantics, and supported peer/storage formats. No database migration or protocol change is required by this plan; any later need should be scoped and reviewed separately.

This deliverable is a source-grounded plan only. No application code has been changed, and test suites have not been run to establish a green baseline. Establish that baseline in step 1 and record pre-existing failures separately from refactor regressions.
