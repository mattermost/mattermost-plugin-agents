ALTER TABLE Agents_UserAgents
    DROP COLUMN IF EXISTS StructuredOutputEnabled,
    DROP COLUMN IF EXISTS ThinkingBudget,
    DROP COLUMN IF EXISTS ReasoningEffort,
    DROP COLUMN IF EXISTS ReasoningEnabled,
    DROP COLUMN IF EXISTS EnabledNativeTools,
    DROP COLUMN IF EXISTS DisableTools,
    DROP COLUMN IF EXISTS EnableVision,
    DROP COLUMN IF EXISTS Model;
