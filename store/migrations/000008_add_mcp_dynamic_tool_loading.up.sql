ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS mcp_dynamic_tool_loading BOOLEAN NOT NULL DEFAULT true;

UPDATE Agents_UserAgents
SET mcp_dynamic_tool_loading = true
WHERE DeleteAt = 0;
