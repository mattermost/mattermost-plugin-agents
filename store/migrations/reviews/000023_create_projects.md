# Schema Migration Review: 000023 — Create project channel tables and Agents_UserAgents.ProjectChannelID

> **Context:** Project channels (experimental, `enableProjectChannels`) store one row per project channel in `Agents_Projects`, the run threads the project agent opened in `Agents_ProjectThreads`, and the memberships and roles granted to the project agent's bot in `Agents_ProjectGrants`, so they can be revoked when the project ends. `Agents_UserAgents.ProjectChannelID` marks the hidden agent that leads a project. All tables start empty and stay small (one row per project, a handful of grants and threads each).

## Schema Changes
- [x] New table(s): `Agents_Projects`, `Agents_ProjectThreads`, `Agents_ProjectGrants`
- [x] New column(s) on `Agents_UserAgents`: `ProjectChannelID VARCHAR(26) NOT NULL DEFAULT ''`
- [x] New index(es): `idx_agents_projects_next_checkin`, `idx_agents_projectthreads_channel` (on the new, empty tables)
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | Only ADD COLUMN. |
| CREATE INDEX uses CONCURRENTLY | N/A | Indexes are created on tables created in the same migration, which are empty; no existing table is indexed here (see 000024). |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FKs. |
| No full-table DELETE/UPDATE | ✅ | No backfill; the column default supplies the value for existing rows. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY. |
| Down migration exists | ✅ | Drops the tables and the column. |
| Transactional/nontransactional split correct | ✅ | All-transactional; the concurrent index on `Agents_UserAgents` is 000024. |

## Postgres-Specific Notes
- `ADD COLUMN ... NOT NULL DEFAULT ''` is metadata-only on PostgreSQL 11+ (constant default → no table rewrite). ✅

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned).
- Can previous Mattermost version run with new schema: Yes — older plugin code ignores the new tables and column. An older plugin would list project agents as regular agents; project channels are experimental and off by default.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `Agents_UserAgents` (ADD COLUMN), new tables.
- Lock types acquired: ACCESS EXCLUSIVE on `Agents_UserAgents` for the metadata-only ADD COLUMN; new tables are not visible to other sessions until commit.
- Impact to concurrent operations: Negligible once the lock is granted; bounded by lock-wait duration if a long-running transaction holds a conflicting lock on `Agents_UserAgents`.

## Zero Downtime
- Possible: Yes.
- Reason: Metadata-only ADD COLUMN on an admin-managed table plus new empty tables.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: `Agents_UserAgents` is small; the other tables are new.

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
See `000023_create_projects.up.sql`.
