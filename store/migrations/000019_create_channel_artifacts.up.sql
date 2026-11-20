CREATE TABLE IF NOT EXISTS Agents_ChannelArtifacts (
    ID VARCHAR(26) PRIMARY KEY,
    ChannelID VARCHAR(26) NOT NULL,
    Title VARCHAR(64) NOT NULL,
    HeadVersion INT NOT NULL,
    CreatedBy VARCHAR(26) NOT NULL,
    CreateAt BIGINT NOT NULL,
    UpdatedBy VARCHAR(26) NOT NULL,
    UpdateAt BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agents_channelartifacts_channel ON Agents_ChannelArtifacts(ChannelID, CreateAt);

CREATE TABLE IF NOT EXISTS Agents_ChannelArtifactVersions (
    ArtifactID VARCHAR(26) NOT NULL,
    Version INT NOT NULL,
    PostID VARCHAR(26) NOT NULL,
    FileID VARCHAR(26) NOT NULL,
    SHA256 CHAR(64) NOT NULL,
    Size BIGINT NOT NULL,
    SourceBotUserID VARCHAR(26) NOT NULL,
    CreatedBy VARCHAR(26) NOT NULL,
    CreateAt BIGINT NOT NULL,
    PRIMARY KEY (ArtifactID, Version)
);
