---
description: RAG pipeline — semantic search orchestration over embeddings/pgvector, MM metadata enrichment, and the LLM answer path.
tags: [search, rag, embeddings, pgvector, postgres]
---

# search/AGENTS.md

RAG orchestration plus the wider embeddings pipeline (`search/`, `embeddings/`, `postgres/`, `chunking/`). Root `/AGENTS.md` still applies.

## Pipeline

Index: post hook / reindex → `indexer` → `embeddings.PostDocument` (text via `format.PostBody`) → `chunking.ChunkText` → `EmbeddingProvider.BatchCreateEmbeddings` → `postgres.PGVector.Store` (table `llm_posts_embeddings`).

Query: `search.Search` → `embeddings.EmbeddingSearch` (embed query → `PGVector.Search` with `ChannelMembers` permission filter) → enrich with MM metadata → optional LLM answer (`search_system` prompt) or MCP `search_posts` / `/api/v1/search/raw`.

## Key files

- `search/search.go` — `Search`, `RAGResult`, `Options`, `RunSearch`, `SearchQuery`, `Enabled()`.
- `search/embeddings.go` — **`InitEmbeddingsSearch`** factory (wires `postgres.NewPGVector` + provider → `embeddings.NewCompositeSearch`).
- `embeddings/composite.go` — `CompositeSearch` (store/search). `postgres/pgvector.go` — the only `VectorStore`.

## Conventions & gotchas

- **`Options.UserID` is required** for queries — pgvector joins `ChannelMembers` for permission filtering.
- **License:** `InitEmbeddingsSearch` requires `IsBasicsLicensed()`.
- `Search` implements `mcpserver/tools.SemanticSearchService`; `SetConversationService` breaks an init cycle.
- Embedding provider types: `openai`, `openai-compatible`, `bifrost`, `mock`. Vector store: `pgvector` only. Default chunking is sentences (1000/200 overlap) when `ChunkSize == 0`.
- `CompositeSearch.Search` returns raw chunk hits (no post-level dedup); dedup happens in MCP result formatting.
- All post text for indexing/enrichment goes through `format/`.

## Tests

`go test ./search/... ./embeddings/...`. Postgres: `postgres/pgvector_test.go` boots `pgvector/pgvector:pg17` via testcontainers (needs Docker), or set `PGVECTOR_TEST_DSN` (URL-style DSN, e.g. `postgres://…`).
