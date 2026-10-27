# Schema Migration Review: 000013 — Add Skills to Agents_UserAgents

> **Context:** Persists the per-agent skill settings (whether skills are on, whether requesters' personal skills are listed, which shared skills are enabled, and per-agent description overrides) as one JSON document. `Agents_UserAgents` is admin-configured and bounded (typically tens of rows).

## Schema Changes
- [ ] New table(s): —
- [x] New column(s) on `Agents_UserAgents`: `Skills TEXT NOT NULL DEFAULT '{}'`
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
| No full-table DELETE/UPDATE | ✅ | No backfill UPDATE; the column default supplies the value for existing rows. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY. |
| Down migration exists | ✅ | Drops the column. |
| Transactional/nontransactional split correct | ✅ | All-transactional. |

## Postgres-Specific Notes
- `ADD COLUMN ... NOT NULL DEFAULT '{}'` is metadata-only on PostgreSQL 11+ (constant default → no table rewrite). ✅

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned).
- Can previous Mattermost version run with new schema: Yes — older plugin code paths simply ignore the column.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `Agents_UserAgents`.
- Lock types acquired:
  - `ALTER TABLE … ADD COLUMN`: ACCESS EXCLUSIVE on `Agents_UserAgents`. Metadata-only because the default is constant, so the lock is held for a negligible amount of work — but the lock is still requested up front and waits for any transaction already touching the table, and readers arriving during that wait queue behind it.
- Impact to concurrent operations: Negligible once the lock is granted; bounded by lock-wait duration if a long-running transaction holds a conflicting lock on `Agents_UserAgents`.

## Zero Downtime
- Possible: Yes.
- Reason: Metadata-only ADD COLUMN on an admin-managed table; no table rewrite.

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
    ADD COLUMN IF NOT EXISTS Skills TEXT NOT NULL DEFAULT '{}';
```
