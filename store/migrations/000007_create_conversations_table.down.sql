DROP TABLE IF EXISTS LLM_Turns;
DROP TABLE IF EXISTS LLM_Conversations;

-- Restore the LLM_PostMeta table created by migration 000002 so rolling back
-- leaves the schema in a state consistent with migration 000004.
CREATE TABLE IF NOT EXISTS LLM_PostMeta (
    RootPostID TEXT NOT NULL PRIMARY KEY,
    Title TEXT NOT NULL
);
