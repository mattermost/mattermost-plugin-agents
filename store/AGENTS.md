# store/AGENTS.md

Scoped instructions for plugin-owned persistence. Root rules in `/AGENTS.md` apply.

## Architecture

- `store/` owns plugin tables and Morph migrations.
- Callers must hold the cluster mutex before `RunMigrations`.
- Config history is append-only with an active row flag; `SaveConfig` uses advisory locking.
- Conversation tables store roots, turns, and bot-scoped uniqueness.
- `mmapi/` queries Mattermost core tables and is not the plugin-owned store.

## Commands

- Unit tests: `go test -v ./store/...`.
- Config and Mattermost DB wrapper tests: `go test -v ./config/... ./mmapi/...`.

## Migrations

- Add paired `store/migrations/NNNN_*.up.sql` and `.down.sql`.
- Keep review notes in `store/migrations/reviews/` when migration review requires them.
- Validate new schema behavior with store tests; do not rely only on SQL syntax.

## Pointers

- Config schema and legacy migration: `/config/AGENTS.md`.
- Activation ordering: `/server/AGENTS.md`.
