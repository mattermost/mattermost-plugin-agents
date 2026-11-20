# Schema Migration Review: 000022 — Add LastVersion to Agents_ChannelArtifacts

> **Context:** A channel artifact's version number is a public identifier (rollback, version
> records, content reads). Numbering the next version as `MAX(Version) + 1` reuses a number
> after the highest version is deleted, so a stale number could read different content.
> `LastVersion` is a counter that only grows, so a deleted number is never reused. Rows from
> before the column existed hold 0; the code takes the larger of the counter, the head, and
> the highest stored version, so they keep numbering correctly. The table holds one row per
> saved artifact and is bounded by the per-channel cap.

## Schema Changes
- [ ] New table(s): —
- [x] New column(s): `LastVersion INT NOT NULL DEFAULT 0` on `Agents_ChannelArtifacts`
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
| No full-table DELETE/UPDATE | ✅ | No DML; a constant default needs no table rewrite on PostgreSQL 11+. |
| morph:nontransactional where needed | N/A | Pure transactional DDL. |
| Down migration exists | ✅ | Drops the column with `IF EXISTS`. |
| Transactional/nontransactional split correct | ✅ | Single transactional statement. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned table; no Mattermost core change).
- Can previous plugin version run with new schema: Yes — additive; older code selects
  explicit columns on reads. Its inserts omit the column and get the default 0.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `Agents_ChannelArtifacts`.
- Lock types acquired: ACCESS EXCLUSIVE, briefly, for the metadata-only ADD COLUMN.
- Impact to concurrent operations: Negligible; the table is small.

## Zero Downtime
- Possible: Yes.
- Reason: Metadata-only additive DDL on a small table.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Row count is bounded by the per-channel artifact cap.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
ALTER TABLE Agents_ChannelArtifacts ADD COLUMN IF NOT EXISTS LastVersion INT NOT NULL DEFAULT 0;
```
