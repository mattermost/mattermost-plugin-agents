---
description: Plugin SQL persistence (squirrel + sqlx) and Morph migrations on the Mattermost Postgres DB.
tags: [store, sql, migrations, postgres]
---

# store/AGENTS.md

Typed SQL access for plugin-owned tables on the shared Mattermost Postgres DB (via `mmapi.NewDBClient`). Not a KV abstraction, and **not** where vectors live (that's `postgres/`). Postgres-only.

- Query style: squirrel with `sq.Dollar` placeholders.
- **Migrations:** numbered paired files in `store/migrations/*.{up,down}.sql`, embedded via `//go:embed` and run by Morph (`migrate.go`); tracking table `Agents_DB_Migrations`. Add a new sequential number with an optional `reviews/NNNNNN_*.md` safety checklist. `RunMigrations()` must be called while holding the cluster mutex `ai_db_migrations` (Morph itself also takes an advisory lock).
- Domains: config history (`Agents_ConfigHistory`), conversations/turns (`LLM_Conversations`, `LLM_Turns`), user agents (`Agents_UserAgents`), system KV (`Agents_System`). `SaveConfig` uses a PG advisory lock for HA.
- **Indexer job state is plugin KV, not SQL** — don't look here for reindex cursors/status.
- Tests use a `postgres:16-alpine` testcontainer with an isolated schema per test. `go test ./store/...`.
