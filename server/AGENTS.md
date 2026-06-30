# server/AGENTS.md

Scoped instructions for the plugin entrypoint (`package main`). Root rules in `/AGENTS.md` still apply.

## Role

- Wire services on `OnActivate`, delegate HTTP to `api.API`, and implement Mattermost plugin hooks.
- Keep business logic in feature packages; keep this package focused on orchestration and adapters.

## Activation order

1. Create Mattermost API, license, and DB clients.
2. Create `store.Store`, hold cluster mutex `ai_db_migrations`, then run Morph migrations.
3. Hold cluster mutex `ai_config_migration`, migrate config JSON into DB, and mark config as migrated.
4. Load config into `config.Container`.
5. Wire bots, search/indexer, MCP, conversations, API, telemetry, and hooks.

## Cluster mutexes

- `ai_db_migrations`: plugin table migrations.
- `ai_config_migration`: config JSON to `Agents_ConfigHistory`.
- `ai_legacy_bots_migration`: legacy config bots to DB-backed agents.
- Related mutexes live in `bots/` and `indexer/`.

## Config gotchas

- After activation migration, the plugin DB is the config source of truth.
- Use `StorePersistedConfigWithoutNotify` when already inside listener-driven migration code to avoid re-entrant listener deadlocks.
- Config update listeners reinitialize bots, embedding search, embedded MCP, and telemetry; new listeners must be idempotent.

## Cluster events

- Publish config and agent events after DB writes.
- Config update events reload DB config into `config.Container`.
- Agent update events force bot refresh and notify webapp caches.
- MCP OAuth invalidation events clear per-user MCP clients.
- Stream stop events delegate to the streaming service.

## Failure policy

- Fail activation for missing SiteURL, DB migration failure, and config migration failure.
- Log and continue for bot ensure, embedding search init, and embedded MCP creation failures when existing code treats them as soft failures.

## Commands

- Package tests: `go test -v ./server/...`
- Single test: `go test -v ./server/... -run TestName`
- Deploy to running Mattermost: `make deploy`

## Pointers

- HTTP routes: `/api/AGENTS.md`.
- Config types: `/config/AGENTS.md`.
- Embedded MCP: `/mcpserver/AGENTS.md`.
