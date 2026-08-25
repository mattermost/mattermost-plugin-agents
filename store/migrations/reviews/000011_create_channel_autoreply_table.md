# Schema Migration Review: 000011 — Create Agents_ChannelAutoReply

> **Context:** New plugin table storing per-channel agent auto-reply settings. One row
> per channel that has auto-reply enabled ("off" is represented by row absence), so the
> table is small and bounded by admin/channel-manager action. Empty on creation. Read
> path is served from an in-memory cache; the table is read in full once per node at
> plugin activation and per-channel on cluster invalidation events.

## Schema Changes
- [x] New table(s): `Agents_ChannelAutoReply` (`ChannelID VARCHAR(26) PRIMARY KEY`,
  `BotID VARCHAR(26) NOT NULL`, `Mode VARCHAR(32) NOT NULL`,
  `UpdatedBy VARCHAR(26) NOT NULL`, `UpdateAt BIGINT NOT NULL`)
- [ ] New column(s): —
- [ ] New index(es): — (only the implicit primary-key index)
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | Only CREATE TABLE. |
| CREATE INDEX uses CONCURRENTLY | N/A | No explicit indexes; PK index is created atomically with the table. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FKs. (`BotID` logically references a bot user and `ChannelID` a channel, but no FK enforcement — consistent with project convention to avoid FKs.) |
| No full-table DELETE/UPDATE | ✅ | No DML. |
| morph:nontransactional where needed | N/A | Pure transactional DDL. |
| Down migration exists | ✅ | `DROP TABLE IF EXISTS Agents_ChannelAutoReply;` |
| Transactional/nontransactional split correct | ✅ | Single transactional statement. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned table; no Mattermost core change).
- Can previous Mattermost version run with new schema: Yes — additive; older plugin code
  never references the table.
- Impact if not compatible: N/A.

## Observations
- Rows can outlive their bot or channel (no FK). Application code re-validates at write
  time and the trigger path (Phase 2) re-checks at reply time and no-ops, so orphaned
  rows are inert.

## Table Locks & Impact
- Tables affected: `Agents_ChannelAutoReply` (newly created).
- Lock types acquired: ACCESS EXCLUSIVE on the new table during CREATE TABLE — no other
  session can reference it because it does not yet exist.
- Impact to concurrent operations: None.

## Zero Downtime
- Possible: Yes.
- Reason: Pure additive DDL on a new object.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Empty new table; row count bounded by number of channels with the feature
  enabled.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS Agents_ChannelAutoReply (
    ChannelID VARCHAR(26) PRIMARY KEY,
    BotID VARCHAR(26) NOT NULL,
    Mode VARCHAR(32) NOT NULL,
    UpdatedBy VARCHAR(26) NOT NULL,
    UpdateAt BIGINT NOT NULL
);
```
