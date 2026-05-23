CREATE TABLE IF NOT EXISTS LLM_LoadedMCPTools (
    ConversationID TEXT NOT NULL,
    BotID TEXT NOT NULL,
    UserID TEXT NOT NULL,
    ToolName TEXT NOT NULL,
    ServerOrigin TEXT NOT NULL DEFAULT '',
    BareName TEXT NOT NULL DEFAULT '',
    CreatedAt BIGINT NOT NULL,
    UpdatedAt BIGINT NOT NULL,
    PRIMARY KEY (ConversationID, BotID, UserID, ToolName)
);

CREATE INDEX IF NOT EXISTS idx_loaded_mcp_tools_conversation
    ON LLM_LoadedMCPTools(ConversationID);
