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
