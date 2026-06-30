# embeddings/AGENTS.md

Scoped instructions for embedding search plumbing. Root rules in `/AGENTS.md` and `/search/AGENTS.md` apply.

## Commands

- Unit/integration tests: `go test -v ./embeddings/...`.
- Related search tests: `go test -v ./search/...`.

## Conventions

- `CompositeSearch` owns chunk -> embed -> vector search composition.
- Provider type constants live in `embeddings.go`; keep admin config mapping in sync.
- Semantic quality evals live with `search/`, not this package.
- Check integration tests for their required Postgres/pgvector DSN before running them locally.
