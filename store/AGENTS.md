# store/AGENTS.md

Scoped instructions for `store/` and `store/migrations/`. Root rules in `/AGENTS.md` still apply; only package-specific conventions and gotchas live here.

## Schema migrations

Migrations run via `mattermost/morph` on PostgreSQL (embedded from `store/migrations/*.sql` in `store/migrate.go`).

- Every migration needs paired `NNNNNN_<name>.up.sql` and `NNNNNN_<name>.down.sql` files plus a review doc at `store/migrations/reviews/NNNNNN_<name>.md` matching the existing review template (schema changes, safety table, locks, backwards compatibility, zero-downtime analysis).
- Always use `CREATE INDEX CONCURRENTLY IF NOT EXISTS` and `DROP INDEX CONCURRENTLY IF EXISTS` — never blocking index DDL.
- Start any file containing `CONCURRENTLY` with `-- morph:nontransactional` on line 1, and keep transactional and non-transactional DDL in separate migrations.
- Avoid `ALTER COLUMN TYPE`, `ALTER TABLE ... ADD CONSTRAINT ... FOREIGN KEY`, and full-table `UPDATE`/`DELETE` backfills. `ADD COLUMN ... NOT NULL DEFAULT <constant>` is metadata-only on PostgreSQL 11+.
- Preserve backwards compatibility so the previous plugin version can keep running against the new schema during rolling upgrades.
- `go test ./store/...` boots its own PostgreSQL container via `testcontainers-go` and requires Docker.
