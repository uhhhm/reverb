# Working on Reverb

Reverb is a Go modular monolith with an embedded React/TypeScript SPA. **Desktop is primary**: use `desktop/`, `internal/app`, and the bundled tools under `desktop/tools/`. Server/Docker mode shares the composition root and remains supported.

`CLAUDE.md` is a symlink to this file. Edit `AGENTS.md`.

## Workflow

1. Read [CONTEXT.md](CONTEXT.md) for domain terminology. For identity, replication, adapter lifecycle, or desktop transport changes, read the relevant section of [docs/architecture.md](docs/architecture.md).
2. Locate the owner and focused tests using the task map below. Preserve the existing caller-facing contract when refactoring.
3. Run focused tests while editing, then `make check`. Run `make check-full` for cross-module or concurrency changes; it adds race tests and browser tests. Native desktop compilation is separate: `make desktop` with platform-appropriate `WAILS_TAGS`.
4. Commit coherent changes with Conventional Commits on `main` when commits are requested. Keep unrelated working-tree changes intact.

## Task map

| Change | Start here | Focused verification |
| --- | --- | --- |
| Playback, seek, queue | `web/src/lib/audioEngine.ts`, `playerStore.ts` | `cd web && npx vitest run src/lib/audioEngine.test.ts src/lib/playerStore.test.ts` |
| Downloads | `internal/download`, `web/src/lib/downloadApi.ts` | `go test ./internal/download/...` |
| Track metadata, sync emission | `internal/override`, `internal/crop`, `internal/cover`, `internal/syncemit`, `internal/api/track_sync.go` | `go test ./internal/api ./internal/materialize ./internal/syncemit ./internal/catalog` |
| Pairing, reconciliation | `internal/sync`, `internal/p2p` | `go test ./internal/sync ./internal/p2p` |
| Adapters, live reload | `internal/registry`, `internal/wiring`, `internal/app/reload.go` | `go test ./internal/wiring ./internal/app` |
| Desktop startup, tools, updates | `desktop/`, `internal/desktop` | `go test ./desktop/... ./internal/desktop/...` |
| HTTP contract | `internal/api/openapi.yaml`, `web/src/lib/*Api.ts` | `go test ./internal/api` and frontend typecheck/tests |

## Setup and checks

- `make setup-web` installs locked frontend dependencies. `make web` builds already-installed dependencies; `make desktop-dev` starts Wails development mode.
- Go minimum is declared by `go.mod`; CI selects its toolchain in `.github/workflows/ci.yml`. Use Node 22+.
- `make test` includes backend, desktop Go packages, and frontend unit tests. Use explicit Go package roots (`./cmd/... ./internal/... ./desktop/...`): repository-wide `./...` can traverse vendored Go in `web/node_modules`.
- `make gen` regenerates sqlc output; `make gen-check` checks drift. Edit SQL in `internal/store/queries`, migrations in `internal/store/migrations`, and handwritten extensions in separate files such as `internal/store/db/underlying.go`. Generated Go files are not hand-edited.
- `make fmt-check`, `make vet`, and `make check-web` expose the individual fast checks. Untagged desktop tests exercise boot and transport; they do not compile the native Wails window.

## Rules

- Register adapters explicitly at the composition root. Keep existing conformance suites and consumer-owned interfaces.
- Preserve catalog IDs versus backend IDs, catalog-first projection, non-emitting peer application, and local-only offline sets. See the architecture reference before changing these paths.
- The product has one household owner. Keep loopback/Host/Origin guards and paired-device authentication; capability gates do not imply a planned account system.
- Write current truth in documentation and comments. Replace stale claims rather than appending corrections or completed-task history.
- Secrets belong in environment variables or ignored `.env` files. Keep `.env.example` free of credentials.
