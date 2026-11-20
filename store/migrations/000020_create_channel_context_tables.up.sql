CREATE TABLE IF NOT EXISTS Agents_ChannelInstructions (
    ChannelID VARCHAR(26) PRIMARY KEY,
    Instructions TEXT NOT NULL,
    UpdatedBy VARCHAR(26) NOT NULL,
    UpdateAt BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS Agents_ChannelContextPosts (
    ChannelID VARCHAR(26) NOT NULL,
    PostID VARCHAR(26) NOT NULL,
    PinnedBy VARCHAR(26) NOT NULL,
    PinnedAt BIGINT NOT NULL,
    PRIMARY KEY (ChannelID, PostID)
);
