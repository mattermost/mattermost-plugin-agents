# Schema Migration Review: 000013 — Add ambient fields to Agents_ChannelAutoReply

> **Context:** Adds the per-channel ambient auto-reply instructions. The table holds one
> row per channel with auto-reply enabled, so it is small and bounded by channel-manager
> action.

## Schema Changes
- [ ] New table(s): —
- [x] New column(s): `Instructions TEXT NOT NULL DEFAULT ''`
- [ ] New index(es): —
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | Only ADD COLUMN. |
| CREATE INDEX uses CONCURRENTLY | N/A | No indexes. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FKs. |
| No full-table DELETE/UPDATE | ✅ | No DML; constant defaults need no table rewrite on PostgreSQL 11+. |
| morph:nontransactional where needed | N/A | Pure transactional DDL. |
| Down migration exists | ✅ | Drops the column with `IF EXISTS`. |
| Transactional/nontransactional split correct | ✅ | Single transactional statement. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned table; no Mattermost core change).
- Can previous plugin version run with new schema: Yes — additive; older code selects
  explicit columns and ignores the new one.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `Agents_ChannelAutoReply`.
- Lock types acquired: ACCESS EXCLUSIVE, briefly, for the metadata-only ADD COLUMN.
- Impact to concurrent operations: Negligible; reads are served from the in-memory cache.

## Zero Downtime
- Possible: Yes.
- Reason: Metadata-only additive DDL on a small table.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Row count is bounded by channels with auto-reply enabled.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
ALTER TABLE Agents_ChannelAutoReply
    ADD COLUMN IF NOT EXISTS Instructions TEXT NOT NULL DEFAULT '';
```
