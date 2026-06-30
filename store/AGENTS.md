---
description: Plugin Postgres persistence — Morph schema migrations, config history, user agents, conversations/turns, system KV.
tags: [store, postgres, migrations, sql]
---

# store/AGENTS.md

Plugin Postgres persistence. Root `/AGENTS.md` still applies.

## Key files

- `store/store.go` — `New`, squirrel builder (`$` placeholders).
- `store/migrate.go` — embedded SQL migrations (Morph), table `Agents_DB_Migrations`.
- `store/config.go` — `Agents_ConfigHistory`, advisory lock on save, `IsConfigMigrated`.
- `store/agents.go` — `Agents_UserAgents` CRUD.
- `store/conversations.go`, `store/turns.go` — `LLM_Conversations`, `LLM_Turns`.
- `store/system.go` — `Agents_System` KV (migration flags).
- `store/migrations/*.sql` — versioned schema.

## Conventions & gotchas

- **Table prefixes:** `Agents_*` (config, agents, system, migrations) and `LLM_*` (conversations, turns). Follow the existing prefix when adding tables.
- **Migrations:** add a new numbered file pair under `store/migrations/`; the **caller must hold a cluster mutex** before `RunMigrations()` (done in `server/main.go`).
- **Turns:** use `CreateTurnAutoSequence` — it retries on unique-constraint violation to assign the next sequence atomically. Don't compute sequences in app code.
- **Conversations** are soft-deleted via `DeleteAt`.
- Squirrel uses Postgres `$N` placeholders; the plugin is Postgres-only.
