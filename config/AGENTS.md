# config/AGENTS.md

Scoped instructions for plugin configuration types and the in-memory config container. Root rules in `/AGENTS.md` still apply.

## Types

- `config.Config` is the JSON-serializable runtime configuration.
- `config.Container` stores an atomic deep copy and fires update listeners.
- `server/configuration.go` is only for one-time Mattermost plugin config loading during migration.

## Source of truth

- After activation migration, config lives in `Agents_ConfigHistory`.
- Do not read or patch Mattermost server plugin config JSON for runtime changes.
- Automation should use `/plugins/mattermost-ai/admin/config`.

## Legacy migrations

- `RunAllLegacyMigrations` runs during activation.
- Add legacy transforms in `legacy_migrations.go` with tests in `legacy_migrations_test.go`.
- Migrations must be idempotent and safe after partial application.

## Container semantics

- `Update(cfg)` deep-copies and fires all listeners.
- `StorePersistedConfigWithoutNotify(cfg)` deep-copies without listeners; use it to avoid listener re-entry during migration flows.
- Prefer narrow accessors such as `MCP()` or `EmbeddingSearchConfig()` over broad `Config()` access.

## Gotchas

- Admin normalization lives in `api/api_config.go`, not this package.
- MCP config JSON tags are part of the webapp/admin contract; keep them stable.
- Token usage logging env overrides are read through config helper methods.

## Commands

- Config tests: `go test -v ./config/...`
- Migration focus: `go test -v ./config/... -run TestMigrate`

## Pointers

- Activation migration wiring: `/server/AGENTS.md`.
- Persistence semantics: `/store/AGENTS.md`.
- Admin API normalization: `/api/AGENTS.md`.
