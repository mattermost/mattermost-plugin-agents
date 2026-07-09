CREATE TABLE IF NOT EXISTS Agents_ChannelContexts (
    ChannelID VARCHAR(26) PRIMARY KEY,
    CustomInstructions TEXT NOT NULL DEFAULT '',
    FileIDs JSONB NOT NULL DEFAULT '[]'::jsonb,
    CreateAt BIGINT NOT NULL,
    UpdateAt BIGINT NOT NULL
);
