# postgres/AGENTS.md

Scoped instructions for pgvector embedding storage. Root rules in `/AGENTS.md` still apply.

## Scope

- Owns `llm_posts_embeddings` for semantic search vectors.
- This table is not managed by `store/migrations/`; `NewPGVector` creates and upgrades it at runtime.
- Rows reference Mattermost `Posts(Id)` with cascading deletes.

## Initialization

- Creates the `vector` extension if needed.
- Creates table and HNSW index for the configured embedding dimensions.
- Dimension/model mismatches require reindexing; see indexer compatibility checks.

## Concurrency

- `Store` uses sorted per-post advisory locks to avoid deadlocks.
- Store operations delete and reinsert chunks for a post batch so shrinking chunk counts are handled correctly.

## Search

- Permission filtering belongs in SQL through channel membership joins.
- `MinScore` is converted to a vector-distance threshold.

## Commands

- Docker-backed pgvector tests: `go test -v ./postgres/...`
- Existing pgvector instance: `PGVECTOR_TEST_DSN='postgres://user:pass@host:5432/db?sslmode=disable' go test -v ./postgres/...`

## Pointers

- Full RAG pipeline: `/search/AGENTS.md`.
- Plugin DB migrations: `/store/AGENTS.md`.
