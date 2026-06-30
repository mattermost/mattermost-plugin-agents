# search/AGENTS.md

Semantic search / RAG / indexing cluster: `search/`, `embeddings/`, `postgres/`, `chunking/`, `indexer/`. Root `/AGENTS.md` and `mcpserver/AGENTS.md` (the MCP `SemanticSearchService` / external search callback) still apply.

## Where code lives

- RAG + provider/store wiring: `search/search.go`, `search/embeddings.go` (`InitEmbeddingsSearch`, `newEmbeddingProvider`, `newVectorStore`).
- Pipeline: `embeddings.CompositeSearch` (chunk → embed → store). Store: `postgres.PGVector` → table `llm_posts_embeddings`. Incremental/bulk: `indexer.Indexer`.

## Tests

- Unit (no Docker): `go test ./search/... ./embeddings/... ./chunking/... ./indexer/...` (mock `embeddings.EmbeddingSearch` and `mmapi.Client`; no DB).
- pgvector store tests (Testcontainers `pgvector/pgvector:pg17`): `go test ./postgres/...`.
- Fast pgvector iteration: `PGVECTOR_TEST_DSN='postgres://user:pass@host:5432/db?sslmode=disable'` — **URL form only** (libpq key=value rejected).
- `embeddings/integration_test.go` and `search/search_eval_test.go` do **not** use `PGVECTOR_TEST_DSN`; they hardcode a localhost DSN and `t.Skip` if unreachable.
- RAG eval (real provider + local pgvector): `GOEVALS=1 OPENAI_API_KEY=… go test -v ./search/ -run Eval -timeout 10m`.

## Config & wiring invariants

- `EmbeddingSearchConfig.Type == ""` → search disabled; `InitEmbeddingsSearch` returns `(nil, nil)`, not an error.
- Requires `enterprise.LicenseChecker.IsBasicsLicensed()`.
- `cfg.Dimensions` must be > 0 and must match both the embedding provider output and the pgvector column width.
- Only `SearchTypeComposite` + `VectorStoreTypePGVector` are supported today.
- **Model-change gate:** if KV `indexer_model_info` disagrees with the current provider/model/dimensions, the plugin nils out live search until a full reindex (`clearIndex=true`) runs and `Indexer.SaveModelInfo` updates it. This is intentional, not a deadlock — the config listener re-inits afterward.

## pgvector schema (no migration files)

`postgres.NewPGVector` runs `CREATE EXTENSION vector` + `CREATE TABLE IF NOT EXISTS llm_posts_embeddings` + HNSW index at runtime. The `vector(N)` dimension is fixed at first table creation — changing `Dimensions` does not alter an existing table; it requires `Clear` + full reindex. `PGVector.Search` **requires** `embeddings.SearchOptions.UserID` (joins `ChannelMembers`/`Channels`/`Posts` for permission filtering); empty `UserID` errors. The RAG layer (`search.Options`) passes the requesting user through to it. Similarity assumes normalized vectors (HNSW `vector_l2_ops`, score `1 - L2²/2`).

## Adding an embedding provider / vector store

Extend `search/embeddings.go` (`newEmbeddingProvider` switch / `mapEmbeddingProvider`, or a new `VectorStoreType` in `newVectorStore`) — not `embeddings/`. Real providers implement `embeddings.EmbeddingProvider` (see `bifrost.NewEmbeddingProvider`). Test provider: `embeddings.ProviderTypeMock` → deterministic vectors.

## Indexer & chunking

- Post body for indexing is `format.PostBody(post)` — never format inline.
- `shouldIndexPost` skips bots, non-default post types, deleted, empty, and bot-DM channels.
- Reindex uses cluster mutex `ai_reindex_job` and KV keys `reindex_job_status` / `indexer_cursor` / `indexer_model_info`; resume preserves the cursor when `clearIndex=false`.
- Embedding-path chunking is `chunking.ChunkText` (strategies `sentences`/`paragraphs`/`fixed`). `chunking.SplitPlaintextOnSentences` is a **separate** API used by `meetings/`, not the indexer.

## Never

- Don't duplicate MCP search types here (see `mcpserver/AGENTS.md`).
- Don't add embedding schema via hand-written SQL migrations elsewhere; change `postgres.NewPGVector`.
