# api/AGENTS.md

Plugin HTTP handlers (Gin). Root `/AGENTS.md` applies.

## Router shape

- `ServeHTTP` builds a fresh `gin.Default()` and registers all routes inline on every request (`api/api.go`) — there is no global router singleton. Tests call `a.ServeHTTP` directly.
- External URLs are `{SiteURL}/plugins/mattermost-ai/{route}`. Most routes have no `/api/v1` prefix; the inter-plugin bridge is under `/bridge/v1`. Admin config is `/admin/config`.
- Tracing/metrics middleware (`otelgin.Middleware`, `metricsMiddleware`) wrap all routes.

## Auth layers (registration order matters)

- `/bridge/v1/*` → `interPluginAuthorizationRequired` (`Mattermost-Plugin-ID` header).
- `/mcp-server/*` → OAuth metadata public; `/mcp` requires `mcpAuthMiddleware`.
- Then `MattermostAuthorizationRequired` (non-empty `Mattermost-User-Id`; no permission check by itself).
- `/admin/*` → `mattermostAdminAuthorizationRequired` (`PermissionManageSystem`).
- Bot/post/channel routes add `aiBotRequired` / `postAuthorizationRequired` / `channelAuthorizationRequired` + bot usage restrictions.

## Adding an endpoint

Register inside `ServeHTTP` under the correct group so it inherits the right middleware. Order-sensitive: register literal segments before `:param` routes (e.g. `/agents/models/fetch` before `/agents/:agentid`). Agent errors go through `abortAgentRequest` (4xx messages are user-visible, 5xx are sanitized). User-facing strings belong in webapp i18n. If the endpoint changes HA-relevant state, publish a cluster event and handle it in `OnPluginClusterEvent`.

## Admin config

`handleGetConfig` clones config before `normalizeAdminConfig` so GET never mutates the cached in-memory config. `handleSaveConfig` → `configStore.SaveConfig` → `configUpdater.Update` → cluster publish.

## Testing

Use `SetupTestEnvironment(t)` (`api_test.go`) and prefer driving `ServeHTTP` over calling handlers directly. No new mocking libraries.
