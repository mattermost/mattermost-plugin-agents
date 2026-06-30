---
description: Plugin composition root — activation wiring, hook delegation, cluster coordination, config lifecycle.
tags: [server, plugin, lifecycle, cluster, wiring]
---

# server/AGENTS.md

The Mattermost plugin entrypoint and composition root (`main.go`). Manual constructor wiring (no DI framework), hook delegation, and HA coordination.

- **Activation order matters** (`OnActivate`): `mmapi` → `store` + `RunMigrations` → `config.Container` → `bots` → search/indexer → MCP → `conversations`/`conversation`/`meetings` → `api.New`. Circular deps are broken with late setters (`SetConversationService`, `SetMeetingsService`, `SetToolPolicyChecker`).
- **There is no `OnConfigurationChange`.** Runtime config comes from the DB (`Agents_ConfigHistory`) loaded at activation; updates propagate via the `config_update` cluster event (`cluster_events.go`) and `config.Container.RegisterUpdateListener` side effects (bot refresh, embedding reinit, MCP reinit, telemetry).
- Three activation-time cluster mutexes: `ai_db_migrations`, `ai_config_migration`, and (in `bots/`) `ai_ensure_bots`. **`SiteURL` is required** before activation completes (OAuth callbacks + embedded MCP).
- Non-fatal at activation: `EnsureBots` failure (so the System Console stays reachable) and embedded-MCP creation failure. Embedding search is intentionally disabled on model/index mismatch until a reindex.
- One-time legacy migrations run here (`legacy_bot_migration.go`, `config.json` → DB); use `StorePersistedConfigWithoutNotify` to avoid re-entrant listener deadlocks.
- `manifest.Version`/`Id` come from build-generated `manifest.go` (`make apply`). Composition-root changes usually need `make deploy` to test. `go test ./server/...`.
