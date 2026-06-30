---
description: Text chunking for embeddings (character-based, langchaingo recursive splitter).
tags: [chunking, embeddings, rag]
---

# chunking/AGENTS.md

Splits post text before embedding; chunk metadata flows into pgvector rows and search results. Production uses `ChunkText` (via `CompositeSearch.Store`).

- Defaults: `ChunkSize` 1000, `ChunkOverlap` 200, strategy `sentences` — **measured in characters, not tokens** (the admin guide's "tokens" wording is wrong; trust the code and webapp helptext). Strategies (`sentences`/`paragraphs`/`fixed`) all use `langchaingo` `NewRecursiveCharacter`.
- If splitting yields a single piece identical to the input, the result is `IsChunk: false`, `TotalChunks: 1`.
- `SplitPlaintextOnSentences` (`text_splitting.go`) is a separate helper used by `meetings/`, not the RAG pipeline.
- Changing chunk settings after indexing requires a reindex for consistent boundaries. `go test ./chunking/...`.
