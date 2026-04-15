-- Remove duplicate (ConversationID, Sequence) rows keeping the earliest.
-- This is a no-op if no duplicates exist.
DELETE FROM LLM_Turns t1
USING LLM_Turns t2
WHERE t1.ConversationID = t2.ConversationID
  AND t1.Sequence = t2.Sequence
  AND t1.ID <> t2.ID
  AND (t1.CreatedAt > t2.CreatedAt OR (t1.CreatedAt = t2.CreatedAt AND t1.ID > t2.ID));

DROP INDEX IF EXISTS idx_llm_turns_conversation;
CREATE UNIQUE INDEX idx_llm_turns_conversation_sequence ON LLM_Turns(ConversationID, Sequence);
