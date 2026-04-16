DROP INDEX IF EXISTS idx_llm_conversations_thread_bot;
CREATE UNIQUE INDEX idx_llm_conversations_thread_bot
    ON LLM_Conversations(RootPostID, BotID) WHERE RootPostID IS NOT NULL;
