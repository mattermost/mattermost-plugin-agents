# Schema Migration Review: 000007 — Create LLM_Conversations / LLM_Turns and drop LLM_PostMeta

> **Context:** The biggest migration in the release. Creates two new tables and drops `LLM_PostMeta` (introduced in 000002, retired in this release). Both new tables are empty when the migration runs.

## Schema Changes
- [x] New table(s): `LLM_Conversations`, `LLM_Turns`
- [ ] New column(s): —
- [x] New index(es):
  - `idx_llm_conversations_thread_bot_user` UNIQUE on `(RootPostID, BotID, UserID) WHERE RootPostID IS NOT NULL AND DeleteAt = 0`
  - `idx_llm_conversations_userid` on `(UserID, UpdatedAt DESC) WHERE DeleteAt = 0`
  - `idx_llm_turns_conversation_sequence` UNIQUE on `(ConversationID, Sequence)`
  - `idx_llm_turns_post` on `(PostID) WHERE PostID IS NOT NULL`
- [ ] Modified column(s): —
- [x] Dropped object(s): table `LLM_PostMeta` (created in 000002)

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | None. |
| CREATE INDEX uses CONCURRENTLY | N/A | Built on freshly-created empty tables in the same transaction. CONCURRENTLY would be incompatible. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | None. (`LLM_Turns.ConversationID` is logically tied to `LLM_Conversations.ID` but no FK is declared, consistent with project convention.) |
| No full-table DELETE/UPDATE | ✅ | One UPDATE targets `LLM_Conversations`, which is brand-new in this migration and therefore empty. |
| morph:nontransactional where needed | N/A | No CONCURRENTLY used. |
| Down migration exists | ✅ | Drops both new tables and recreates `LLM_PostMeta` (empty) so the schema after a rollback matches what 000004 left behind. |
| Transactional/nontransactional split correct | ✅ | All-transactional. |

## Backwards Compatibility
- Compatible with previous ESR: Yes — plugin schema only.
- Can previous Mattermost version run with new schema: Yes for Mattermost core. **No** for older versions of *this plugin* — code that still tries to read/write `LLM_PostMeta` will fail. Confirm the plugin's minimum supported version is bumped accordingly.
- Impact if not compatible: A previously-installed older plugin binary running against this schema would error on `LLM_PostMeta` access.

## Table Locks & Impact
- Tables affected: `LLM_Conversations` (created), `LLM_Turns` (created), `LLM_PostMeta` (dropped).
- Lock types acquired:
  - `CREATE TABLE` / `CREATE INDEX`: ACCESS EXCLUSIVE on new objects — no contention.
  - `UPDATE`: ROW EXCLUSIVE on `LLM_Conversations` (empty).
  - `DROP TABLE`: ACCESS EXCLUSIVE on `LLM_PostMeta` for the duration of drop.
- Impact to concurrent operations: Any open transactions referencing `LLM_PostMeta` will block the DROP until they finish, then fail. By this point in the release, no plugin code path should reach that table.

## Zero Downtime
- Possible: Yes from a *schema* perspective — locks are bounded and brief.
- Caveat: Rollouts must shut down or upgrade the plugin atomically with the migration. Old plugin code that queries `LLM_PostMeta` after migration 7 has applied will see "relation does not exist" errors.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: All new tables are empty; `LLM_PostMeta` is small and bounded by AI thread count.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS LLM_Conversations (
    ID TEXT PRIMARY KEY,
    UserID TEXT NOT NULL,
    BotID TEXT NOT NULL,
    ChannelID TEXT,
    RootPostID TEXT,
    Title TEXT NOT NULL DEFAULT '',
    SystemPrompt TEXT NOT NULL DEFAULT '',
    Operation TEXT NOT NULL DEFAULT '',
    CreatedAt BIGINT NOT NULL,
    UpdatedAt BIGINT NOT NULL,
    DeleteAt BIGINT NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_conversations_thread_bot_user
    ON LLM_Conversations(RootPostID, BotID, UserID)
    WHERE RootPostID IS NOT NULL AND DeleteAt = 0;

CREATE INDEX IF NOT EXISTS idx_llm_conversations_userid
    ON LLM_Conversations(UserID, UpdatedAt DESC) WHERE DeleteAt = 0;

CREATE TABLE IF NOT EXISTS LLM_Turns (
    ID TEXT PRIMARY KEY,
    ConversationID TEXT NOT NULL,
    PostID TEXT,
    Role TEXT NOT NULL,
    Content JSONB NOT NULL,
    TokensIn BIGINT NOT NULL DEFAULT 0,
    TokensOut BIGINT NOT NULL DEFAULT 0,
    Sequence INTEGER NOT NULL,
    CreatedAt BIGINT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_turns_conversation_sequence
    ON LLM_Turns(ConversationID, Sequence);

CREATE INDEX IF NOT EXISTS idx_llm_turns_post
    ON LLM_Turns(PostID) WHERE PostID IS NOT NULL;

-- Backfill titles from LLM_PostMeta into LLM_Conversations for existing installs.
-- No-op on fresh installs since both tables are empty at this point.
UPDATE LLM_Conversations
SET Title = pm.Title,
    UpdatedAt = EXTRACT(EPOCH FROM NOW())::BIGINT * 1000
FROM LLM_PostMeta pm
WHERE LLM_Conversations.RootPostID = pm.RootPostID
  AND (LLM_Conversations.Title = '' OR LLM_Conversations.Title IS NULL);

DROP TABLE IF EXISTS LLM_PostMeta;
```
