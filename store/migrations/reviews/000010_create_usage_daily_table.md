# Schema Migration Review: 000010 — Create LLM_Usage_Daily

> **Context:** Per-day token usage aggregates keyed by (Day, UserID, BotID), written by the
> always-on usage recorder and read by the sysadmin stats endpoint. Row volume is bounded by
> active users × agents per day.

## Schema Changes
- [x] New table(s): `LLM_Usage_Daily`
- [ ] New column(s): —
- [ ] New index(es): — (composite PK only; its btree leads on `Day` and serves day-range scans)
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | CREATE TABLE only. |
| CREATE INDEX uses CONCURRENTLY | N/A | No standalone indexes. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FKs. |
| No full-table DELETE/UPDATE | ✅ | No backfill. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY. |
| Down migration exists | ✅ | Drops the table. |
| Transactional/nontransactional split correct | ✅ | All-transactional. |

## Postgres-Specific Notes
- `CREATE TABLE IF NOT EXISTS` acquires no locks on existing objects; the table is new. ✅

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned table).
- Can previous Mattermost version run with new schema: Yes — older plugin code ignores the table.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `LLM_Usage_Daily` (new).
- Lock types acquired: none on existing tables.
- Impact to concurrent operations: none.

## Zero Downtime
- Possible: Yes.
- Reason: New empty table; no existing data touched.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: New empty table.

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS LLM_Usage_Daily (
    Day DATE NOT NULL,
    UserID TEXT NOT NULL,
    BotID TEXT NOT NULL,
    IsGuest BOOLEAN NOT NULL DEFAULT FALSE,
    IsBot BOOLEAN NOT NULL DEFAULT FALSE,
    InputTokens BIGINT NOT NULL DEFAULT 0,
    OutputTokens BIGINT NOT NULL DEFAULT 0,
    Cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (Day, UserID, BotID)
);
```
