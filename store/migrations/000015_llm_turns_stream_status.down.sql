DROP INDEX IF EXISTS idx_llm_turns_streaming_updatedat;

ALTER TABLE LLM_Turns
    DROP COLUMN IF EXISTS UpdatedAt;

ALTER TABLE LLM_Turns
    DROP COLUMN IF EXISTS Status;
