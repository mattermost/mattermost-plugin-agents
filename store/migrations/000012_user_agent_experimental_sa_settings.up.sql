ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS ExperimentalBypassToolApproval BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS ExperimentalUseBotPermissions BOOLEAN NOT NULL DEFAULT false;
