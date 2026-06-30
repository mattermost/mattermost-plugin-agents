# config/AGENTS.md

Scoped instructions for plugin configuration. Root rules in `/AGENTS.md` still apply.

## Architecture

- `config.Container` owns the current config with an atomic pointer and update listeners.
- `Update` deep-copies before notifying listeners or storing changes.
- Runtime plugin config lives in the plugin DB after migration from Mattermost `config.json`.
- Legacy migration logic belongs in `legacy_migrations.go`.
- MCP tool policies live in `mcp_config.go`; for duplicate tool entries, the last entry wins.
- Token usage logging can be overridden by `MM_FEATUREFLAGS_AI_TOKEN_USAGE_LOG_TO_PLUGIN` and `MM_FEATUREFLAGS_AI_TOKEN_USAGE_LOG_TO_FILE`.

## Commands

- Config tests: `go test -v ./config/...`

## Gotchas

- Prefer narrow interfaces over broad `Config()` access when adding dependencies.
- Admin API normalization forces some admin defaults; do not assume raw stored config equals what the UI sees.
- Cross-check provider/admin docs in `docs/admin_guide.md` when adding config fields.
