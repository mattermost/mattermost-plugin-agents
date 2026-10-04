CREATE TABLE IF NOT EXISTS Agents_AgentVersions (
    ID VARCHAR(26) PRIMARY KEY,
    AgentID VARCHAR(26) NOT NULL,
    Version INT NOT NULL,
    Config TEXT NOT NULL,
    CreatedBy VARCHAR(26) NOT NULL DEFAULT '',
    CreateAt BIGINT NOT NULL,
    Source VARCHAR(32) NOT NULL,
    RestoredFromVersion INT NOT NULL DEFAULT 0,
    ChangedFields TEXT NOT NULL DEFAULT '[]'
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agentversions_agent_version ON Agents_AgentVersions(AgentID, Version);
