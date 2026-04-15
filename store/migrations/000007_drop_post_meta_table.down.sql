-- No foreign keys: consistent with Mattermost conventions.
-- IF NOT EXISTS is safe for existing installs that already have this table.
CREATE TABLE IF NOT EXISTS LLM_PostMeta (
    RootPostID TEXT NOT NULL PRIMARY KEY,
    Title TEXT NOT NULL
);

-- Clean up FK constraint from old LLM_Threads table if it exists
ALTER TABLE IF EXISTS LLM_Threads DROP CONSTRAINT IF EXISTS llm_threads_rootpostid_fkey;
