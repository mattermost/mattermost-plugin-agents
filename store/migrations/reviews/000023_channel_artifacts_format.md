# Schema Migration Review: 000023 — Add Format to Agents_ChannelArtifacts

> **Context:** The channel artifact library now holds Markdown documents as well as HTML.
> `Format` records which, so the webapp picks the right viewer and the content routes serve
> the right type. Rows from before the column existed are HTML, which the default covers. The
> table holds one row per saved artifact and is bounded by the per-channel cap.

## Schema Changes
- [ ] New table(s): —
- [x] New column(s): `Format VARCHAR(16) NOT NULL DEFAULT 'html'` on `Agents_ChannelArtifacts`
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
  explicit columns on reads. Its inserts omit the column and get the default 'html'.
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
ALTER TABLE Agents_ChannelArtifacts ADD COLUMN IF NOT EXISTS Format VARCHAR(16) NOT NULL DEFAULT 'html';
```
