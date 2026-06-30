---
description: Plugin entrypoint and lifecycle — activation order, schema/config/bot migrations, service wiring, cluster events.
tags: [server, lifecycle, activation, migration, cluster]
---

# server/AGENTS.md

The plugin binary (`package main`): lifecycle, service-graph construction, hook delegation, cluster events, telemetry init. Root `/AGENTS.md` still applies.

## Key files

- `server/main.go` — `main()`, `Plugin`, `OnActivate`/`OnDeactivate`, hooks (`MessageHasBeenPosted`, `ServeHTTP`, notifications, data retention).
- `server/configuration.go` — one-time `LoadPluginConfiguration` migration wrapper.
- `server/cluster_events.go` — HA: config reload, bot refresh, MCP OAuth invalidation, stream stop.
- `server/legacy_bot_migration.go` — one-time copy of config bots → `Agents_UserAgents`.
- `server/embedded_mcp_server.go` — in-memory embedded MCP server (requires `SiteURL`).

## Activation flow (`OnActivate`)

1. Schema migrations under cluster mutex `ai_db_migrations` → `store.RunMigrations()`.
2. Config migration under mutex `ai_config_migration`: if `!store.IsConfigMigrated()`, load `config.json`, run `config.RunAllLegacyMigrations`, `store.SaveConfig`, then load into `config.Container`.
3. Legacy bot migration (own mutex + `Agents_System` flag).
4. Wire search/indexer/streaming/MCP, then `conversation.Service` + `conversations`, then `api.New` + telemetry.

## Conventions & gotchas

- **Order matters.** Circular deps are broken with setters (`SetConversationService`, `SetMeetingsService`, …) — preserve the call order in `main.go`.
- **`SiteURL` required** for activation (embedded MCP + OAuth); activation fails if unset.
- **Postgres only** — `mmapi.NewDBClient` panics on other drivers.
- **`EnsureBots` failure does not fail activation** — the plugin stays configurable.
- **`config.Container.RegisterUpdateListener`** fires bot refresh, embedding-search reinit, MCP reinit, and telemetry reapply on every config save. After migrations, use `StorePersistedConfigWithoutNotify` to avoid re-entrant listeners.
- Caller must hold a cluster mutex before `store.RunMigrations()`.
