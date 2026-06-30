# search/AGENTS.md

Scoped instructions for semantic search, embeddings, pgvector, and indexing. Root rules in `/AGENTS.md` still apply.

## Architecture

- Search stack: `search.InitEmbeddingsSearch` -> `embeddings.CompositeSearch` -> `postgres.PGVector`.
- `InitEmbeddingsSearch` returning `(nil, nil)` when disabled is expected behavior.
- Search requires the license checker to allow Basics features.
- Model compatibility checks can intentionally disable search until admin reindex updates stored model metadata.
- `search.Search` enriches embedding results into `RAGResult`; MCP embedded mode receives it directly.
- External MCP search uses the plugin callback endpoint `/search/raw`.

## Commands

- Search stack tests: `go test -v ./search/... ./embeddings/... ./postgres/... ./indexer/...`
- Pgvector tests with Docker: `go test -v ./postgres/...`
- Reuse local pgvector: `PGVECTOR_TEST_DSN=postgres://... go test -v ./postgres/...`
- Search evals need live services: `GOEVALS=1 OPENAI_API_KEY=... go test -v ./search/... -run Eval`

## Gotchas

- `postgres.PGVector` uses advisory locks on sorted post IDs to avoid deadlocks.
- Dimensions are fixed when pgvector tables are created.
- Do not duplicate `search.Options` or `search.RAGResult` in MCP code.
- Use `telemetry.DetachContext` for async user-facing search processing.
