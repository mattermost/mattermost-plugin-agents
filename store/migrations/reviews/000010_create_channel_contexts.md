# Schema Migration Review: 000010 — Create Agents_ChannelContexts

> **Context:** Adds plugin-owned storage for per-channel AI instructions and knowledge-file references. The table is new and empty when created.

## Schema Changes
- [x] New table: `Agents_ChannelContexts`
- [ ] New columns on existing tables: —
- [ ] New indexes beyond the primary key: —
- [ ] Modified columns: —
- [ ] Dropped objects: —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | None. |
| CREATE INDEX uses CONCURRENTLY | N/A | Only the implicit primary-key index on a new table. |
| DROP INDEX uses CONCURRENTLY | N/A | None. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No foreign keys; file IDs reference Mattermost data logically. |
| No full-table DELETE/UPDATE | ✅ | None. |
| morph:nontransactional where needed | N/A | All-transactional. |
| Down migration exists | ✅ | Drops the new table. |
| Transactional/nontransactional split correct | ✅ | All-transactional. |

## Backwards Compatibility
- Compatible with previous ESR: Yes — plugin-owned additive schema only.
- Can a previous plugin version run with the new schema: Yes — older versions ignore the table.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `Agents_ChannelContexts` (created).
- Lock type: ACCESS EXCLUSIVE on the new table during creation.
- Impact to concurrent operations: None; no prior code can access the table.

## Zero Downtime
- Possible: Yes. The migration only creates a new empty table.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: No existing data is scanned or rewritten.

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS Agents_ChannelContexts (
    ChannelID VARCHAR(26) PRIMARY KEY,
    CustomInstructions TEXT NOT NULL DEFAULT '',
    FileIDs JSONB NOT NULL DEFAULT '[]'::jsonb,
    CreateAt BIGINT NOT NULL,
    UpdateAt BIGINT NOT NULL
);
```
