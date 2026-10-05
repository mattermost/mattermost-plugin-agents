ALTER TABLE Agents_UserAgents
    DROP COLUMN IF EXISTS Documents;

DROP INDEX IF EXISTS idx_agentdocuments_sha256_createdby;
DROP TABLE IF EXISTS Agents_AgentDocuments;
