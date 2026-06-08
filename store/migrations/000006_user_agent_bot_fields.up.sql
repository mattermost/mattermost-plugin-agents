ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS Model VARCHAR(512) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS EnableVision BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS DisableTools BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS EnabledNativeTools TEXT NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS ReasoningEnabled BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS ReasoningEffort VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS ThinkingBudget INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS StructuredOutputEnabled BOOLEAN NOT NULL DEFAULT false;

-- Backfill existing rows to match prior implicit defaults (vision off, tools on, reasoning on).
UPDATE Agents_UserAgents SET
    EnableVision = false,
    DisableTools = false,
    ReasoningEnabled = true
WHERE DeleteAt = 0;
