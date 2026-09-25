ALTER TABLE Agents_UserAgents
    DROP COLUMN IF EXISTS ExperimentalBypassToolApproval;
ALTER TABLE Agents_UserAgents
    DROP COLUMN IF EXISTS ExperimentalUseBotPermissions;
