---
description: RAG core interfaces and the composite chunk→embed→store/search orchestration.
tags: [embeddings, rag, vectors, providers]
---

# embeddings/AGENTS.md

Core RAG interfaces (`EmbeddingSearch`, `VectorStore`, `EmbeddingProvider`, `PostDocument`) plus `CompositeSearch`, the only production `EmbeddingSearch`. Wiring/factory lives in `search/embeddings.go` (`InitEmbeddingsSearch`); the running service is a hot-swapped `atomic.Pointer`.

- Provider types: `openai`, `openai-compatible`, `bifrost` (sub-providers openai/azure/cohere/bedrock), `mock`. Vector store: only `pgvector` is wired. Service type: only `composite`.
- Empty `cfg.Type` disables search (returns `nil, nil` — not an error); init also requires `IsBasicsLicensed()` and `Dimensions > 0`.
- Indexing batches embeddings (`BatchCreateEmbeddings`); query search uses a single `CreateEmbedding`.
- **Add an embedding provider:** implement `EmbeddingProvider`, add a `ProviderType*` constant, add a case in `search/newEmbeddingProvider`, and wire admin UI config if user-facing. **Add a vector store:** implement `VectorStore` + a case in `search/newVectorStore`.
- Gotcha: integration tests (`integration_test.go`) hit a hardcoded local `postgres://mmuser:mostest@localhost:5432/postgres` (skip if unavailable), unlike `postgres/`'s testcontainers.
