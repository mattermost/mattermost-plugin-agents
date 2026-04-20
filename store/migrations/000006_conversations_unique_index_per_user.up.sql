-- Repair the unique index on LLM_Conversations for servers that applied an
-- earlier version of migration 000005 which only indexed (RootPostID, BotID).
-- Each user who @mentions the bot in a shared thread must get their own
-- conversation so approval and tool execution remain per-requester. Without
-- this, the second user hits ErrConversationConflict and the re-lookup
-- filtered by UserID finds nothing ("conversation vanished after conflict").
DROP INDEX IF EXISTS idx_llm_conversations_thread_bot;
DROP INDEX IF EXISTS idx_llm_conversations_thread_bot_user;
CREATE UNIQUE INDEX idx_llm_conversations_thread_bot_user
    ON LLM_Conversations(RootPostID, BotID, UserID)
    WHERE RootPostID IS NOT NULL AND DeleteAt = 0;
