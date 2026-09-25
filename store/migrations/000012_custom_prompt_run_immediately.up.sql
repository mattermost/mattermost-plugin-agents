ALTER TABLE LLM_CustomPrompts
    ADD COLUMN IF NOT EXISTS RunImmediately BOOLEAN NOT NULL DEFAULT FALSE;

-- No backfill: FALSE preserves today's behavior for every existing prompt
-- (insert the rendered text into the draft and let the user press send).
