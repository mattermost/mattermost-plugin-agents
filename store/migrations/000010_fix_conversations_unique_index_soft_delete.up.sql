-- Recreate the unique index on (RootPostID, BotID) to exclude soft-deleted rows,
-- so that a new conversation can be created for the same thread+bot after deletion.
DROP INDEX IF EXISTS idx_llm_conversations_thread_bot;
CREATE UNIQUE INDEX idx_llm_conversations_thread_bot
    ON LLM_Conversations(RootPostID, BotID) WHERE RootPostID IS NOT NULL AND DeleteAt = 0;
