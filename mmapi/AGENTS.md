# mmapi/AGENTS.md

Scoped instructions for Mattermost API/database wrappers. Root rules in `/AGENTS.md` apply.

## Scope

- `mmapi/` wraps Mattermost plugin API calls and direct reads of Mattermost core tables.
- Plugin-owned tables and migrations live in `store/`.

## Commands

- Unit tests: `go test -v ./mmapi/...`.

## Gotchas

- Direct DB helpers assume PostgreSQL.
- Thread/post helpers live here; there is no separate top-level `posts/` package.
- Preserve `ErrKVNotFound` semantics when changing KV wrappers.
- Formatting for LLM/tool output still goes through `format/`.

## Pointers

- Plugin-owned persistence: `/store/AGENTS.md`.
- API handlers: `/api/AGENTS.md`.
