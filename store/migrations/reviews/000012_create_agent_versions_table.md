# Schema Migration Review: 000012 — Create Agents_AgentVersions

> **Context:** New append-only plugin table holding one row per saved version of a
> self-service agent (`Agents_UserAgents`). Every agent create/update writes a row in the
> same transaction as the agent write. Rows are never updated or deleted (soft-deleted
> agents keep their history; no pruning). Empty on creation; existing agents get a
> version 1 row from an idempotent Go backfill (`Store.BackfillAgentVersions`) that runs
> at plugin activation, not from this migration. Growth is bounded by the number of agent
> saves, which are human-driven admin actions.

## Schema Changes
- [x] New table(s): `Agents_AgentVersions` (`ID VARCHAR(26) PRIMARY KEY`,
  `AgentID VARCHAR(26) NOT NULL`, `Version INT NOT NULL`, `Config TEXT NOT NULL`,
  `CreatedBy VARCHAR(26) NOT NULL DEFAULT ''`, `CreateAt BIGINT NOT NULL`,
  `Source VARCHAR(32) NOT NULL`, `RestoredFromVersion INT NOT NULL DEFAULT 0`,
  `ChangedFields TEXT NOT NULL DEFAULT '[]'`)
- [ ] New column(s): —
- [x] New index(es): `idx_agentversions_agent_version` — unique on `(AgentID, Version)`;
  backstop for concurrent version numbering and serves the per-agent list/get/max lookups
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | Only CREATE TABLE / CREATE INDEX. |
| CREATE INDEX uses CONCURRENTLY | N/A | Plain `CREATE UNIQUE INDEX` is fine: the table is created in the same migration, so it is empty and unreachable by concurrent writers. CONCURRENTLY would also be incompatible with the transactional execution. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX in the up migration. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FKs. (`AgentID` logically references `Agents_UserAgents.ID`, but no FK enforcement — consistent with project convention.) |
| No full-table DELETE/UPDATE | ✅ | No DML. |
| morph:nontransactional where needed | N/A | Pure transactional DDL. |
| Down migration exists | ✅ | Drops the index then the table. |
| Transactional/nontransactional split correct | ✅ | All DDL transactional. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned table; no Mattermost core change).
- Can previous Mattermost version run with new schema: Yes — additive; older plugin code
  never references the table. Agent saves made by an older plugin version after a
  downgrade do not write versions; the new code lazily records the pre-update state as
  version 1 on the next save, and the activation backfill only covers agents with no rows.
- Impact if not compatible: N/A.

## Observations
- `Config` holds a JSON snapshot of the agent configuration, including custom
  instructions. API reads are restricted to users who can manage the agent.
- `ChangedFields` stores configuration field names only, never values.

## Table Locks & Impact
- Tables affected: `Agents_AgentVersions` (newly created).
- Lock types acquired: ACCESS EXCLUSIVE on the new table during CREATE TABLE / CREATE
  INDEX — no other session can reference it because it does not yet exist.
- Impact to concurrent operations: None.

## Zero Downtime
- Possible: Yes.
- Reason: Pure additive DDL on a new object.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Empty new table; the activation backfill inserts at most one row per active
  agent.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS Agents_AgentVersions (
    ID VARCHAR(26) PRIMARY KEY,
    AgentID VARCHAR(26) NOT NULL,
    Version INT NOT NULL,
    Config TEXT NOT NULL,
    CreatedBy VARCHAR(26) NOT NULL DEFAULT '',
    CreateAt BIGINT NOT NULL,
    Source VARCHAR(32) NOT NULL,
    RestoredFromVersion INT NOT NULL DEFAULT 0,
    ChangedFields TEXT NOT NULL DEFAULT '[]'
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agentversions_agent_version ON Agents_AgentVersions(AgentID, Version);
```
