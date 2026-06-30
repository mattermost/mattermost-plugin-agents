# config/AGENTS.md

Plugin configuration types and one-time migrations. Root `/AGENTS.md` applies.

## Runtime source of truth

- Live config is the active row in `Agents_ConfigHistory` (`store.GetConfig`/`SaveConfig`). `config.json` is read **once** at activation, only if `IsConfigMigrated()` is false (`server/main.go`, mutex `ai_config_migration`).
- There is **no `OnConfigurationChange` path** for live config and **no version field** — backward compatibility relies on JSON `omitempty` + tolerant unmarshal plus append-only history. For automation, read/write `GET`/`PUT /plugins/mattermost-ai/admin/config`, not the Mattermost server config.
- In-memory config is `config.Container` (`atomic.Pointer[Config]`); `Update` deep-copies and fires listeners. Prefer narrow getters over reading the whole `Container.Config()`.

## Adding a field

Add a JSON-tagged field to `config.Config` (or the relevant nested struct, e.g. `config/mcp_config.go`). If admin GET/PUT needs to force a value, do it in `normalizeAdminConfig` (`api/api_config.go`). Service **validation** lives in `llm.IsValidService` (`llm/configuration.go`), not here.

## Gotchas

- During migration/listener chains, persist with `StorePersistedConfigWithoutNotify` (not `Update`) to avoid listener deadlock.
- Legacy migrations (`config/legacy_migrations.go`, run once at activation): `MigrateServicesToBots` (skips if bots already exist) and `MigrateSeparateServicesFromBots`. Don't re-trigger these outside activation.
- `MCPServerConfig.GetToolPolicy`: missing entry → `("ask", true)`; invalid policy → `"ask"`; duplicate tool names → last wins; disabled server → `("ask", false)`.
- `mcp.Config` / `mcp.ServerConfig` are type aliases of `config.MCPConfig` / `config.MCPServerConfig` — edit the config package, not the alias.
