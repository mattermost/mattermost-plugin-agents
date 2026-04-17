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
