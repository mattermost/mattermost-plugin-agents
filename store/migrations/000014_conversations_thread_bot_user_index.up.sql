-- morph:nontransactional
-- GetLatestConversationByThreadBotUser looks up (RootPostID, BotID, UserID)
-- across active and archived rows, which the partial unique index
-- idx_llm_conversations_thread_bot_user (DeleteAt = 0 only) cannot serve.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_llm_conversations_thread_bot_user_all
    ON LLM_Conversations(RootPostID, BotID, UserID);
