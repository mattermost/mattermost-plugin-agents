# Schema Migration Review: 000012 — Add experimental service account settings to Agents_UserAgents

> **Context:** Persists two per-agent experimental flags that only take effect while service account authentication is on: `ExperimentalBypassToolApproval` (tool calls run and results are shared without prompting) and `ExperimentalUseBotPermissions` (embedded/plugin MCP tools run as the agent's bot user). `Agents_UserAgents` is admin-configured and bounded (typically tens of rows).

## Schema Changes
- [ ] New table(s): —
- [x] New column(s) on `Agents_UserAgents`: `ExperimentalBypassToolApproval BOOLEAN NOT NULL DEFAULT false`, `ExperimentalUseBotPermissions BOOLEAN NOT NULL DEFAULT false`
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
| No full-table DELETE/UPDATE | ✅ | No backfill UPDATE; column defaults supply the value for existing rows. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY. |
| Down migration exists | ✅ | Drops both columns. |
| Transactional/nontransactional split correct | ✅ | All-transactional. |

## Postgres-Specific Notes
- `ADD COLUMN ... NOT NULL DEFAULT false` is metadata-only on PostgreSQL 11+ (constant default → no table rewrite). ✅

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned).
- Can previous Mattermost version run with new schema: Yes — older plugin code paths simply ignore the columns.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `Agents_UserAgents`.
- Lock types acquired:
  - `ALTER TABLE … ADD COLUMN` (twice): ACCESS EXCLUSIVE on `Agents_UserAgents`. Metadata-only because the defaults are constant, so each lock is held for a negligible amount of work, but it still waits for any transaction already touching the table.
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
    ADD COLUMN IF NOT EXISTS ExperimentalBypassToolApproval BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS ExperimentalUseBotPermissions BOOLEAN NOT NULL DEFAULT false;
```
