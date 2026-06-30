# store/AGENTS.md

Scoped instructions for plugin-owned PostgreSQL tables. Root rules in `/AGENTS.md` still apply.

## Connection

- `store.New(db *sqlx.DB)` uses the Mattermost master DB connection from `mmapi.NewDBClient`.
- The plugin is Postgres-only; non-Postgres drivers are rejected in `mmapi`.

## Schema migrations

- SQL migrations live in `store/migrations/NNNNNN_name.{up,down}.sql`.
- Callers must hold cluster mutex `ai_db_migrations` before `RunMigrations()`.
- Morph uses its own advisory lock and `Agents_DB_Migrations`.
- Add paired up and down migrations with the next sequence number.

## Table ownership

- `Agents_System`: plugin key-value flags.
- `Agents_ConfigHistory`: append-only config history with one active row.
- `Agents_UserAgents`: self-service agents.
- Conversation tables: conversation entities and turns.
- Custom prompts use `customprompts.Store` on `mmapi.DBClient`, not methods on `store.Store`.

## Config persistence

- `SaveConfig` deactivates the old config and inserts the new active config in one transaction.
- `GetConfig` returns `nil, nil` on fresh installs with no active config.
- Do not delete history rows as part of ordinary config changes.

## Not here

- `llm_posts_embeddings` is managed by `postgres/`, not Morph migrations.

## Commands

- Store tests: `go test -v ./store/...`
- Migration focus: `go test -v ./store/ -run TestRunMigrations`

## Pointers

- Runtime config model: `/config/AGENTS.md`.
- pgvector schema: `/postgres/AGENTS.md`.
- Mattermost DB wrapper: `/mmapi/AGENTS.md`.
