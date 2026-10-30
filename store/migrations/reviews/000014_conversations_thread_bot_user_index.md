# Schema Migration Review: 000014 — Index LLM_Conversations on (RootPostID, BotID, UserID)

> **Context:** Conversation archive keeps archived rows (`DeleteAt > 0`) and looks up a
> thread's latest conversation, active or archived, with
> `GetLatestConversationByThreadBotUser`. That lookup runs about twice per DM reply and on
> every @mention, channel auto-reply, and loop-in. The only index on the tuple,
> `idx_llm_conversations_thread_bot_user` (000007), is partial on `DeleteAt = 0`, so the
> lookup without a DeleteAt predicate falls back to a sequential scan. This adds a plain,
> non-unique index on the same columns. The partial unique index stays: it still enforces
> one active conversation per thread, bot, and user.

## Schema Changes
- [ ] New table(s): —
- [ ] New column(s): —
- [x] New index(es): `idx_llm_conversations_thread_bot_user_all` on
  `(RootPostID, BotID, UserID)` (non-unique, no predicate)
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | None. |
| CREATE INDEX uses CONCURRENTLY | ✅ | `CREATE INDEX CONCURRENTLY IF NOT EXISTS`. |
| DROP INDEX uses CONCURRENTLY | ✅ | Down migration uses `DROP INDEX CONCURRENTLY IF EXISTS`. |
| No FOREIGN KEY via ALTER TABLE | ✅ | None. |
| No full-table DELETE/UPDATE | ✅ | No DML. |
| morph:nontransactional where needed | ✅ | Both files start with `-- morph:nontransactional`, required for CONCURRENTLY. |
| Down migration exists | ✅ | Drops the index. |
| Transactional/nontransactional split correct | ✅ | Each file holds a single non-transactional statement. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned table; no Mattermost core change).
- Can previous plugin version run with new schema: Yes — an extra index changes no query results.
- Impact if not compatible: N/A.

## Table Locks & Impact
- Tables affected: `LLM_Conversations`.
- Lock types acquired: SHARE UPDATE EXCLUSIVE for the concurrent build; reads and writes continue.
- Impact to concurrent operations: Negligible; the build waits for in-flight transactions to finish.
  If the build fails it leaves an INVALID index that `IF NOT EXISTS` would then skip; drop it
  manually and rerun the migration in that case.

## Zero Downtime
- Possible: Yes.
- Reason: Concurrent index build on a table with one row per agent conversation.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: One row per conversation (not per message); the concurrent build does not block writes.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
-- morph:nontransactional
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_llm_conversations_thread_bot_user_all
    ON LLM_Conversations(RootPostID, BotID, UserID);
```
