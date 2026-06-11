# Schema Migration Review: 000009 — Add MaxToolTurns to Agents_UserAgents

> **Context:** Persists per-agent override for the tool-call-execute-recall loop ceiling enforced by `toolrunner`. `Agents_UserAgents` is admin-configured and bounded (typically tens of rows).

## Schema Changes
- [ ] New table(s): —
- [x] New column(s) on `Agents_UserAgents`: `MaxToolTurns INT NOT NULL DEFAULT 30`
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
| No full-table DELETE/UPDATE | ✅ | No backfill UPDATE; column default supplies the value for existing rows. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY. |
| Down migration exists | ✅ | Drops the column. |
| Transactional/nontransactional split correct | ✅ | All-transactional. |

## Postgres-Specific Notes
- `ADD COLUMN ... NOT NULL DEFAULT 30` is metadata-only on PostgreSQL 11+ (constant default → no table rewrite). ✅

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned).
- Can previous Mattermost version run with new schema: Yes — older plugin code paths simply ignore the column.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `Agents_UserAgents`.
- Lock types acquired:
  - `ALTER TABLE … ADD COLUMN`: ACCESS EXCLUSIVE on `Agents_UserAgents`. Metadata-only because the default is constant — returns instantly.
- Impact to concurrent operations: Negligible.

## Zero Downtime
- Possible: Yes.
- Reason: Metadata-only ADD COLUMN on an admin-managed table.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: `Agents_UserAgents` is admin-configured and small.

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS MaxToolTurns INT NOT NULL DEFAULT 30;
```
