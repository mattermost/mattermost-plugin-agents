---
description: pgvector implementation of the embeddings VectorStore (schema-as-code, not Morph migrations).
tags: [postgres, pgvector, embeddings, vectors]
---

# postgres/AGENTS.md

Pgvector-backed `embeddings.VectorStore` for post embeddings. Just `pgvector.go` + tests — **no Morph migration files**; the schema is DDL-in-Go inside `NewPGVector` (plugin SQL tables live in `store/` instead).

- Table `llm_posts_embeddings` is created in the Mattermost DB with an FK to core `Posts(Id) ON DELETE CASCADE`. The `vector(N)` width is baked in at first creation from `PGVectorConfig.Dimensions` — **changing dimensions without recreating/reindexing breaks search**.
- Indexes: HNSW `vector_l2_ops`; scores are derived from L2 distance (`1 - L2²/2`), which assumes normalized vectors.
- `Store` deletes all rows for a `post_id` then re-inserts (prevents orphan chunks when chunk count shrinks); per-post `pg_advisory_xact_lock` guards concurrent chunk writes.
- `Search` **requires `SearchOptions.UserID`** and enforces channel membership via joins on core `Channels`/`ChannelMembers`/`Posts`. Limit clamps to `maxSearchLimit = 1000`.
- Tests boot `pgvector/pgvector:pg17` via testcontainers; set `PGVECTOR_TEST_DSN` (URL-style DSN only, not libpq key=value) to use an existing instance. `go test ./postgres/...`.
