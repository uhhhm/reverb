# Transport contracts

`internal/api/openapi.yaml` owns HTTP schemas and the WebSocket `RealtimeEvent` union. The download slice is fully typed: create input, job output, queue state, and download events. Other HTTP response schemas can be completed incrementally; a generated declaration for an undocumented response does not establish its shape.

Run `make setup-contracts` once, then `make contracts` after editing the schemas. `make contracts-check` checks generated output without rewriting it. `make contracts-test` exercises Go HTTP handlers and validates their responses and serialized event payloads against the schemas. `make check` runs both.

The pinned tools live in `tools/contracts`, separate from the app's dependencies: openapi-typescript 7 requires TypeScript 5, whereas the app uses TypeScript 6. The tools emit:

- `web/src/lib/generated/api.d.ts`: HTTP and event types; handwritten wrappers import aliases from these declarations.
- `internal/core/download_wire.gen.go`: flat Go transport records explicitly marked `x-go-generate` in OpenAPI. Go field names, named types, and existing JSON omission rules are recorded alongside each property to preserve the wire representation.
- `web/src/lib/generated/validateRealtime.js` and its declaration: standalone event validation with no runtime dependency on the generation toolchain.

Every property in a Go-generated record must declare its Go name and supported type, or explicitly opt out with `x-go-ignore: true` for compatibility inputs the handler ignores. Generation fails on missing annotations or incompatible schema types.

Unknown event topics and malformed payloads are ignored before updating frontend state. Known valid events retain their payloads, including nullable ID lists emitted by Go and the null `sync.started` payload. Extra properties remain allowed for forward compatibility. HTTP request validation behavior remains in the handlers; schema generation does not add server-side rejection rules.

Matching normalization uses the existing `internal/matching/testdata` corpus in both languages. External track encoding uses `internal/trackref/testdata/wire.json`. UI dedup identity remains distinct from quality/section-aware download dedup identity and from catalog IDs used in replication.
