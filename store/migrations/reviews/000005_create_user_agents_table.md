# Schema Migration Review: 000005 — Create Agents_UserAgents table

> **Context:** New plugin table for admin-configured agent definitions. Empty on creation. Three secondary indexes including a partial unique index enforcing one active agent per bot user.

## Schema Changes
- [x] New table(s): `Agents_UserAgents`
- [ ] New column(s): —
- [x] New index(es):
  - `idx_useragents_creator` on `(CreatorID) WHERE DeleteAt = 0`
  - `idx_useragents_active` on `(DeleteAt)`
  - `idx_useragents_bot_user_id_active` UNIQUE on `(BotUserID) WHERE DeleteAt = 0`
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | None. |
| CREATE INDEX uses CONCURRENTLY | N/A | All indexes are built on a freshly-created empty table in the same transaction. CONCURRENTLY would be incompatible with the transactional `CREATE TABLE` that precedes it. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | None. |
| No full-table DELETE/UPDATE | ✅ | No DML. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY required. |
| Down migration exists | ✅ | Drops indexes, then table. |
| Transactional/nontransactional split correct | ✅ | Pure transactional DDL. |

## Observations
- Use of partial indexes (`WHERE DeleteAt = 0`) is appropriate and keeps the unique constraint on live rows only — soft-deleted rows can re-use a `BotUserID`.
- `idx_useragents_active` on `(DeleteAt)` is low-selectivity (most rows have `DeleteAt = 0`); consider whether this index pulls its weight, or whether `WHERE DeleteAt = 0` partial indexes for the actually-queried columns would be more useful. Not a migration safety issue, just a future tuning note.

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned).
- Can previous Mattermost version run with new schema: Yes — additive.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `Agents_UserAgents` (newly created).
- Lock types acquired: ACCESS EXCLUSIVE on the new table for CREATE TABLE / CREATE INDEX — no contention.
- Impact to concurrent operations: None.

## Zero Downtime
- Possible: Yes.
- Reason: Pure additive DDL on a brand-new table.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Empty table; typical production population is bounded (admins configure handfuls of agents, not millions).
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS Agents_UserAgents (
    ID VARCHAR(26) PRIMARY KEY,
    BotUserID VARCHAR(26) NOT NULL,
    CreatorID VARCHAR(26) NOT NULL,
    DisplayName VARCHAR(256) NOT NULL DEFAULT '',
    Username VARCHAR(64) NOT NULL,
    ServiceID VARCHAR(36) NOT NULL,
    CustomInstructions TEXT NOT NULL DEFAULT '',
    ChannelAccessLevel INT NOT NULL DEFAULT 0,
    ChannelIDs TEXT NOT NULL DEFAULT '[]',
    UserAccessLevel INT NOT NULL DEFAULT 0,
    UserIDs TEXT NOT NULL DEFAULT '[]',
    TeamIDs TEXT NOT NULL DEFAULT '[]',
    AdminUserIDs TEXT NOT NULL DEFAULT '[]',
    EnabledTools TEXT NOT NULL DEFAULT '[]',
    AutoEnableNewMCPTools BOOLEAN NOT NULL DEFAULT false,
    CreateAt BIGINT NOT NULL,
    UpdateAt BIGINT NOT NULL,
    DeleteAt BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_useragents_creator ON Agents_UserAgents(CreatorID) WHERE DeleteAt = 0;
CREATE INDEX IF NOT EXISTS idx_useragents_active ON Agents_UserAgents(DeleteAt);
CREATE UNIQUE INDEX IF NOT EXISTS idx_useragents_bot_user_id_active ON Agents_UserAgents(BotUserID) WHERE DeleteAt = 0;
```
