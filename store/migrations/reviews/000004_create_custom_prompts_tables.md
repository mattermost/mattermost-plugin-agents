# Schema Migration Review: 000004 — Create LLM_CustomPrompts and LLM_CustomPromptPins

> **Context:** Two new plugin tables for user-authored prompts plus per-user "pin" relationships. Both empty on creation.

## Schema Changes
- [x] New table(s): `LLM_CustomPrompts`, `LLM_CustomPromptPins` (composite PK `(UserID, PromptID)`)
- [ ] New column(s): —
- [ ] New index(es): — (only implicit PK indexes)
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | None. |
| CREATE INDEX uses CONCURRENTLY | N/A | No explicit indexes. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FKs declared. (`LLM_CustomPromptPins.PromptID` is logically a reference to `LLM_CustomPrompts.ID` but no FK enforcement — consistent with project convention to avoid FKs.) |
| No full-table DELETE/UPDATE | ✅ | No DML. |
| morph:nontransactional where needed | N/A | All transactional. |
| Down migration exists | ✅ | Drops pins first, then prompts (correct order). |
| Transactional/nontransactional split correct | ✅ | Pure transactional DDL. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned).
- Can previous Mattermost version run with new schema: Yes — additive.
- Impact if not compatible: N/A.

## Observations
- Without an FK, orphaned pins (rows in `LLM_CustomPromptPins` whose `PromptID` no longer exists) are possible if prompts are hard-deleted. Confirm the application code performs a cleanup or treats orphans as a no-op.
- `LLM_CustomPrompts.DeletedAt` is present (soft delete pattern), so hard deletes shouldn't be the common path.

## Table Locks & Impact
- Tables affected: both newly created.
- Lock types acquired: ACCESS EXCLUSIVE on the new tables — no contention possible.
- Impact to concurrent operations: None.

## Zero Downtime
- Possible: Yes.
- Reason: Pure additive DDL.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Empty new tables.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS LLM_CustomPrompts (
    ID TEXT NOT NULL PRIMARY KEY,
    CreatorID TEXT NOT NULL,
    Name TEXT NOT NULL,
    Description TEXT NOT NULL DEFAULT '',
    Template TEXT NOT NULL DEFAULT '',
    IsShared BOOLEAN NOT NULL DEFAULT FALSE,
    CreatedAt BIGINT NOT NULL DEFAULT 0,
    UpdatedAt BIGINT NOT NULL DEFAULT 0,
    DeletedAt BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS LLM_CustomPromptPins (
    UserID TEXT NOT NULL,
    PromptID TEXT NOT NULL,
    PRIMARY KEY (UserID, PromptID)
);
```
