# Schema Migration Review: 000025 — Next check-in index on Agents_Projects

> **Context:** The check-in ticker selects projects whose `NextCheckinAt` is due every minute; the partial index covers only projects with check-ins scheduled.

## Schema Changes
- [ ] New table(s): —
- [ ] New column(s): —
- [x] New index(es): `idx_agents_projects_next_checkin` (partial, `NextCheckinAt > 0`)
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | — |
| CREATE INDEX uses CONCURRENTLY | ✅ | `CREATE INDEX CONCURRENTLY IF NOT EXISTS`. |
| DROP INDEX uses CONCURRENTLY | ✅ | Down migration. |
| No FOREIGN KEY via ALTER TABLE | ✅ | — |
| No full-table DELETE/UPDATE | ✅ | — |
| morph:nontransactional where needed | ✅ | Line 1 of both files. |
| Down migration exists | ✅ | — |
| Transactional/nontransactional split correct | ✅ | Only the concurrent index. |

## Postgres-Specific Notes
- Servers that ran an earlier build of 000023, which created this index inline, already have it; `IF NOT EXISTS` makes this migration a no-op there.
- A failed concurrent build leaves an `INVALID` index that `IF NOT EXISTS` skips on retry. Recovery: check `pg_index.indisvalid` for `idx_agents_projects_next_checkin`, `DROP INDEX CONCURRENTLY` it, and rerun the migration.

## Backwards Compatibility
- Compatible with previous ESR: Yes. Older plugin code ignores `Agents_Projects`.

## Table Locks & Impact
- Tables affected: `Agents_Projects`.
- Lock types acquired: SHARE UPDATE EXCLUSIVE (concurrent build); reads and writes continue.

## Zero Downtime
- Possible: Yes.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: `Agents_Projects` is small (one row per project channel).

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
See `000025_projects_next_checkin_index.up.sql`.
