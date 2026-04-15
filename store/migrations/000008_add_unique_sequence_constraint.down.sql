DROP INDEX IF EXISTS idx_llm_turns_conversation_sequence;
CREATE INDEX idx_llm_turns_conversation ON LLM_Turns(ConversationID, Sequence);
