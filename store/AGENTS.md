# store/AGENTS.md

Scoped instructions for `store/` and `store/migrations/`. Root rules in `/AGENTS.md` still apply; only package-specific conventions and gotchas live here.

## Schema migrations

Migrations run via `mattermost/morph` on PostgreSQL (embedded from `store/migrations/*.sql` in `store/migrate.go`).

- Store structured, versioned, or queryable domain data (per-channel/per-user collections) in PostgreSQL tables with migrations here rather than hand-rolled CAS or key-prefix scans in plugin KV.
- Every migration needs paired `NNNNNN_<name>.up.sql` and `NNNNNN_<name>.down.sql` files, a review doc at `store/migrations/reviews/NNNNNN_<name>.md` matching the existing review template (schema changes, safety table, locks, backwards compatibility, zero-downtime analysis), and a bump to the expected migration count in `store/store_test.go` (`migration tracking records correct count`).
- Always use `CREATE INDEX CONCURRENTLY IF NOT EXISTS` and `DROP INDEX CONCURRENTLY IF EXISTS` — never blocking index DDL. Note that a failed concurrent build leaves an `INVALID` index that `IF NOT EXISTS` will skip on retry; document recovery (check for invalid indexes, drop them concurrently, then rerun) in the migration review.
- Start any file containing `CONCURRENTLY` with `-- morph:nontransactional` on line 1, and keep transactional and non-transactional DDL in separate migrations.
- Avoid `ALTER COLUMN TYPE`, `ALTER TABLE ... ADD CONSTRAINT ... FOREIGN KEY`, and full-table `UPDATE`/`DELETE` backfills. `ADD COLUMN ... NOT NULL DEFAULT <constant>` is metadata-only on PostgreSQL 11+.
- Preserve backwards compatibility so the previous plugin version can keep running against the new schema during rolling upgrades.
- `go test ./store/...` boots its own PostgreSQL container via `testcontainers-go` and requires Docker.
