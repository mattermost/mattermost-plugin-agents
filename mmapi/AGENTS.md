# mmapi/AGENTS.md

Scoped instructions for Mattermost API wrappers. Root rules in `/AGENTS.md` still apply.

## Client

- `Client` wraps `pluginapi.Client` for posts, users, channels, KV, files, permissions, and websockets.
- Use `mmapi.Client` in services for testability; existing mockery mocks live in `mmapi/mocks/`.
- `KVGet` maps missing plugin KV entries to `ErrKVNotFound`; use `IsKVNotFound(err)`.

## DBClient

- `DBClient` wraps Mattermost's master Postgres connection.
- Non-Postgres drivers are not supported.
- Use `NewTestDBClient(db)` for tests without plugin API.
- SQL builders should use Postgres placeholders.

## Which layer to use

- Mattermost entities: `mmapi.Client`.
- Plugin tables: `store.Store`.
- Custom prompts: `customprompts.Store`.
- Embeddings and pgvector: `postgres.PGVector`.

## Gotchas

- `channels.go` helpers are channel-type helpers only; LLM formatting still goes through `format/`.
- Do not bypass permission helpers when reading Mattermost content for prompts or tools.

## Commands

- Wrapper tests: `go test -v ./mmapi/...`

## Pointers

- Entity formatting: `/format/AGENTS.md`.
- Store layer: `/store/AGENTS.md`.
- File content permissions: `/files/AGENTS.md`.
