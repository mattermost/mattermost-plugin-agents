CREATE TABLE IF NOT EXISTS Agents_UserAgents (
    ID VARCHAR(26) PRIMARY KEY,
    BotUserID VARCHAR(26) NOT NULL,
    CreatorID VARCHAR(26) NOT NULL,
    DisplayName VARCHAR(256) NOT NULL DEFAULT '',
    Username VARCHAR(64) NOT NULL,
    ServiceID VARCHAR(36) NOT NULL,
    CustomInstructions TEXT NOT NULL DEFAULT '',
    ChannelAccessLevel INT NOT NULL DEFAULT 0,
    ChannelIDs TEXT NOT NULL DEFAULT '[]',
    UserAccessLevel INT NOT NULL DEFAULT 0,
    UserIDs TEXT NOT NULL DEFAULT '[]',
    TeamIDs TEXT NOT NULL DEFAULT '[]',
    AdminUserIDs TEXT NOT NULL DEFAULT '[]',
    EnabledTools TEXT NOT NULL DEFAULT '[]',
    AutoEnableNewMCPTools BOOLEAN NOT NULL DEFAULT false,
    CreateAt BIGINT NOT NULL,
    UpdateAt BIGINT NOT NULL,
    DeleteAt BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_useragents_creator ON Agents_UserAgents(CreatorID) WHERE DeleteAt = 0;
CREATE INDEX IF NOT EXISTS idx_useragents_active ON Agents_UserAgents(DeleteAt);
CREATE UNIQUE INDEX IF NOT EXISTS idx_useragents_bot_user_id_active ON Agents_UserAgents(BotUserID) WHERE DeleteAt = 0;
