DROP INDEX IF EXISTS idx_llm_turns_conversation;
CREATE UNIQUE INDEX idx_llm_turns_conversation_sequence ON LLM_Turns(ConversationID, Sequence);
