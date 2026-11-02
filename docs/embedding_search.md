# Set up semantic search

Semantic search finds posts by meaning, not exact words. When it's on, agents add semantic results to the keyword results they already get from the `search_posts` tool, and AI search from the search bar and `/ask-channel` use it. This page covers requirements, settings, indexing, and when to run each reindex action. Semantic search is experimental and needs an Enterprise license. Without it, agents still search with keywords.

## How it works

Mattermost splits post text into chunks, sends each chunk to an embedding provider, and stores the resulting vectors in your PostgreSQL database with the pgvector extension. Searches embed the question and return the nearest posts.

- Search results come only from channels the requesting user belongs to. Deleted posts and channels are excluded.
- Deleting a post removes its embeddings. Mattermost data retention also removes orphaned embeddings.
- **Indexing sends the text of every indexed post to the embedding provider.** For data control, point it at a self-hosted OpenAI-compatible endpoint. See [Sovereign AI deployments](sovereign_ai.md).

## Requirements

- An Enterprise license.
- PostgreSQL with the `pgvector` extension available. Mattermost runs `CREATE EXTENSION IF NOT EXISTS vector`, so the database user needs permission, or you create the extension first. Half precision storage needs pgvector 0.7 or later, which Amazon RDS includes on supported engine versions.
- An embedding provider: OpenAI or an OpenAI-compatible API.

## Settings

Go to **System Console > Plugins > Agents > Embedding Search**.

| Setting | Notes |
|---|---|
| **Enable Embedding Search** | Turns semantic search on. Enabling it fills in OpenAI, pgvector, and 1536 dimensions. |
| **Vector Store Type** | **PostgreSQL pgvector**. |
| **Embedding Provider Type** | **OpenAI** or **OpenAI-compatible API**. Each takes an embedding **Model** and an **API Key**. The compatible type also takes an **API URL**. |
| **Dimensions** | The vector size your model produces, for example 768, 1024, or 1536. It must match the model. |
| **HNSW M** | Graph connections per row in the vector index. Default 8, range 2 to 100. Lower uses less memory and is slightly less accurate. |
| **Vector precision** | **Standard (vector)** is the default. **Half precision (halfvec)** uses less memory and disk. |
| **Index posts from the last N days** | `0` indexes all posts, which is the default. A positive value indexes only recent posts. |
| **Chunking Strategy** | **Sentences** (default), **Paragraphs**, or **Fixed Size**. |
| **Chunk Size** | Maximum characters per chunk. Default 1000. |
| **Chunk Overlap** | Characters shared between chunks. Default 200. |
| **Recency Bias** | Off by default. When on, newer posts rank higher among results chosen by relevance. |
| **Recency Half-Life (days)** | Default 7. A post this old loses half of its recency boost. |
| **Recency Floor** | Default 0.7, range 0.01 to 1. The lowest score multiplier for old posts. `1` turns recency off. |
| **Reindex Worker Count** | Concurrent workers during bulk reindexing. Default 4, maximum 32. Raise it if your provider's rate limits allow. Lower it to reduce load. |
| **Reindex Batch Size** | Posts fetched and embedded per batch. Default 200, maximum 1000. |
| **Reindex Index Strategy** | **Maintain index during reindex** (default) or **Drop and rebuild index after reindex**. See [Large databases](#large-databases). |

The defaults stay within OpenAI Tier 1 rate limits. On Azure OpenAI, throughput is capped by your deployment's tokens-per-minute quota.

## Index your posts

Indexing doesn't start by itself. After you save the settings, run an initial **Full Reindex**. For a large history, set **Index posts from the last N days** first. That choice is the historical cut. For example, `365` indexes only the last year.

Use the controls in the **Embedding Search** panel:

| Action | Use it when |
|---|---|
| **Full Reindex** | First setup, or after changing the embedding provider, model, dimensions, or vector precision. Clears the index and rebuilds it. Search is unavailable until it finishes. |
| **Resume Reindex** or **Start Over** | A reindex was interrupted. Resume continues from the checkpoint. Don't use Resume after a dimension or precision change, because it keeps the old table schema. |
| **Catch Up** | You increased the window, or you want to index posts missing from the index. Search stays available. It doesn't re-embed posts that are already indexed. |
| **Rebuild vector index** | You changed **HNSW M**. It rebuilds only the graph and reuses existing embeddings. Search is unavailable while it runs. It can't change the vector type. |
| **Cancel Reindexing** | Stop a running job. |
| **Index Health** and **Refresh** | Check status, post counts, missing posts, and the vector index state. |

Rules that aren't obvious:

- Saving a setting never starts a job. After you change **HNSW M** or the window, run the matching action.
- **Vector precision** changes drop the embeddings table. Run **Full Reindex**.
- Indexes created before **HNSW M** existed stay at pgvector's default of 16 until you run **Rebuild vector index**.
- Raising N and running **Catch Up** adds older posts without re-embedding. Lowering N doesn't delete anything. Search still returns posts already indexed, and memory use drops only after the next Full Reindex.
- Changing N while a job runs doesn't change that job's window. Cancel it and start a new one.
- Posts indexed after the initial run stay current automatically. **Catch Up** repairs holes.
- A job that stops updating for more than 10 minutes is flagged as stale, usually because its node crashed. Start a new run to take over from the checkpoint.

## Large databases

For tens of millions of posts, set **Reindex Index Strategy** to **Drop and rebuild index after reindex** before a Full Reindex. Keeping the HNSW index updated on every insert dominates the cost at scale. Dropping it, loading in bulk, and rebuilding once is usually an order of magnitude faster. The strategy applies only to full reindexes. **Catch Up** never drops the index.

- **Search is unavailable** from the start of the reindex until the index is rebuilt, and the API returns HTTP 503. Live posts are still indexed during the bulk load. During the final build, which can take hours, live indexing, deletions, and retention pause. Catch Up repairs new posts afterward.
- **Repair phase.** After the build, search returns, and a short `repairing` phase re-embeds posts edited while indexing was paused. Search works during it, but a few recent edits can be slightly stale. If the job stops before repair ends, the marker stays, so resume or reindex to finish.
- **Tune the build on the database.** Mattermost doesn't override PostgreSQL settings. Keep the HNSW graph in `maintenance_work_mem`. Estimate `rows × (4 × dimensions + graph)` bytes for standard vectors, or `rows × (2 × dimensions + graph)` for half precision. The graph overhead shrinks with **HNSW M**. As an example, about 1.4 KB per row at 256 dimensions with `m=8`, or about 28 GB for 20 million posts. Half precision isn't a 2x memory cut, because graph links stay the same size. Past that budget PostgreSQL spills to disk and the build can slow by 40 times. pgvector 0.6 and later can parallelize with `max_parallel_maintenance_workers`.
- **Crash recovery.** State is durable. After a restart, **Index Health** shows the index state, and search stays off until you resume or start a full reindex. The plugin never rebuilds during activation. A cancelled or failed bulk load leaves the index dropped, so the next run continues the same way.
- **Widening the window.** Catch Up after a large increase, such as one year to all posts, can take a long time because it maintains the index as it inserts. On a very large corpus a deferred Full Reindex can be faster, at the cost of search downtime.

## Notes for admins

- If the saved model, provider, dimensions, or precision no longer match the index, the panel shows **Embedding Model Changed** and search is disabled until a Full Reindex.
- After a license downgrade below Enterprise, semantic search stops. Turning it off is always allowed.
- Index control actions are written to the audit log as `reindexPosts`, `cancelReindexJob`, `catchUpReindex`, and `rebuildVectorIndex`.
