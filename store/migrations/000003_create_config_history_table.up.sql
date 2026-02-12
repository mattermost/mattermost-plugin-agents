CREATE TABLE IF NOT EXISTS Agents_ConfigHistory (
    ID VARCHAR(26) PRIMARY KEY,
    Config TEXT NOT NULL,
    CreateAt BIGINT NOT NULL,
    Active BOOLEAN NOT NULL DEFAULT false
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_confighistory_active ON Agents_ConfigHistory(Active) WHERE Active = true;
