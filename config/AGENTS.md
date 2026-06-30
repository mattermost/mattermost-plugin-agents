---
description: Plugin config schema, the in-memory Container, and one-time legacy JSON migrations.
tags: [config, migrations, container]
---

# config/AGENTS.md

Plugin configuration schema plus the thread-safe in-memory `Container` and one-time legacy transforms. **Runtime source of truth is the DB table `Agents_ConfigHistory`**, not Mattermost server plugin settings JSON — automate via `GET`/`PUT /plugins/mattermost-ai/admin/config` (system-admin), not server config patch.

- **Two migration systems, don't confuse them:** schema migrations are numbered SQL files in `store/migrations/` (Morph); **legacy config-shape transforms** are idempotent functions chained in `RunAllLegacyMigrations` (`legacy_migrations.go`), run once at activation. Add config-shape steps as functions here, not SQL.
- `Container` holds an `atomic.Pointer[Config]`; `Update` deep-copies (via JSON) and notifies all listeners. Prefer narrow interface methods over `Container.Config()` (its own comment says "avoid").
- Admin GET normalizes defaults (`MCP.Enabled`, embedded server, OpenAI `UseResponsesAPI`) via `normalizeAdminConfig` on a clone.
- `EnableTokenUsageLogTo{Plugin,File}` can be overridden by `MM_FEATUREFLAGS_AI_TOKEN_USAGE_LOG_TO_*` env vars. `go test ./config/...`.
