# Schema Migration Review: 000012 — Add RunImmediately to LLM_CustomPrompts

> **Context:** Per-prompt opt-in that makes a custom prompt post its rendered text right away instead of inserting it into the composer draft for review. `LLM_CustomPrompts` is user-authored content, bounded by how many prompts a workspace's users create (hundreds at most).

## Schema Changes
- [ ] New table(s): —
- [x] New column(s) on `LLM_CustomPrompts`: `RunImmediately BOOLEAN NOT NULL DEFAULT FALSE`
- [ ] New index(es): —
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | Only ADD COLUMN. |
| CREATE INDEX uses CONCURRENTLY | N/A | No indexes. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FKs. |
| No full-table DELETE/UPDATE | ✅ | No backfill UPDATE; the column default supplies the value for existing rows, which is also the behavior-preserving value. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY. |
| Down migration exists | ✅ | Drops the column. |
| Transactional/nontransactional split correct | ✅ | All-transactional. |

## Postgres-Specific Notes
- `ADD COLUMN ... NOT NULL DEFAULT FALSE` is metadata-only on PostgreSQL 11+ (constant default → no table rewrite). ✅

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned).
- Can previous Mattermost version run with new schema: Yes — older plugin code paths never select the column, and their `INSERT` omits it, so the default applies.
- Impact if not compatible: N/A.

## Observations
- `FALSE` is deliberately both the column default and the behavior-preserving value: every prompt that exists before this migration keeps the "insert into draft, user presses send" flow.
- Rolling the migration back drops the flag, so prompts that had opted in revert to the review-then-send flow rather than breaking. No data beyond the opt-in itself is lost.

## Table Locks & Impact
- Tables affected: `LLM_CustomPrompts`.
- Lock types acquired:
  - `ALTER TABLE … ADD COLUMN`: ACCESS EXCLUSIVE on `LLM_CustomPrompts`. Metadata-only because the default is constant — returns instantly.
- Impact to concurrent operations: Negligible.

## Zero Downtime
- Possible: Yes.
- Reason: Metadata-only ADD COLUMN on a small, user-authored table.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: `LLM_CustomPrompts` holds one row per user-authored prompt; the table stays small.

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
ALTER TABLE LLM_CustomPrompts
    ADD COLUMN IF NOT EXISTS RunImmediately BOOLEAN NOT NULL DEFAULT FALSE;
```
