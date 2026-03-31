CREATE TABLE IF NOT EXISTS LLM_Turns (
    ID TEXT PRIMARY KEY,
    ConversationID TEXT NOT NULL,
    PostID TEXT,
    Role TEXT NOT NULL,
    Content JSONB NOT NULL,
    TokensIn BIGINT NOT NULL DEFAULT 0,
    TokensOut BIGINT NOT NULL DEFAULT 0,
    Sequence INTEGER NOT NULL,
    CreatedAt BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_llm_turns_conversation ON LLM_Turns(ConversationID, Sequence);
CREATE INDEX IF NOT EXISTS idx_llm_turns_post ON LLM_Turns(PostID) WHERE PostID IS NOT NULL;
