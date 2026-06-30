# indexer/AGENTS.md

Scoped instructions for background post indexing. Root rules in `/AGENTS.md` and `/search/AGENTS.md` apply.

## Commands

- Unit tests: `go test -v ./indexer/...`.
- Related search stack tests: `go test -v ./search/... ./embeddings/...`.

## Conventions

- Background reindex jobs may start from their own root context; request-scoped incremental indexing should use the caller's context.
- Keep the embedding search getter pattern aligned with `search/` wiring.
- Search/RAG behavior is validated in `search/` tests and evals.
