-- Each user in a shared thread gets their own conversation with the bot so
-- that approval and tool execution are scoped to the original requester.
DROP INDEX IF EXISTS idx_llm_conversations_thread_bot;
DROP INDEX IF EXISTS idx_llm_conversations_thread_bot_user;
CREATE UNIQUE INDEX idx_llm_conversations_thread_bot_user
    ON LLM_Conversations(RootPostID, BotID, UserID)
    WHERE RootPostID IS NOT NULL AND DeleteAt = 0;
