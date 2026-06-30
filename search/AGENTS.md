---
description: User-facing semantic/RAG search — enrichment, the raw endpoint, and the SemanticSearchService.
tags: [search, rag, mcp, api]
---

# search/AGENTS.md

User-facing semantic layer: embedding search + Mattermost metadata enrichment + optional LLM answer. `*search.Search` implements `mcpserver/tools.SemanticSearchService` (the MCP-facing contract); `Options` and `RAGResult` are the shared domain types.

- `RunSearch` streams an LLM answer to a DM; `SearchQuery` returns JSON immediately. Internal default `Limit` is 5.
- The raw endpoint handler is registered as `POST /search/raw` (full path `/plugins/mattermost-ai/search/raw`); it returns enriched hits **without** an LLM, default limit 10 / max 50, permissions via the `Mattermost-User-Id` header. The external MCP HTTP client targets `/api/v1/search/raw` — verify the path when touching that callback.
- Search is disabled (factory returns nil) when unlicensed, and is set to nil at activation when the stored indexer model info mismatches the current provider/dimensions/model until a reindex updates it.
- OTel spans: `"run search"` and `"search query"` only (intentionally not on the sync `RunSearch` return). `go test ./search/...`.
