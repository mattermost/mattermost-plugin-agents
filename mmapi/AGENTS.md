# mmapi/AGENTS.md

Scoped instructions for Mattermost API and DB wrappers. Root rules in `/AGENTS.md` still apply.

## Architecture

- `Client` wraps pluginapi and Mattermost API operations.
- `ErrKVNotFound` distinguishes missing KV keys from empty values.
- `DBClient` is Postgres-only and uses squirrel with `$` placeholders.
- Thread helpers return `ThreadData` consumed by `format.ThreadData` and feature packages.

## Commands

- MMAPI tests: `go test -v ./mmapi/...`

## Gotchas

- Do not use `DBClient` with MySQL.
- Code that queries Mattermost core tables needs a real or faithfully mocked schema in tests.
