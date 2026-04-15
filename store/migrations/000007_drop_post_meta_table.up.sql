-- Backfill titles from the old LLM_PostMeta table into LLM_Conversations.
-- This is a no-op on fresh installs (both tables are empty).
UPDATE LLM_Conversations
SET Title = pm.Title,
    UpdatedAt = EXTRACT(EPOCH FROM NOW())::BIGINT * 1000
FROM LLM_PostMeta pm
WHERE LLM_Conversations.RootPostID = pm.RootPostID
  AND (LLM_Conversations.Title = '' OR LLM_Conversations.Title IS NULL);

DROP TABLE IF EXISTS LLM_PostMeta;
