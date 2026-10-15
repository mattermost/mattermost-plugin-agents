ALTER TABLE LLM_Turns
    ADD COLUMN IF NOT EXISTS Status TEXT NOT NULL DEFAULT 'complete';

ALTER TABLE LLM_Turns
    ADD COLUMN IF NOT EXISTS UpdatedAt BIGINT NOT NULL DEFAULT 0;

UPDATE LLM_Turns SET UpdatedAt = CreatedAt WHERE UpdatedAt = 0;

CREATE INDEX IF NOT EXISTS idx_llm_turns_streaming_updatedat
    ON LLM_Turns(UpdatedAt) WHERE Status = 'streaming';
