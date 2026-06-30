---
description: Gin HTTP surface for the plugin — route registration, layered auth middleware, agent CRUD, admin/bridge/MCP endpoints.
tags: [api, gin, http, auth, agents]
---

# api/AGENTS.md

All plugin HTTP routes. Root `/AGENTS.md` still applies.

## Key files

- `api/api.go` — `API`, `New`, `ServeHTTP` (router + middleware), metrics, auth helpers.
- `api/api_agents.go` — `CreateAgentRequest`, agent CRUD, quotas, permissions.
- `api/api_config.go` — `GET`/`PUT /admin/config` (`normalizeAdminConfig`).
- `api/api_admin.go` — reindex, MCP admin (`mattermostAdminAuthorizationRequired`).
- `api/api_post.go`, `api/api_channel.go` — post/channel AI actions.
- `api/middleware_mcp.go`, `api/api_llm_bridge.go`, `api/api_bridge_mcp.go` — MCP OAuth + inter-plugin bridge.
- `api/api_search.go` — `POST /search/raw` callback for the MCP HTTP search service.

## Conventions & gotchas

- **Gin + otelgin:** `otelgin.Middleware("mattermost-ai-agents")` wraps every request — HTTP spans are automatic. The router is currently rebuilt per `ServeHTTP` call.
- **Auth tiers (order matters):**
  - `/bridge/v1/*` → `interPluginAuthorizationRequired` (`Mattermost-Plugin-ID`).
  - `/mcp-server/*` → optional `mcpAuthMiddleware`.
  - Most routes → `MattermostAuthorizationRequired` (`Mattermost-User-Id`).
  - `/admin/*` → `mattermostAdminAuthorizationRequired` (`PermissionManageSystem`).
  - Post/channel AI routes → `aiBotRequired` + `postAuthorizationRequired` / `channelAuthorizationRequired`.
- **Route ordering:** register static sub-paths (e.g. `/agents/models/fetch`) before param routes (`/:agentid`) so they aren't captured as the param.
- **Errors:** use `abortAgentRequest` for user-visible JSON errors in agent routes; don't leak internal errors.
- **Config:** persist via `store.SaveConfig` → `configUpdater.Update`; don't write the Mattermost `config.json`. `normalizeAdminConfig` mutates config on GET/PUT (GET clones first).
- Tool output / entity formatting goes through `format/` (see `format/AGENTS.md`).

Adapters live in `mmapi/` (Mattermost API + Postgres, Postgres-only — `mmapi.NewDBClient` panics otherwise).
