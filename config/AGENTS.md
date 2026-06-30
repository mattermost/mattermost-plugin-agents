---
description: Plugin configuration types and in-memory Container with update listeners; legacy JSON migrations.
tags: [config, container, migrations, listeners]
---

# config/AGENTS.md

Canonical `config.Config` shape, MCP/web-search sub-config, legacy migrations, and the thread-safe `Container`. Root `/AGENTS.md` still applies.

## Key files

- `config/config.go` — `Config`, `Container` (atomic pointer), listener registration, token-usage env overrides.
- `config/mcp_config.go` — `MCPConfig`, tool policies (`ask`, `auto_run_in_dm`, `auto_run_everywhere`).
- `config/legacy_migrations.go` — `RunAllLegacyMigrations`, `MigrateServicesToBots`.

## Conventions & gotchas

- **`Config` embeds domain types** from other packages: `[]llm.ServiceConfig`, `[]llm.BotConfig`, `embeddings.EmbeddingSearchConfig`. Those types (and their validation) live in `llm/configuration.go` / `embeddings/`, not here — `config/` is persistence/shape, not LLM domain logic.
- **Prefer `Container` accessor methods** over broad `Config()` reads (noted in the source).
- **Update listeners have side effects** (bot refresh, search reinit, MCP reinit, telemetry). When reloading from the DB inside a listener, use `StorePersistedConfigWithoutNotify` to avoid re-entrancy/deadlock.
- `normalizeAdminConfig` (in `api/api_config.go`) forces some fields (MCP enabled, OpenAI `UseResponsesAPI`) on GET/PUT — don't duplicate that logic here.
- Token-usage logging is additionally gated by env vars even when the config flag is set.
- `config/mcp_config.go` is plugin MCP wiring — distinct from the runtime client in `mcp/`.
