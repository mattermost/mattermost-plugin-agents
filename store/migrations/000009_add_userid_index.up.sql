CREATE INDEX IF NOT EXISTS idx_llm_conversations_userid
    ON LLM_Conversations(UserID, UpdatedAt DESC) WHERE DeleteAt = 0;
