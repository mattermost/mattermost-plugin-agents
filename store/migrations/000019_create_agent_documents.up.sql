CREATE TABLE IF NOT EXISTS Agents_AgentDocuments (
    ID VARCHAR(26) PRIMARY KEY,
    SHA256 VARCHAR(64) NOT NULL,
    Name VARCHAR(256) NOT NULL,
    MimeType VARCHAR(128) NOT NULL,
    Size BIGINT NOT NULL,
    Content BYTEA NOT NULL,
    ExtractedText TEXT NOT NULL,
    TextRunes INT NOT NULL,
    CreatedBy VARCHAR(26) NOT NULL DEFAULT '',
    CreateAt BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agentdocuments_sha256_createdby ON Agents_AgentDocuments(SHA256, CreatedBy);

ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS Documents TEXT NOT NULL DEFAULT '[]';
