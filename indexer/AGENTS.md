---
description: Keeps the vector index in sync via realtime hooks and resumable reindex jobs.
tags: [indexer, rag, jobs, embeddings]
---

# indexer/AGENTS.md

Keeps `llm_posts_embeddings` in sync: realtime post hooks plus admin reindex/catch-up jobs, health checks, and model-compatibility tracking.

- Realtime: `MessageHasBeenPosted/Updated` → `IndexPost`, `MessageHasBeenDeleted` → `DeletePost`, `RunDataRetention` → `DeleteOrphaned`. Indexed text is `format.PostBody`.
- `shouldIndexPost` skips: empty-and-attachmentless posts, any bot author, non-`PostTypeDefault`, soft-deleted, and bot DMs.
- **Job state lives in plugin KV, not `store/`** (cursor, status, model info, last-indexed ts), guarded by the `ai_reindex_job` cluster mutex; batch size 100, cursor pagination on `(CreateAt, Id)`.
- Model info is saved only after a full reindex with `clearIndex=true`; a catch-up pass requires a prior full reindex. A model mismatch disables search until reindex.
- Admin routes: `POST/GET /admin/reindex*`. `go test ./indexer/...`.
