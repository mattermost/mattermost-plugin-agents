# config/AGENTS.md

Scoped instructions for plugin configuration types and migration. Root rules in `/AGENTS.md` apply.

## Architecture

- `Config` is JSON-serialized to the plugin DB.
- `Container` is the in-memory atomic config holder with listener hooks.
- Legacy migrations in `legacy_migrations.go` run during activation when old `config.json` values are imported.
- MCP config structs live here intentionally; `mcp/` re-exports aliases to avoid circular ownership.

## Commands

- Unit tests: `go test -v ./config/...`.
- Related persistence tests: `go test -v ./store/...`.

## Conventions

- Keep config structs declarative: strings, ints, bools, slices, and maps; no runtime service instances.
- New admin-facing config fields usually need server config, store migration/defaults, API handling, webapp admin UI, docs, and tests.
- Provider/admin behavior belongs in `docs/admin_guide.md`; do not copy long provider tables here.
