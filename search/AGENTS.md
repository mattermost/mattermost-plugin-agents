# search/AGENTS.md

Scoped instructions for semantic search and the RAG pipeline. Root rules in `/AGENTS.md` still apply.

## Scope

- Covers `search/`, `embeddings/`, `indexer/`, vector-path `chunking/`, and their `postgres/` pgvector dependency.
- Postgres-specific schema details live in `/postgres/AGENTS.md`.

## Layers

- `search/embeddings.go`: factory and license-gated wiring.
- `embeddings/`: embedding provider/store interfaces and `PostDocument` types.
- `indexer/`: real-time indexing, reindex jobs, health checks, and model compatibility.
- `chunking.ChunkText`: embedding chunking only.
- `postgres/`: pgvector store and permission-filtered search SQL.
- `search/search.go`: user-facing RAG requests, enrichment, and async search.

## Type ownership

- Reuse `search.Options` and `search.RAGResult` in MCP tools; do not duplicate them.
- `embeddings` owns vector-store input types. `search` owns user-facing result types.

## Indexing rules

- Keep `indexer.shouldIndexPost` aligned with health-check SQL.
- Skip bot posts, bot DMs, empty messages, non-default post types, and deleted posts.
- Indexed content must use `format.PostBody(post)`.
- Model or dimension changes require reindex compatibility handling.

## Jobs and HA

- Reindexing uses cluster mutex `ai_reindex_job`, KV compare-and-swap, stale heartbeat reclaim, and a cutoff timestamp to prevent races.
- `getSearch func() embeddings.EmbeddingSearch` may return nil when search is disabled; treat nil as disabled, not an error.
- `search.SetConversationService` breaks a circular init dependency; call order matters in server startup.

## Commands

- Unit stack: `go test -v ./search/... ./embeddings/... ./indexer/... ./chunking/...`
- pgvector tests: `go test -v ./postgres/...`
- Existing pgvector: `PGVECTOR_TEST_DSN='postgres://user:pass@host:5432/db?sslmode=disable' go test -v ./postgres/...`
- Search evals, provider env needed: `GOEVALS=1 go test -v ./search/ -run Eval`
- Semantic search e2e: `cd e2e && npx playwright test tests/semantic-search/ --reporter=list`

## Pointers

- LLM-facing formatting: `/format/AGENTS.md`.
- pgvector details: `/postgres/AGENTS.md`.
- MCP search tool boundary: `/mcpserver/AGENTS.md`.
