# Schema Migration Review: 000003 — Create Agents_ConfigHistory table

> **Context:** New plugin table for tracking config snapshots, with a partial unique index that enforces "at most one Active=true row" — a clean PG-native pattern. Empty on creation.

## Schema Changes
- [x] New table(s): `Agents_ConfigHistory` (`ID VARCHAR(26) PK`, `Config TEXT`, `CreateAt BIGINT`, `Active BOOLEAN`)
- [ ] New column(s): —
- [x] New index(es): `idx_agents_confighistory_active` — partial unique index `WHERE Active = true`
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | None. |
| CREATE INDEX uses CONCURRENTLY | N/A | Plain `CREATE UNIQUE INDEX` is fine here: the table is being created in the same migration, so it is empty and unreachable by concurrent writers. CONCURRENTLY is also incompatible with running inside a transaction (which the rest of this file requires). |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | None. |
| No full-table DELETE/UPDATE | ✅ | No DML. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY used; transactional execution is correct. |
| Down migration exists | ✅ | Drops the index then the table. |
| Transactional/nontransactional split correct | ✅ | All DDL transactional. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned).
- Can previous Mattermost version run with new schema: Yes — additive only.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `Agents_ConfigHistory` (newly created).
- Lock types acquired: ACCESS EXCLUSIVE during CREATE TABLE / CREATE INDEX, against an object no other session can see.
- Impact to concurrent operations: None.

## Zero Downtime
- Possible: Yes.
- Reason: Pure additive DDL on a brand-new table.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Empty new table.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS Agents_ConfigHistory (
    ID VARCHAR(26) PRIMARY KEY,
    Config TEXT NOT NULL,
    CreateAt BIGINT NOT NULL,
    Active BOOLEAN NOT NULL DEFAULT false
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_confighistory_active ON Agents_ConfigHistory(Active) WHERE Active = true;
```
