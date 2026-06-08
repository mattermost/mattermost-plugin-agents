# Schema Migration Review: 000001 — Create Agents_System table

> **Context:** Brand-new plugin table created from scratch. No existing rows. Plugin uses the morph postgres driver (`store/migrate.go`) with the `Agents_DB_Migrations` tracking table and a 300-second statement timeout.

## Schema Changes
- [x] New table(s): `Agents_System` (key/value config — `SKey VARCHAR(64) PRIMARY KEY`, `SValue TEXT`)
- [ ] New column(s): —
- [ ] New index(es): — (only the implicit primary-key index)
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | Only CREATE TABLE. |
| CREATE INDEX uses CONCURRENTLY | N/A | No explicit indexes; primary-key index is created atomically with the table. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FK statements. |
| No full-table DELETE/UPDATE | ✅ | No DML. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY / no nontransactional statements — running inside a transaction is correct. |
| Down migration exists | ✅ | `DROP TABLE IF EXISTS Agents_System;` |
| Transactional/nontransactional split correct | ✅ | Pure DDL inside a transaction. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin schema; no Mattermost core change).
- Can previous Mattermost version run with new schema: Yes — the table is plugin-owned and additive; nothing in core references it.
- Impact if not compatible: None.

## Table Locks & Impact
- Tables affected: `Agents_System` (newly created).
- Lock types acquired: ACCESS EXCLUSIVE on the new table for the duration of CREATE TABLE — but no other session can be touching the table because it does not yet exist.
- Impact to concurrent operations: None.

## Zero Downtime
- Possible: Yes.
- Reason: Pure additive DDL on an object no concurrent reader/writer can reach.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Empty new table; nothing to seed.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS Agents_System (
    SKey VARCHAR(64) PRIMARY KEY,
    SValue TEXT
);
```
