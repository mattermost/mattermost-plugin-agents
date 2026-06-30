---
description: Gin HTTP surface — middleware/auth tiers, route conventions, bridge/user/admin endpoints.
tags: [api, http, gin, auth, otel]
---

# api/AGENTS.md

All plugin HTTP routes. The base path is `/plugins/mattermost-ai/…` (the manifest id), **not** `/api/v1/…`.

- **The Gin router is rebuilt on every `ServeHTTP` call** (`gin.Default()` per request) — it's not a long-lived router. Middleware order: `otelgin.Middleware` → logger → metrics → route-specific auth.
- **Auth tiers (order matters):** `/bridge/v1/*` → `interPluginAuthorizationRequired` (`Mattermost-Plugin-ID` header, bypasses user auth); most routes → `MattermostAuthorizationRequired` (`Mattermost-User-Id`); `/admin/*` → admin auth (`PermissionManageSystem`); post/channel actions add `aiBotRequired` + post/channel authorization + bot usage restrictions.
- **Handler files are `api_<domain>.go` (+ `_test.go`).** Add an endpoint by registering it on the right router/group inside `ServeHTTP`, applying the existing middleware group, and mirroring it in `webapp/src/client.tsx` if user-facing. Watch route ordering (literal segments before `/:param`).
- Admin config lives at `GET`/`PUT /admin/config` (`handleGetConfig`/`handleSaveConfig`); GET clones before `normalizeAdminConfig` so it must not mutate the cached config. `POST /search/raw` returns semantic hits without an LLM (snake_case JSON, 503 when search is disabled). Metrics are served separately via `ServeMetrics`, not the Gin router.
- Thread/channel analysis endpoints require a Basics license. Client websocket events: `bots_invalidate`, `mcp_connection_updated`. `go test ./api/...`.
