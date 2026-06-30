# search/AGENTS.md

Scoped instructions for semantic search and RAG. Root rules in `/AGENTS.md` apply.

## Architecture

- `search.Search` orchestrates RAG answer generation and streaming.
- Embedding/vector wiring lives in `search/embeddings.go`; provider/config changes usually start there plus admin config.
- The stack is `search/` -> `embeddings/` -> `postgres/` pgvector, with `indexer/` feeding documents.
- `RunSearch` launches async work with `telemetry.DetachContext`; keep the search span open until the goroutine completes.
- `SetConversationService` breaks plugin wiring cycles; preserve activation order.
- RAG result formatting must go through `format/`.
- `mcpserver/AGENTS.md` covers the MCP `SemanticSearchService` bridge; do not duplicate DTOs there.

## Commands

- Unit tests: `go test -v ./search/... ./embeddings/... ./indexer/...`.
- pgvector tests with Docker: `go test -v ./postgres/...`.
- Fast pgvector path: `PGVECTOR_TEST_DSN='postgres://user:pass@host:5432/db?sslmode=disable' go test -v ./postgres/...`.
- Citation evals: `GOEVALS=1 LLM_PROVIDER=openai OPENAI_API_KEY=... ./bin/evalviewer check -v ./search`.
- Semantic search evals need `GOEVALS=1`, `OPENAI_API_KEY`, and local pgvector infrastructure.

## Gotchas

- `postgres/pgvector_test.go` starts `pgvector/pgvector:pg17` through testcontainers by default.
- `embeddings/` integration tests may expect a fixed local Postgres DSN; check the test before assuming testcontainers.
- Search evals are not part of the default `make evals-ci` package list.

## Pointers

- Embedding/search admin setup: `docs/admin_guide.md`.
- OpenTelemetry async rules: `/telemetry/AGENTS.md`.
