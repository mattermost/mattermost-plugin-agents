---
description: Post indexing for RAG — incremental hooks plus resumable reindex/catch-up jobs with KV state and cluster locks.
tags: [indexer, reindex, jobs, kv, cluster]
---

# indexer/AGENTS.md

Incremental indexing (post hooks) and batch reindex/catch-up jobs into the embeddings store. Root `/AGENTS.md` still applies.

## Key files

- `indexer/indexer.go` — `Indexer`, `IndexPost`, `DeletePost`, `RunDataRetention`, `StartReindexJob`, `StartCatchUpJob`, `CheckIndexHealth`, model-compat checks.
- `indexer/indexer_job.go` — KV keys, `JobStatus`, `runReindexJob`, cursor + catch-up pass.

## Conventions & gotchas

- **KV keys:** `reindex_job_status`, `indexer_cursor`, `indexer_model_info`, `indexer_last_indexed_ts`.
- **Index eligibility (`shouldIndexPost`):** non-empty message or attachments; not a bot; `PostTypeDefault`; not deleted; not a bot-DM channel.
- **Reindex jobs:** guarded by cluster mutex `ai_reindex_job` with CAS on KV; resumable via cursor; stale threshold 10m; a catch-up pass picks up posts created during the job; `clearIndex` calls `search.Clear()`.
- **Health check** compares the DB post count (excluding bots/bot-DMs) against `COUNT(DISTINCT post_id)` in `llm_posts_embeddings`.
- Changing the embedding model invalidates the index via `indexer_model_info` — handle model-compat before reindexing.
