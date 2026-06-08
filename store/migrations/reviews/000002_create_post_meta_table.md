# Schema Migration Review: 000002 — Create LLM_PostMeta + drop legacy LLM_Threads FK

> **Context:** New table creation plus a defensive DROP CONSTRAINT against a legacy plugin table (`LLM_Threads`) that may exist on upgrade installs from older versions of the plugin. Both statements are guarded with `IF [NOT] EXISTS`. Note: `LLM_PostMeta` itself is later dropped in migration 000007 — this is a transitional table for a single release.

## Schema Changes
- [x] New table(s): `LLM_PostMeta` (`RootPostID TEXT PRIMARY KEY`, `Title TEXT NOT NULL`)
- [ ] New column(s): —
- [ ] New index(es): — (only the implicit primary-key index)
- [ ] Modified column(s): —
- [x] Dropped object(s): legacy FK constraint `llm_threads_rootpostid_fkey` on `LLM_Threads` (if present)

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | None. |
| CREATE INDEX uses CONCURRENTLY | N/A | No explicit indexes. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | The migration *removes* a FK; it never adds one. |
| No full-table DELETE/UPDATE | ✅ | No DML. |
| morph:nontransactional where needed | N/A | All-transactional DDL. |
| Down migration exists | ⚠️ Partial | Down drops `LLM_PostMeta` but does **not** restore the dropped FK on `LLM_Threads`. Acceptable — the up was a cleanup of an undesired constraint, and re-adding a FK during a downgrade would itself be unsafe. Worth documenting in the release notes. |
| Transactional/nontransactional split correct | ✅ | Pure DDL inside a transaction. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned tables only).
- Can previous Mattermost version run with new schema: Yes.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `LLM_PostMeta` (created), `LLM_Threads` (constraint dropped, if it exists).
- Lock types acquired:
  - `CREATE TABLE`: ACCESS EXCLUSIVE on the new table — no contention possible.
  - `ALTER TABLE … DROP CONSTRAINT`: brief ACCESS EXCLUSIVE on `LLM_Threads`. The operation is metadata-only and returns instantly, but it must wait for any in-flight statement on `LLM_Threads` to finish. Plugin write traffic to that legacy table is expected to be effectively zero by this point.
- Impact to concurrent operations: Negligible.

## Zero Downtime
- Possible: Yes.
- Reason: Both DDLs are metadata-only. The DROP CONSTRAINT will only block if another session is mid-statement on `LLM_Threads`; even then, ACCESS EXCLUSIVE is released immediately on completion.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: New empty table; FK drop is metadata-only regardless of `LLM_Threads` row count.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
-- No foreign keys: consistent with Mattermost conventions.
-- IF NOT EXISTS is safe for existing installs that already have this table.
CREATE TABLE IF NOT EXISTS LLM_PostMeta (
    RootPostID TEXT NOT NULL PRIMARY KEY,
    Title TEXT NOT NULL
);

-- Clean up FK constraint from old LLM_Threads table if it exists
ALTER TABLE IF EXISTS LLM_Threads DROP CONSTRAINT IF EXISTS llm_threads_rootpostid_fkey;
```
