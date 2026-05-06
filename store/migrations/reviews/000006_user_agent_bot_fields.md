# Schema Migration Review: 000006 — Add bot/model fields to Agents_UserAgents

> **Context:** Adds 8 new columns to `Agents_UserAgents` (introduced in 000005, same release) and runs an UPDATE to set "prior implicit defaults". `Agents_UserAgents` is admin-configured and bounded in size (typically tens of rows, not millions).

## Schema Changes
- [ ] New table(s): —
- [x] New column(s) on `Agents_UserAgents`: `Model`, `EnableVision`, `DisableTools`, `EnabledNativeTools`, `ReasoningEnabled`, `ReasoningEffort`, `ThinkingBudget`, `StructuredOutputEnabled` — all `NOT NULL` with explicit `DEFAULT`
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
| No full-table DELETE/UPDATE | ✅ | A `WHERE DeleteAt = 0` UPDATE is issued against `Agents_UserAgents`. The table is admin-managed and bounded (typically tens of rows), so impact is negligible. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY. |
| Down migration exists | ✅ | Drops the 8 columns. (Down does not "un-write" the UPDATE, but since the columns themselves disappear, that's fine.) |
| Transactional/nontransactional split correct | ✅ | All-transactional. |

## Postgres-Specific Notes
- All ADD COLUMNs use `NOT NULL DEFAULT <constant>`. On PostgreSQL 11+ this is metadata-only and constant-time — no table rewrite. ✅
- The eight `ADD COLUMN IF NOT EXISTS` clauses are issued in a single `ALTER TABLE`, so they share one ACCESS EXCLUSIVE lock acquisition. ✅
- The UPDATE scales with row count, but `Agents_UserAgents` is an admin-managed table with bounded population.

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned).
- Can previous Mattermost version run with new schema: Yes — added columns are not used by older plugin code paths and have safe defaults.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `Agents_UserAgents`.
- Lock types acquired:
  - `ALTER TABLE … ADD COLUMN`: ACCESS EXCLUSIVE on `Agents_UserAgents`. Metadata-only because each default is a constant — returns instantly.
  - `UPDATE`: ROW EXCLUSIVE on the table; per-row tuple locks on matching rows. Permits concurrent SELECTs.
- Impact to concurrent operations: Negligible given table size.

## Zero Downtime
- Possible: Yes.
- Reason: All ADD COLUMNs are metadata-only; UPDATE is on a small admin-managed table.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: `Agents_UserAgents` is admin-configured and small; no realistic large-dataset scenario.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS Model VARCHAR(512) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS EnableVision BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS DisableTools BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS EnabledNativeTools TEXT NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS ReasoningEnabled BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS ReasoningEffort VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ThinkingBudget INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS StructuredOutputEnabled BOOLEAN NOT NULL DEFAULT false;

-- Backfill existing rows to match prior implicit defaults (vision off, tools on, reasoning on).
UPDATE Agents_UserAgents SET
    EnableVision = false,
    DisableTools = false,
    ReasoningEnabled = true
WHERE DeleteAt = 0;
```
