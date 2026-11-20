# Schema Migration Review: 000024 — Unique live ProjectChannelID on Agents_UserAgents

> **Context:** At most one live agent may lead a project channel. `Agents_Projects` (keyed by channel) already enforces one project per channel; this partial unique index is defense in depth on the agent rows.

## Schema Changes
- [ ] New table(s): —
- [ ] New column(s): —
- [x] New index(es): `idx_agents_useragents_project_channel` (partial, unique)
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | — |
| CREATE INDEX uses CONCURRENTLY | ✅ | `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS`. |
| DROP INDEX uses CONCURRENTLY | ✅ | Down migration. |
| No FOREIGN KEY via ALTER TABLE | ✅ | — |
| No full-table DELETE/UPDATE | ✅ | — |
| morph:nontransactional where needed | ✅ | Line 1 of both files. |
| Down migration exists | ✅ | — |
| Transactional/nontransactional split correct | ✅ | Only the concurrent index. |

## Postgres-Specific Notes
- Every existing row has `ProjectChannelID = ''`, which the partial predicate excludes, so the build cannot fail on duplicates.
- A failed concurrent build leaves an `INVALID` index that `IF NOT EXISTS` skips on retry. Recovery: check `pg_index.indisvalid` for `idx_agents_useragents_project_channel`, `DROP INDEX CONCURRENTLY` it, and rerun the migration.

## Backwards Compatibility
- Compatible with previous ESR: Yes. Older plugin code never writes the column.

## Table Locks & Impact
- Tables affected: `Agents_UserAgents`.
- Lock types acquired: SHARE UPDATE EXCLUSIVE (concurrent build); reads and writes continue.

## Zero Downtime
- Possible: Yes.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: `Agents_UserAgents` is small.

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
See `000024_user_agents_project_channel_index.up.sql`.
