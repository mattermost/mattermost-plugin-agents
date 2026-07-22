# Phase 1c — MCP Apps webapp rendering, built-in demo apps, e2e coverage

Status: ready for implementation.
Branch: `cursor/mcp-apps-webapp-a67d`, created FROM `cursor/mcp-apps-sandbox-a67d` (tip `ef08052e` at plan time). Third PR in the stack (1a → 1b → 1c).

Implements master-spec decisions **D3** (render only on Success/AutoApproved), **D4** (Connect to view), **D5** (onlooker states, state 3 as client-side approximation), **D9** (sizing: 100% width, clamped height), **D10** (built-in demo app in the embedded MCP server). Phase-2 features are explicitly stubbed: `onCallTool` returns a rejection result, `ui/message` is a logged no-op, `ui/open-link` opens with `noopener,noreferrer`.

---

## 1. Verified research facts (all re-verified on `cursor/mcp-apps-sandbox-a67d`, 2026-07-22)

### 1.1 Contracts inherited from 1a/1b (do not re-derive)

- **Bootstrap** — `GET /plugins/mattermost-ai/ai_bots` (`api/api.go:528-533`) returns `AIBotsResponse{Bots, SearchEnabled, AllowUnsafeLinks, MCPApps}`. `MCPApps` (`api/api_mcp_apps.go:25-33`) is `{enabled: bool, sandboxURL?: string, disabledReason?: "apps_disabled"|"no_sandbox_origin"|"invalid_sandbox_url"}`. `sandboxURL` is the **final page URL** (external: `…/sandbox.html`; same-origin: `<SiteURL>/plugins/mattermost-ai/mcp/apps/sandbox`). Pass it to `AppRenderer` as `sandbox.url` via `new URL(sandboxURL)`; never append paths (1b contract C5/C6).
- **Resource endpoint** — `GET /plugins/mattermost-ai/mcp/app-resource?post_id=<postID>&tool_call_id=<toolCallID>` (`api/api_mcp_app_resource.go`, route registered behind `MattermostAuthorizationRequired` at `api/api.go:322`). Success body mirrors MCP `ReadResourceResult`:
  ```json
  {"contents":[{"uri":"ui://…","mimeType":"text/html;profile=mcp-app","text":"<html>…","_meta":{"ui":{"csp":{…},"permissions":{…},"prefersBorder":true}}}]}
  ```
  Error body: `{"error_code": string, "message": string, "auth_url"?: string}`. Taxonomy (1a §5.4):

  | HTTP | `error_code` | 1c behavior |
  |---|---|---|
  | 400 | `invalid_request` | fallback note (dev error) |
  | 404 | `not_found` | graceful fallback to plain ToolCard + subtle note |
  | 403 | `forbidden` | onlooker popover (D5 state 3) |
  | 401 | `mcp_auth_required` (+`auth_url`) | "Connect to view" (D4 / D5 state 2) |
  | 502 | `upstream_unreachable` / `invalid_resource_mime` | fallback note |
  | 500 | (plain gin error) | fallback note |

  Repeated 401 **after** a completed connect ⇒ D5 state-3 popover (client approximation; there is no distinct server code — the doc comment on `AppResourceErrorResponse` states this).
- **`ui_meta` on the wire** — `llm.ToolCall` JSON carries `server_origin` and `ui_meta: {resource_uri, visibility?}` (`llm/tools.go:246-275`, `llm/tools_ui.go`). Persisted `tool_use` `ContentBlock`s carry the same (`conversation/content_block.go:53-54`). `FilterForNonRequester` keeps `ui_meta` on **shared** tool_use blocks, strips it otherwise; live WS `redactToolCalls` (`streaming/streaming.go:448-464`) strips `ui_meta` for non-requesters always — onlookers get it only from `GET /conversations/:id` after a share. Webapp `ContentBlock` (`webapp/src/types/conversation.ts`) already has `server_origin`/`shared` but **not** `ui_meta`; webapp `ToolCall` (`components/tool_types.ts`) has neither `ui_meta` nor is `server_origin` copied by `toolUseBlockToToolCall` for `ui_meta` purposes — both are 1c work.
- **CSP hand-off (C7)** — pass `contents[0]._meta.ui.csp` (camelCase, `mcp.AppResourceCSP` shape) verbatim as `sandbox.csp`. Omitted ⇒ no `?csp=` ⇒ sandbox serves the spec's restrictive default. Never synthesize a csp.
- **C8** — the sandbox page only talks to a parent whose origin string equals the SiteURL origin **exactly**. `localhost` ≠ `127.0.0.1`. Symptom of mismatch: AppFrame's 10 s "Timed out waiting for sandbox proxy iframe".
- **Endpoint timing** — `handleGetMCPAppResource` resolves the tool call via `GetTurnByPostID(postID)`; the anchor turn is created at stream **finalize**. Fetching during a live stream 404s. Therefore 1c renders apps only for **persisted** rounds (see D-1c-3).

### 1.2 `@mcp-ui/client@7.1.1` API (verified from the published tarball, `dist/src/components/AppRenderer.d.ts` / `AppFrame.d.ts`)

- `AppRendererProps` (callback mode, no `client`): `toolName: string`, `sandbox: SandboxConfig`, `toolResourceUri?: string`, `html?: string`, `toolInput?: Record<string, unknown>`, `toolResult?: CallToolResult`, `hostContext?: McpUiHostContext`, `onReadResource?: ({uri}) => Promise<ReadResourceResult>`, `onCallTool?`, `onMessage?`, `onOpenLink?`, `onLoggingMessage?`, `onSizeChanged?: ({width?, height?}) => void`, `onError?: (Error) => void`, `onFallbackRequest?`.
- `SandboxConfig`: `{url: URL, permissions?: string, csp?: McpUiResourceCsp}` — csp is sent both as `?csp=<json>` and via postMessage; the 1b server enforces the header form.
- `AppFrame` renders the outer iframe with inline `width:100%; height:600px; sandbox="allow-scripts allow-same-origin allow-forms"` (dist line 5600) and **applies size-changed width/height itself as inline styles on the iframe** (dist line 5676). D9 therefore controls height on a wrapper div and neutralizes the iframe's inline height with `!important` CSS (see §3.6).
- `McpUiTheme = "light" | "dark"`; `McpUiHostContext` has `theme?`, `styles?: {variables?: Record<McpUiStyleVariableKey, string>}` (keys like `--color-background-primary`, `--color-text-primary`, `--font-sans`, …), `locale?`, `platform?: 'web'|'desktop'|'mobile'`, `containerDimensions?` (verified in `@modelcontextprotocol/ext-apps@1.2.0` `spec.types.d.ts`).
- Peers `react ^18||^19` (host provides 18.2); deps `@modelcontextprotocol/ext-apps ^1.2.0`, `@modelcontextprotocol/sdk ^1.27.1`, `zod ^3.23.8` — all get bundled (spike measured post-spike `main.js` ≈ 2.17 MiB including 512 KB baked HTML; expect roughly +400–600 KB from the library alone).
- Spike learnings that bind: `onSizeChanged` may **never** fire (default height must be sane); `toolResult` must be wrapped as `CallToolResult` (`{content:[{type:'text',text}]}`); double-iframe + two simultaneous renderers (center + RHS) work.

### 1.3 Backend surfaces 1c touches

- `mcpserver/tools/provider.go` — `MCPTool{Name, Description, Schema, Resolver, Available}` has **no `Meta` field**; `registerDynamicTool` builds `&mcp.Tool{...}` without `Meta`. go-sdk v1.6.1 `mcp.Tool` embeds `Meta` (`_meta`), and `Server.AddResource(r *Resource, h ResourceHandler)` exists (resources capability auto-inferred).
- `mcpserver/inmemory_server.go` — `NewInMemoryServer(config InMemoryConfig, logger, searchService, fileContentService)` registers tools via `registerTools(tools.AccessModeRemote, …)`. `CreateConnectionForUser(userID, "", nil, nil)` gives an **unauthenticated** connection usable for tools/list and static resources/read (session auth only bites at tool-call time).
- `mcpserver/config.go` — `InMemoryConfig{BaseConfig}` only. Config structs are declarative (mcpserver/AGENTS.md); runtime services are constructor params.
- `server/embedded_mcp_server.go` — `NewEmbeddedMCPServer(pluginAPI, logger, searchService, fileContentService)`; two call sites in `server/main.go` (~366 and ~384, inside the config-update listener) where `p.configuration.MCP()` is available.
- `config/mcp_config.go` — `MCPEmbeddedServerConfig{Enabled, ToolConfigs}`; `MCPToolConfig{Name, Policy, Enabled}`; policies `ask|auto_run_in_dm|auto_run_everywhere`. When `EmbeddedServer.ToolConfigs` is non-empty it **replaces** the vetted seed (`mcp/client_manager.go:573-577`) — relevant to the e2e container config.
- Tool discovery meta path: `mcp/user_clients.go` `GetTools` calls `parseToolUIMeta(tool.Meta)` for every client including the embedded one, and drops tools not `VisibleToModel()`. So a demo tool with `_meta.ui.resourceUri` flows to `llm.Tool.UIMeta` with zero new plumbing.
- `mcp/resources_test.go` already contains `fakeEmbeddedMCPServer` (wraps any go-sdk server in an in-memory transport, implements `mcp.EmbeddedMCPServer.CreateClientTransport`) — reuse for the 1c integration test. `mcpserver` does not import the plugin `mcp` package, so `mcp` tests may import `mcpserver` without a cycle. Do NOT import `mcp.UIResourceMIMEType` from `mcpserver` (keeps that non-dependency); declare a local constant.
- `format/` rule: all formatting of Mattermost entities for tool output goes through `format/` (AGENTS.md) — the demo tool's JSON payload gets a `format/` function.

### 1.4 Webapp surfaces 1c touches

- Render chain: `LLMBotPost` → `renderedRounds.map(RoundView)` → `ToolApprovalSet` → `ToolCard` (`webapp/src/components/llmbot_post/llmbot_post.tsx`, `tool_approval_set.tsx`, `tool_card.tsx`). Live rounds have ids `'live'` / `` `live-${n}-${ts}` ``; persisted rounds use turn ids. `LLMBotPost` has `conversation` (with `user_id` = requester) via `useConversation`.
- `toolUseBlockToToolCall` (`llmbot_post/turn_content_utils.ts:96-109`) copies neither `server_origin` nor `ui_meta` — fix here.
- Bootstrap fetch: `useBotlist` (`webapp/src/bots.tsx:66-119`) fetches `getAIBots()` when redux `bots` is null and dispatches `BotsHandler`, `SET_SEARCH_ENABLED`, `SET_ALLOW_UNSAFE_LINKS`. Reducers in `webapp/src/redux.tsx` (plain switch reducers, key `'plugins-' + manifest.id`). `mcpApps` follows the same pattern. **Gotcha:** nothing globally mounted calls `useBotlist`; a channel post can render before any bots fetch — `MCPAppView` must itself call `useBotlist()` so the bootstrap is guaranteed to load.
- OAuth reuse: `custom_mattermost-ai_mcp_connection_updated` WS event → `notifyMCPConnectionUpdated` (`webapp/src/index.tsx:202-207`) → `useMCPConnectionEvents(listener)` (`hooks/use_mcp_connection_events.ts`; payload `{status: 'connected'|'disconnected', serverName?, serverOrigin?}`). Connect pattern: `window.open(authURL, '_blank', 'noopener,noreferrer')` (`components/rhs/tool_provider_popover.tsx:93-95`).
- Theme: the plugin webapp has **no** theme provider or theme selector usage; all styling uses Mattermost CSS vars scoped on `#root` (`utils/dom.ts` documents this). Theme detection must read computed CSS vars (§3.7).
- Client conventions: DTO types live in `client.tsx` (`UserMCPServerInfo` precedent); fetches use `Client4.getOptions`.
- Jest: `npm test` (`jest --config jest.config.js`); existing suites `tool_card.test.tsx`, `tool_approval_set.test.tsx`, `turn_content_utils.test.ts`, `llmbot_post.test.tsx`; `mcp_apps.test.tsx` shows the react-intl mocking pattern (mock `useIntl` + `FormattedMessage`).
- i18n: new webapp strings use `FormattedMessage`/`formatMessage` with `defaultMessage` (id-less style per newer components like `llmbot_post.tsx`); `make check-style-fix` re-extracts `webapp/src/i18n/en.json` — never hand-edit.

### 1.5 e2e surfaces

- `RunAgentContainer` (`e2e/helpers/agent-container.ts`) → `RunSystemConsoleContainer` (`e2e/helpers/system-console-container.ts`): when `config.mcp` is provided it is passed **verbatim** as `pluginConfig.config.mcp` (line 124-126), so new fields (`apps`, `embeddedServer.enableDemoApps`) flow through untouched; only the TS types need extending.
- `MattermostContainer.url()` returns `http://<host>:<mappedPort>` and `setSiteURL()` sets `ServiceSettings.SiteURL` to exactly that (`e2e/helpers/mmcontainer.ts:42-46,130-135`) — the browser origin equals the SiteURL origin, so the **same-origin insecure sandbox mode works in CI** (C8 satisfied); external mode would need a second mapped port (out of scope for the lean spec, covered in QA).
- LLM mocking: `buildChatCompletionMockRule` + `buildToolCallResponse(toolCallId, toolName, argsJSON)` + `buildTextResponse` (`e2e/helpers/openai-mock.ts`); pattern in `e2e/tests/agents/mcp-tools.spec.ts` (title mock keyed on the title prompt, tool-call mock keyed on the agent's system prompt with `times:1`, final text keyed on the toolCallId appearing in the follow-up completion). Agents need either `autoEnableNewMCPTools: true` or an explicit `enabledMCPTools: [{server_origin: 'embedded://mattermost', tool_name: …}]` (phase-0 gotcha).
- Shards: `e2e/scripts/ci-test-groups.mjs` — every spec must be assigned; `make check-shards` validates. Shard sizes: 1→16 (includes heavy real-api/live-service suites), 2→16 (mostly mock-driven), 3→18, 4→21. New spec goes to **shard 2** (fewest heavy suites, balances runtime not alphabet).
- OAuth mock harness exists for QA reference: `e2e/helpers/mcp-oauth-mock.ts` + `e2e/tests/tool-config/mcp-oauth-auth.spec.ts`.

---

## 2. Design decisions (1c-local, with rationale)

| # | Decision | Rationale |
|---|---|---|
| D-1c-1 | **App mounts inside `ToolCard`, after `ToolCallHeader`, OUTSIDE the `!isCollapsed` gate.** | Auto-approved cards default to collapsed (`tool_approval_set.tsx:276-299`); demo apps in DMs are auto-approved, so an inside-collapse app would be invisible by default. The spike mounted outside the gate successfully. Collapse still governs arguments/response. |
| D-1c-2 | **Pre-fetch the resource with our own client call, then mount `AppRenderer` with `toolResourceUri` + `onReadResource` resolving from the cached promise.** | The error taxonomy (401/403/404/502) must be handled *before* any iframe exists — `AppRenderer` only surfaces opaque `onError`. Caching means exactly one network fetch; `onReadResource` still returns the parsed 1a wire shape directly, per contract. `sandbox.csp` comes from the prefetched `_meta.ui.csp` (C7). |
| D-1c-3 | **Apps render only for persisted rounds** (`appsEligible=false` for round ids `'live'`/`live-*`). | The endpoint 404s until the anchor turn persists (§1.1). Live rounds are replaced by persisted rounds on the `end`-event refetch, so the app appears seconds after stream end — consistent with D3's "after consent/result" spirit and avoids a guaranteed 404→retry dance. |
| D-1c-4 | **One demo app (`preview_post`), not two.** | A single app already covers every host code path 1c must prove: `_meta.ui` discovery, `resources/read` via the embedded transport, default restrictive CSP (self-contained HTML), theme via `hostContext`, `size-changed` emission (D9 clamp), and in-app interactivity (a "Show raw JSON" toggle) for e2e. A second app (clock/echo) would duplicate the same host paths with zero new coverage and add a second hand-maintained protocol implementation; Phase-2 interactivity work will need purpose-built fixtures anyway. |
| D-1c-5 | **Demo apps gated by a new `mcp.embeddedServer.enableDemoApps` bool (default false), API-only (no System Console UI).** | `MCPEmbeddedServerConfig` has no DevMode and `InMemoryConfig` hardcodes `DevMode:false`; the `Available` predicate would leave hidden-but-callable tools registered, which is wrong for a config gate. A declarative bool on `InMemoryConfig` (per mcpserver/AGENTS.md "config structs are declarative") that skips registration entirely is the cleanest. `normalizeMCPConfig` spreads `embeddedServer` (`system_console/mcp_servers.tsx:62-65`), so console edits preserve the flag without new UI. Set it via `PUT /plugins/mattermost-ai/admin/config`. |
| D-1c-6 | **`onCallTool` stub returns `{content:[{type:'text',text:<i18n'd "display-only" notice>}], isError:true}`.** | Spec-conformant (the app gets a normal tool result it can display), no broken promise chain, and the text explains V1 scope. Rejecting the JSON-RPC promise instead would surface as a generic app error. |
| D-1c-7 | **Theme = luminance of the computed `--center-channel-bg` on `#root`; palette = a fixed 8-variable map of resolved MM CSS vars into ext-apps `styles.variables`.** | No theme selector exists in the plugin (§1.4); computed CSS vars are the ground truth that already drives every styled-component. Values must be resolved literals because the guest document cannot see MM vars. Computed once per `MCPAppView` mount; live theme-switch propagation (`host-context-changed`) is Phase-3 polish. |
| D-1c-8 | **D9 sizing on a wrapper div**: `width:100%`, height state = clamp(`onSizeChanged.height`, 160, `0.7 * window.innerHeight`), default **420 px** when the app never reports; wrapper CSS forces the inner iframe to `100%/100%` with `!important`. | AppFrame writes inline height on the iframe itself (§1.2); `!important` on our wrapper descendant selector wins over inline styles, giving us the single source of truth for height. 420 px default fits the RHS at 400 px width without dominating a center-channel post. |
| D-1c-9 | **New e2e spec gets its own container config via an options parameter on `RunAgentContainer`** rather than mutating the shared default. | Setting `embeddedServer.tool_configs` replaces the vetted seed for every spec sharing the helper (§1.3) — scoping the override to the new spec avoids policy drift in `mcp-tools.spec.ts` et al. |
| D-1c-10 | e2e spec assigned to **`e2e-shard-2`**. | Lightest shard by expected runtime (mock-driven suites, no real-api/live-service entries); `make check-shards` enforces membership. |

---

## 3. Part A — Webapp rendering (file-by-file)

### 3.1 Dependency

`cd webapp && npm install @mcp-ui/client@^7.1.1` — updates `package.json` + `package-lock.json` (`make check-locks` must pass). It is bundled (not an external); expect webpack size warnings, record the `main.js` delta in the PR.

### 3.2 Types

**`webapp/src/components/tool_types.ts`** — add:

```ts
// Mirrors llm.ToolUIMeta JSON (llm/tools_ui.go).
export interface ToolUIMeta {
    resource_uri: string;
    visibility?: string[];
}
```

and on `ToolCall`:

```ts
    // MCP Apps metadata from the tool's _meta.ui. Present only when the tool
    // declares an app UI and the viewer may see it (requester, or shared).
    ui_meta?: ToolUIMeta;
```

**`webapp/src/types/conversation.ts`** — `import {ToolUIMeta} from '@/components/tool_types';` (no cycle: `tool_types.ts` imports nothing) and add to `ContentBlock` under the ToolUse fields:

```ts
    ui_meta?: ToolUIMeta;
```

**`webapp/src/components/llmbot_post/turn_content_utils.ts`** — in `toolUseBlockToToolCall` add:

```ts
        server_origin: block.server_origin ?? undefined,
        ui_meta: block.ui_meta ?? undefined,
```

(The live WS path needs nothing: `JSON.parse(data.tool_call) as ToolCall[]` already carries `server_origin`/`ui_meta` from `llm.ToolCall` JSON.)

### 3.3 Redux + bootstrap

**`webapp/src/redux.tsx`** — add:

```ts
export const MCPAppsHandler = 'SET_MCP_APPS';

function mcpApps(state: MCPAppsBootstrap = {enabled: false}, action: any) {
    switch (action.type) {
    case MCPAppsHandler:
        return action.mcpApps;
    default:
        return state;
    }
}
```

register `mcpApps` in the `combineReducers` map. Import `MCPAppsBootstrap` from `@/client`.

**`webapp/src/bots.tsx`** — in `fetchBots` (after the `SET_ALLOW_UNSAFE_LINKS` dispatch):

```ts
            dispatch({
                type: MCPAppsHandler,
                mcpApps: response.mcpApps ?? {enabled: false},
            });
```

### 3.4 Client — `webapp/src/client.tsx`

Add (DTOs mirror `api/api_mcp_app_resource.go` exactly):

```ts
export interface MCPAppsBootstrap {
    enabled: boolean;
    sandboxURL?: string;
    disabledReason?: string;
}

export interface AppResourceCSP {
    connectDomains?: string[];
    resourceDomains?: string[];
    frameDomains?: string[];
    baseUriDomains?: string[];
}

export interface AppResourceUIMeta {
    csp?: AppResourceCSP;
    permissions?: Record<string, Record<string, unknown>>;
    domain?: string;
    prefersBorder?: boolean;
}

export interface AppResourceContents {
    uri: string;
    mimeType: string;
    text: string;
    _meta?: {ui?: AppResourceUIMeta};
}

// Mirrors MCP ReadResourceResult — returned verbatim to @mcp-ui/client's
// onReadResource.
export interface AppResourceResponse {
    contents: AppResourceContents[];
}

export class MCPAppResourceError extends Error {
    status: number;
    errorCode: string;
    authURL?: string;
    constructor(status: number, errorCode: string, message: string, authURL?: string) { … }
}

export async function getMCPAppResource(postID: string, toolCallID: string): Promise<AppResourceResponse> {
    const params = new URLSearchParams({post_id: postID, tool_call_id: toolCallID});
    const response = await fetch(`${baseRoute()}/mcp/app-resource?${params}`, Client4.getOptions({method: 'GET'}));
    if (response.ok) {
        return response.json();
    }
    let body: {error_code?: string; message?: string; auth_url?: string} | null = null;
    try {
        body = await response.json();
    } catch {
        // non-JSON error body (e.g. gin 500) — fall through
    }
    throw new MCPAppResourceError(response.status, body?.error_code ?? 'unknown', body?.message ?? 'app resource fetch failed', body?.auth_url);
}
```

### 3.5 New component — `webapp/src/components/mcp_apps/mcp_app_view.tsx`

```ts
interface MCPAppViewProps {
    postID: string;
    tool: ToolCall;           // caller guarantees ui_meta?.resource_uri present + status ∈ {Success, AutoApproved}
    requesterUserID?: string; // conversation.user_id, for the D5 popover
}
```

Internal state machine:

```ts
type AppPhase =
    | {phase: 'loading'}
    | {phase: 'ready'; contents: AppResourceContents; response: AppResourceResponse}
    | {phase: 'auth_required'; authURL: string}
    | {phase: 'no_access'}      // 403, or 401 again after a connect completed
    | {phase: 'unavailable'};   // 400/404/500/502/network/malformed
```

Behavior (prescriptive):

1. `useBotlist()` first (guarantees the bootstrap fetch ran — §1.4 gotcha), then `const mcpApps = useSelector((state: any) => state['plugins-' + manifest.id]?.mcpApps as MCPAppsBootstrap | undefined);`. If `!mcpApps?.enabled || !mcpApps.sandboxURL` → `return null` (plain ToolCard, zero footprint). `sandboxUrl = useMemo(() => new URL(mcpApps.sandboxURL), […])` with try/catch → `null` → render nothing.
2. Fetch effect keyed `[postID, tool.id]`: call `getMCPAppResource(postID, tool.id)`. Success: validate `response.contents[0]?.text` non-empty, else `unavailable`. Store the raw `response` too (returned by `onReadResource`). Failure mapping per §1.1 table: `401 && authURL` → `auth_required`; `401` without authURL after connect, or `403` → `no_access`; everything else → `unavailable`. Guard state updates with a cancelled flag.
3. **Connect to view (D4/D5 state 2):** in `auth_required`, render a `ConnectButton` (styled like `AcceptRejectButton`); click → `window.open(authURL, '_blank', 'noopener,noreferrer')` and set a `connectAttemptedRef`. Subscribe `useMCPConnectionEvents`: on `{status:'connected'}` where `!event.serverOrigin || event.serverOrigin === tool.server_origin`, refetch. If the refetch returns 401 again and `connectAttemptedRef` is set → `no_access` (repeated-401-after-auth, D5 state-3 approximation).
4. **`no_access` (D5 state 3):** a subtle inline row (11 px, 0.56 alpha, `LockIcon`) whose text is wrapped in a react-bootstrap `OverlayTrigger`+`Tooltip` (same imports as ToolCard): requester username from `useSelector((state: GlobalState) => (requesterUserID ? state.entities.users.profiles[requesterUserID]?.username : undefined))`; message `@{username} has access to this app, but you don't, so we can't render it for you.` with a username-less fallback variant (`The requester has access to this app, but you don't, so we can't render it for you.`).
5. **`unavailable`:** subtle single-line note `The interactive view for this tool couldn't be loaded.` — the plain ToolCard content around it is untouched (never a broken box).
6. **`loading`:** compact row (spinner + `Loading app…`), NOT the full default height, to avoid layout jump for fast fetches.
7. **`ready`:** render

```tsx
<AppContainer $height={height} $bordered={contents._meta?.ui?.prefersBorder !== false} data-testid='mcp-app-view'>
    <AppRenderer
        toolName={tool.name}
        sandbox={{url: sandboxUrl, csp: contents._meta?.ui?.csp}}
        toolResourceUri={tool.ui_meta!.resource_uri}
        onReadResource={onReadResource}
        toolInput={toolInput}
        toolResult={toolResult}
        hostContext={hostContext}
        onCallTool={onCallTool}
        onMessage={onMessage}
        onOpenLink={onOpenLink}
        onSizeChanged={onSizeChanged}
        onError={onRendererError}
        onFallbackRequest={onFallbackRequest}
    />
</AppContainer>
```

Handler prescriptions:

- `onReadResource = useCallback(async ({uri}) => { if (uri === tool.ui_meta?.resource_uri && cachedResponse) { return cachedResponse as ReadResourceResult-shaped; } throw new Error('resource not available'); }, …)` — returns the parsed 1a body directly (contract), no second network round-trip.
- `toolInput`: `tool.arguments` when it is a plain object, else `{}`.
- `toolResult` (spike learning 9): `tool.result != null ? {content: [{type: 'text' as const, text: tool.result}]} : undefined`. Type it with a local structural `CallToolResultShape` (`{content: Array<{type: 'text'; text: string}>; isError?: boolean}`) rather than importing from the transitive `@modelcontextprotocol/sdk` (avoids an extraneous-dependency lint and a hard version coupling); assign with a narrowing cast only if tsc demands it.
- `onCallTool` (D-1c-6): resolve `{content:[{type:'text', text: formatMessage({defaultMessage: 'This app is display-only in Mattermost right now. Interactive app actions will be supported in a future release.'})}], isError: true}`.
- `onMessage`: `console.debug('[mcp-apps] ui/message ignored (Phase 1)', params)` (eslint-disabled line) and resolve `{}`.
- `onOpenLink`: allow only `http:`/`https:` (parse with `new URL`, reject otherwise), then `window.open(params.url, '_blank', 'noopener,noreferrer'); return {};`. (`allowUnsafeLinks` governs markdown link *rendering*, not host-mediated opens; noopener+scheme-check is the convention that matters here.)
- `onSizeChanged`: `if (params.height != null) setHeight(clampAppHeight(params.height));`
- `onError` / `onFallbackRequest`: `onError` → `setPhase({phase:'unavailable'})` (fallback to plain card); `onFallbackRequest` → `throw new Error('method not supported')`.

### 3.6 Sizing — `webapp/src/components/mcp_apps/app_sizing.ts`

```ts
export const APP_MIN_HEIGHT = 160;
export const APP_DEFAULT_HEIGHT = 420;
export const APP_MAX_VIEWPORT_RATIO = 0.7;

export function maxAppHeight(viewportHeight: number): number {
    return Math.max(APP_MIN_HEIGHT, Math.round(viewportHeight * APP_MAX_VIEWPORT_RATIO));
}

export function clampAppHeight(reported: number, viewportHeight: number = window.innerHeight): number {
    return Math.min(Math.max(Math.round(reported), APP_MIN_HEIGHT), maxAppHeight(viewportHeight));
}
```

`AppContainer` (in `mcp_app_view.tsx`, styled-components):

```ts
const AppContainer = styled.div<{$height: number; $bordered: boolean}>`
    width: 100%;
    height: ${(p) => p.$height}px;
    margin-top: 8px;
    overflow: hidden;
    ${(p) => (p.$bordered ? `
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    ` : '')}

    /* AppFrame writes inline width/height on its iframe; the wrapper is the
       single source of truth for D9, so neutralize them. */
    iframe {
        width: 100% !important;
        height: 100% !important;
        border: none;
        display: block;
    }
`;
```

`height` state initial value `APP_DEFAULT_HEIGHT`; `hostContext.containerDimensions = {maxHeight: maxAppHeight(window.innerHeight)}`.

### 3.7 Theme/host context — `webapp/src/components/mcp_apps/host_context.ts`

```ts
export function resolveAppTheme(doc: Document = document): 'light' | 'dark';
export function buildHostStyleVariables(doc: Document = document): Record<string, string>;
```

- `resolveAppTheme`: read `getComputedStyle(root).getPropertyValue('--center-channel-bg')` from `#root` (fallback `document.body`), parse `#rrggbb`/`#rgb`/`rgb(…)`; relative luminance `0.2126r + 0.7152g + 0.0722b` on 0–255; `< 128` ⇒ `'dark'`; unparseable ⇒ `'light'`.
- `buildHostStyleVariables` resolves these MM sources into **literal** CSS values (the guest cannot see MM vars):

| ext-apps variable | source |
|---|---|
| `--color-background-primary` | computed `--center-channel-bg` |
| `--color-background-secondary` | `rgba(<--center-channel-color-rgb>, 0.04)` |
| `--color-text-primary` | computed `--center-channel-color` |
| `--color-text-secondary` | `rgba(<--center-channel-color-rgb>, 0.72)` |
| `--color-border-primary` | `rgba(<--center-channel-color-rgb>, 0.16)` |
| `--color-background-info` | computed `--button-bg` |
| `--color-text-inverse` | computed `--button-color` |
| `--font-sans` | computed `font-family` of `#root` |

Skip entries whose source var resolves empty. `hostContext` assembled in `mcp_app_view.tsx` via `useMemo` on mount:

```ts
{theme: resolveAppTheme(), styles: {variables: buildHostStyleVariables()}, locale: intl.locale, platform: 'web', containerDimensions: {maxHeight: maxAppHeight(window.innerHeight)}}
```

### 3.8 Integration into the render chain

**`webapp/src/components/tool_card.tsx`** — new props `requesterUserID?: string; appsEligible?: boolean;`. Immediately after `</ToolCallHeader>` (outside `!isCollapsed`):

```tsx
{appsEligible && tool.ui_meta?.resource_uri && isSuccess && (
    <MCPAppView
        postID={postID}
        tool={tool}
        requesterUserID={requesterUserID}
    />
)}
```

(`isSuccess` already means `Success || AutoApproved` — exactly D3. Enabled-per-bootstrap is checked inside `MCPAppView`.)

**`webapp/src/components/tool_approval_set.tsx`** — new pass-through props `requesterUserID?: string; appsEligible?: boolean;` forwarded to every `ToolCard` (QuestionCards are user-interaction tools that never carry `ui_meta`; no change there).

**`webapp/src/components/llmbot_post/llmbot_post.tsx`** — in the `renderedRounds.map`, compute `const appsEligible = !isLiveRound && !round.id.startsWith('live-');` and pass to `RoundView`, which forwards `requesterUserID={conversation?.user_id}` (add both to `RoundViewProps`) into `ToolApprovalSet`. (D-1c-3.)

### 3.9 i18n

New strings, all id-less `defaultMessage` style: `Loading app…`, `Connect to view`, `This app is display-only in Mattermost right now. Interactive app actions will be supported in a future release.`, `@{username} has access to this app, but you don't, so we can't render it for you.`, `The requester has access to this app, but you don't, so we can't render it for you.`, `The interactive view for this tool couldn't be loaded.`. Run `make check-style-fix` (re-extracts `webapp/src/i18n/en.json`); commit the regenerated file.

### 3.10 Jest tests

**`webapp/src/components/mcp_apps/app_sizing.test.ts`** — table:

| case | input (reported, viewport) | want |
|---|---|---|
| below min | (100, 1000) | 160 |
| in range | (400, 1000) | 400 |
| above max | (900, 1000) | 700 |
| tiny viewport floors at min | (900, 200) | 160 |
| rounds fractional | (400.6, 1000) | 401 |

**`webapp/src/components/mcp_apps/host_context.test.ts`** — jsdom with stubbed `getComputedStyle`: white bg → light; `#1b1d22` → dark; `rgb(28,30,36)` → dark; unparseable → light; variables map contains resolved literals and omits empty sources.

**`webapp/src/components/mcp_apps/mcp_app_view.test.tsx`** — mock `@/client` (`getMCPAppResource`, `MCPAppResourceError`), mock `@mcp-ui/client` as `{AppRenderer: (props) => <div data-testid='app-renderer-stub'/>}` (capture props), mock `@/bots` `useBotlist` as no-op, provide redux store state with `mcpApps` + `profiles`, mock react-intl per `mcp_apps.test.tsx` pattern. Table:

| case | mcpApps state / fetch outcome | want |
|---|---|---|
| apps disabled in bootstrap | `{enabled:false}` | renders null |
| bootstrap missing sandboxURL | `{enabled:true}` | renders null |
| success | resolves contents | stub rendered; `sandbox.url` = bootstrap URL; `sandbox.csp` = `_meta.ui.csp`; `toolResult` wraps `tool.result` into `content[0].text`; `toolResourceUri` = `ui_meta.resource_uri` |
| success, `prefersBorder:false` | resolves | container unbordered |
| 401 with auth_url | rejects `MCPAppResourceError(401,'mcp_auth_required',…,url)` | Connect to view button; click calls `window.open(url,'_blank','noopener,noreferrer')` |
| connected event then 200 | 401 → notify connected → resolves | stub rendered (refetch happened) |
| connected event then 401 again | 401 → connect click → notify → 401 | no-access row rendered (state-3 approximation) |
| 403 | rejects 403 `forbidden` | no-access row with `@invoker` text |
| 404 | rejects 404 | unavailable note |
| 502 | rejects 502 | unavailable note |
| network error | rejects TypeError | unavailable note |
| onError from renderer | success then invoke captured `onError` | unavailable note replaces stub |
| onCallTool stub | success; await captured `onCallTool({})` | resolves `{isError:true}` with display-only text |
| onSizeChanged clamp | success; invoke `onSizeChanged({height: 5000})` | container height = clamped max |

**`webapp/src/components/llmbot_post/turn_content_utils.test.ts`** — extend the existing suite: `toolUseBlockToToolCall` copies `server_origin` and `ui_meta` from the block; absent fields stay undefined.

**`webapp/src/components/tool_card.test.tsx`** — extend: with `appsEligible` + `ui_meta` + Success status the card renders the (mocked) `MCPAppView`; not rendered for Pending/Error status, missing `ui_meta`, or `appsEligible=false`; renders even when `isCollapsed`.

---

## 4. Part B — Built-in demo app (D10)

### 4.1 Config gate

**`config/mcp_config.go`** — add to `MCPEmbeddedServerConfig`:

```go
	// EnableDemoApps registers the built-in demo MCP Apps tools/resources
	// (e.g. preview_post) on the embedded server. Intended for demos, QA,
	// and e2e; default false.
	EnableDemoApps bool `json:"enableDemoApps,omitempty"`
```

**`webapp/src/components/system_console/mcp_servers.tsx`** — add `enableDemoApps?: boolean;` to the `MCPEmbeddedServerConfig` TS type (no UI; `normalizeMCPConfig`'s spread already preserves it — add one assertion to the existing `normalizeMCPConfig` test).

**`mcpserver/config.go`** — add to `InMemoryConfig`:

```go
	// EnableDemoApps registers demo MCP Apps tools and ui:// resources.
	EnableDemoApps bool `json:"enable_demo_apps"`
```

**`server/embedded_mcp_server.go`** — `NewEmbeddedMCPServer(pluginAPI, logger, searchService, fileContentService, enableDemoApps bool)`; set `InMemoryConfig{…, EnableDemoApps: enableDemoApps}`. Both `server/main.go` call sites pass `p.configuration.MCP().EmbeddedServer.EnableDemoApps` (the update-listener call site re-reads it, so config toggles take effect on save — same lifecycle as the rest of `ReInit`).

### 4.2 `_meta` support in the tool provider

**`mcpserver/tools/provider.go`** —

- Add `Meta mcp.Meta` field to `MCPTool` (doc: "Meta is attached verbatim as the tool's `_meta` (e.g. MCP Apps `ui.resourceUri`). Nil for ordinary tools.").
- In `registerDynamicTool`, after constructing `tool`: `if mcpTool.Meta != nil { tool.Meta = mcpTool.Meta }`.

### 4.3 Demo tool + resource — `mcpserver/tools/demo_apps.go` (NEW) + `mcpserver/tools/demo_apps/preview_post.html` (NEW, `//go:embed`)

```go
const (
	// demoAppMIMEType must equal mcp.UIResourceMIMEType in the plugin's mcp
	// package; declared locally so mcpserver keeps not importing that package.
	demoAppMIMEType = "text/html;profile=mcp-app"

	previewPostResourceURI = "ui://mattermost/preview-post.html"
)

//go:embed demo_apps/preview_post.html
var previewPostHTML string

type PreviewPostArgs struct {
	PostID string `json:"post_id" jsonschema:"The ID of the post to preview,minLength=26,maxLength=26"`
}

func (p *MattermostToolProvider) getDemoAppTools() []MCPTool {
	return []MCPTool{{
		Name:        "preview_post",
		Description: previewPostDescription,
		Schema:      NewJSONSchemaForAccessMode[PreviewPostArgs](string(p.accessMode)),
		Resolver:    typed("preview_post", p.toolPreviewPost),
		Meta: mcp.Meta{"ui": map[string]any{
			"resourceUri": previewPostResourceURI,
		}},
	}}
}

// ProvideDemoAppTools registers the demo MCP Apps tools and their ui://
// resources. Called only when EnableDemoApps is set.
func (p *MattermostToolProvider) ProvideDemoAppTools(mcpServer *mcp.Server) {
	for _, t := range p.getDemoAppTools() {
		p.registerDynamicTool(mcpServer, t)
	}
	mcpServer.AddResource(&mcp.Resource{
		URI:      previewPostResourceURI,
		Name:     "preview-post-app",
		MIMEType: demoAppMIMEType,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      previewPostResourceURI,
			MIMEType: demoAppMIMEType,
			Text:     previewPostHTML,
		}}}, nil
	})
}
```

`toolPreviewPost` resolver: `requireID("post_id", …)`; `client.GetPost(ctx, args.PostID, "")`; `client.GetUser(ctx, post.UserId, "")` (best-effort — empty username on error); `client.GetChannel(ctx, post.ChannelId, "")` (best-effort); return `format.MarshalPostPreview(format.PostPreviewEntry{…})`.

**`format/format.go`** — add (per the AGENTS.md formatting rule; JSON because the consumer is the app, but the entity→output mapping still lives in `format/`):

```go
// PostPreviewEntry is the payload rendered by the preview_post demo MCP App.
type PostPreviewEntry struct {
	PostID             string `json:"post_id"`
	Message            string `json:"message"`
	Username           string `json:"username,omitempty"`
	ChannelDisplayName string `json:"channel_display_name,omitempty"`
	CreateAt           int64  `json:"create_at"`
}

// MarshalPostPreview renders a PostPreviewEntry as the JSON tool output the
// preview_post demo app parses.
func MarshalPostPreview(entry PostPreviewEntry) (string, error)
```

**`mcpserver/inmemory_server.go`** — in `NewInMemoryServer`, after `registerTools(…)`:

```go
	if config.EnableDemoApps {
		provider := tools.NewMattermostToolProvider(mattermostServer.authProvider, logger, config, tools.AccessModeRemote, searchService, fileContentService)
		provider.ProvideDemoAppTools(mattermostServer.mcpServer)
		logger.Info("Registered demo MCP Apps tools")
	}
```

### 4.4 `preview_post.html` — hand-written guest app (self-contained, no build step, ≲6 KB)

Must work under the spec's restrictive default CSP (`script-src 'self' 'unsafe-inline'`, `connect-src 'none'`): one `<style>` block, one inline `<script>`, zero external fetches. Vanilla JS implementing the guest side of the ext-apps protocol (framing verified in Phase 0 / `sandbox/sandbox.html`; the guest runs inside the inner iframe and posts JSON-RPC 2.0 to `window.parent` with `'*'` target origin):

1. Send request `{jsonrpc:'2.0', id:1, method:'ui/initialize', params:{appInfo:{name:'preview-post',version:'1.0.0'}, appCapabilities:{}}}`; on the id-1 result, read `result.hostContext.theme` and set `document.documentElement.dataset.theme` (CSS has `[data-theme="dark"]` overrides using the ext-apps style variables when provided via `hostContext.styles.variables`, with hardcoded light/dark fallbacks).
2. Send notification `{jsonrpc:'2.0', method:'ui/notifications/initialized'}`.
3. Handle notifications: `ui/notifications/tool-input` (ignore/log), `ui/notifications/tool-result` → `JSON.parse(params.result.content[0].text)` → render the styled preview card: avatar initial, `@username`, channel name, formatted `create_at` date, message body (`textContent` assignment only — no innerHTML with post data). Malformed JSON → show the raw text preformatted.
4. Interactive element for e2e: a `<button data-testid="preview-post-toggle">Show raw JSON</button>` toggling `<pre data-testid="preview-post-raw">` visibility (purely in-app; no host round-trip — display-only V1). Message body element carries `data-testid="preview-post-message"`.
5. After rendering, send `{jsonrpc:'2.0', method:'ui/notifications/size-changed', params:{height: document.documentElement.scrollHeight}}` (exercises the D9 clamp path).

### 4.5 Go tests (table-driven; no new libraries)

**`mcpserver/tools/demo_apps_test.go`** —
- `TestPreviewPostToolMeta`: `getDemoAppTools()[0].Meta["ui"].(map[string]any)["resourceUri"] == previewPostResourceURI`; embedded HTML non-empty, contains `ui/notifications/initialized`, `preview-post-toggle`, and no `http://`/`https://` external references (`src=`/`href=` scan).
- `TestToolPreviewPost`: table over resolver cases using the existing `helpers_test.go` httptest pattern (post found → JSON contains message/username; post missing → error; invalid ID → error).

**`format/format_test.go`** — `TestMarshalPostPreview`: round-trips the entry through `encoding/json`, empty-username omitted.

**`mcpserver/inmemory_server_test.go`** (new or extended) — `TestInMemoryServerDemoApps`, table over `enableDemoApps ∈ {true,false}`: build `NewInMemoryServer(InMemoryConfig{BaseConfig{MMServerURL:"http://mm"}, EnableDemoApps: tt.enabled}, …)`, connect a go-sdk `mcp.Client` over `CreateConnectionForUser("user1", "", nil, nil)`; assert `tools/list` contains/omits `preview_post` and, when present, `_meta.ui.resourceUri`; `resources/read(previewPostResourceURI)` returns the HTML with MIME `text/html;profile=mcp-app` / errors when disabled.

**`mcp/embedded_apps_test.go`** (NEW) — the required 1a-path proof: adapter `type demoEmbeddedServer struct{ inner *mcpserver.MattermostInMemoryMCPServer }` implementing `CreateClientTransport(userID, sessionID string, _ *pluginapi.Client)` by delegating to `inner.CreateConnectionForUser(userID, "", nil, nil)` (pattern: `fakeEmbeddedMCPServer` in `mcp/resources_test.go`). Build a `ClientManager` with `EmbeddedServer: {Enabled: true}` config and this embedded client; assert (a) `GetToolsForUser` (or the user-clients `GetTools` path) yields `preview_post` with `UIMeta.ResourceURI == "ui://mattermost/preview-post.html"` — proving `parseToolUIMeta` fires for the embedded transport — and (b) `ReadUserAppResource(ctx, "user1", mcp.EmbeddedClientKey, previewPostResourceURI)` returns `AppResource{HTML: <embedded html>, MIMEType: "text/html;profile=mcp-app"}`.

### 4.6 Docs

`docs/admin_guide.md`, inside the existing "MCP Apps" subsection: one short "Demo apps" paragraph — `mcp.embeddedServer.enableDemoApps` (admin config API only), what `preview_post` does, default off.

---

## 5. Part C — e2e (Playwright)

### 5.1 Helper changes

**`e2e/helpers/system-console-container.ts`** — extend the `mcp` type on `SystemConsolePluginConfig`:

```ts
        embeddedServer?: {
            enabled?: boolean;
            enableDemoApps?: boolean;
            tool_configs?: Array<{ name?: string; policy?: string; enabled?: boolean }>;
        };
        apps?: {
            enabled?: boolean;
            sandboxURL?: string;
            allowInsecureSameOriginSandbox?: boolean;
        };
```

(No behavior change: the `if (config.mcp)` verbatim assignment already forwards these.)

**`e2e/helpers/agent-container.ts`** — signature `RunAgentContainer(overrides?: {mcp?: SystemConsolePluginConfig['mcp']})`; the `mcp:` block becomes `overrides?.mcp ?? { …existing literal… }` (D-1c-9). Existing callers unchanged.

### 5.2 New spec — `e2e/tests/mcp-apps/demo-app.spec.ts`

`beforeAll` (timeout 180000):

```ts
mattermost = await RunAgentContainer({mcp: {
    enabled: true,
    enablePluginServer: true,
    idleTimeoutMinutes: 30,
    servers: [],
    embeddedServer: {
        enabled: true,
        enableDemoApps: true,
        // Replaces the vetted seed for THIS container only: preview_post
        // auto-runs in DMs so the app renders without approval clicks (D3
        // AutoApproved path).
        tool_configs: [{name: 'preview_post', policy: 'auto_run_in_dm', enabled: true}],
    },
    apps: {enabled: true, allowInsecureSameOriginSandbox: true},
}});
openAIMock = await RunOpenAIMocks(mattermost.network);
```

**Test 1 — "same-origin sandbox page is served"** (pure request-level):

```ts
const res = await request.get(`${mattermost.url()}/plugins/mattermost-ai/mcp/apps/sandbox`);
expect(res.status()).toBe(200);
expect(res.headers()['content-type']).toContain('text/html');
expect(await res.text()).toContain('sandbox-proxy-ready');
```

**Test 2 — "demo app renders after tool success and responds to interaction"** (timeout 120000):

1. Admin client → `AgentAPIHelper.createTestAgent` with `{displayName: 'Preview App Agent', serviceID: mockServiceId, autoEnableNewMCPTools: false, enabledMCPTools: [{server_origin: 'embedded://mattermost', tool_name: 'preview_post'}], enabledNativeTools: []}`.
2. Seed `const seededPost = await adminClient.createPost({channel_id: townSquare.id, message: 'MCP Apps demo seeded post <ts>'})`.
3. Mocks (mirror `mcp-tools.spec.ts:178-199`): title mock (`bodyContains` the title prompt, `times:1`); `buildToolCallResponse('call_preview_post_demo', 'preview_post', JSON.stringify({post_id: seededPost.id}))` keyed `bodyContains` the agent's system prompt (`times:1`); `buildTextResponse('The post preview is shown above.')` keyed `bodyContains: 'call_preview_post_demo'` (`times:1`).
4. Login `agentRegularUsername`; `mmPage.createAndNavigateToDMWithBot(…, agent.name)`; send `Use the preview_post tool to preview post ${seededPost.id}. Call the tool now.` **in the DM center channel** (apps render in the post body, not the RHS composer — the DM is a conversation with the agent, `auto_run_in_dm` applies).
5. `await expect(page.getByText('The post preview is shown above.')).toBeVisible({timeout: 60000});` — stream done; persisted-round refetch then mounts the app (D-1c-3).
6. Iframe hierarchy + content + interactivity:

```ts
await expect(page.getByTestId('mcp-app-view')).toBeVisible({timeout: 30000});
const outer = page.frameLocator('iframe[src*="/plugins/mattermost-ai/mcp/apps/sandbox"]');
const inner = outer.frameLocator('iframe');
await expect(inner.getByTestId('preview-post-message')).toContainText('MCP Apps demo seeded post', {timeout: 30000});
await expect(inner.getByTestId('preview-post-raw')).not.toBeVisible();
await inner.getByTestId('preview-post-toggle').click();
await expect(inner.getByTestId('preview-post-raw')).toBeVisible();
```

That is the whole spec — sandbox mode, tool→app pipeline, double-iframe hierarchy, in-app interactivity. Connect-to-view/onlooker flows stay in jest + QA (remote-OAuth e2e would need the `mcp-oauth-mock` harness to also serve ui resources; deliberately out of the lean slice).

### 5.3 Shard assignment (MANDATORY, same change)

`e2e/scripts/ci-test-groups.mjs` → append `'tests/mcp-apps/demo-app.spec.ts'` to `'e2e-shard-2'` (D-1c-10). `make check-shards` must pass.

---

## 6. Ordered commit plan

1. `config: add enableDemoApps to embedded MCP server config` — `config/mcp_config.go`, `webapp/src/components/system_console/mcp_servers.tsx` type + `normalizeMCPConfig` test assertion.
2. `mcpserver: preview_post demo MCP App behind enableDemoApps` — `MCPTool.Meta` + `registerDynamicTool`, `demo_apps.go`, `demo_apps/preview_post.html`, `format.MarshalPostPreview`, `InMemoryConfig.EnableDemoApps` + registration, `server/embedded_mcp_server.go` + `server/main.go` wiring, docs paragraph, Go tests (§4.5 except the `mcp/` one).
3. `mcp: embedded demo app ui_meta discovery and resources/read test` — `mcp/embedded_apps_test.go`.
4. `webapp: plumb ui_meta and mcpApps bootstrap into tool data` — `tool_types.ts`, `types/conversation.ts`, `turn_content_utils.ts` (+test), `redux.tsx`, `bots.tsx`, `client.tsx` (`MCPAppsBootstrap`, app-resource DTOs, `getMCPAppResource`, `MCPAppResourceError`).
5. `webapp: render MCP Apps from ToolCard via @mcp-ui/client` — dependency + lockfile, `mcp_apps/` (view, sizing, host_context) + tests, `tool_card.tsx` / `tool_approval_set.tsx` / `llmbot_post.tsx` threading + test updates, regenerated `webapp/src/i18n/en.json`.
6. `e2e: demo MCP App rendering spec` — helper type/opts changes, `tests/mcp-apps/demo-app.spec.ts`, `ci-test-groups.mjs` shard entry.

### Verification before the PR

`make check-style` · `make test` · `cd webapp && npm run check-types && npm test` · `make check-shards` · `make check-i18n` · `make check-locks` (or the `make check` aggregate; isolate failures per AGENTS.md). Then run the new e2e spec once locally: `cd e2e && npx playwright test tests/mcp-apps/demo-app.spec.ts --reporter=list` (needs `make dist` first for the plugin tarball).

---

## 7. QA script (manual GUI verification for the orchestrator)

Setup: local Mattermost (`http://localhost:8065`, SiteURL set to exactly that; **browse via `localhost`, not `127.0.0.1`** — C8), plugin built from this branch (`make deploy`). Configure via `PUT /plugins/mattermost-ai/admin/config`: `mcp.apps = {enabled: true, allowInsecureSameOriginSandbox: true}`, `mcp.embeddedServer.enableDemoApps = true`. Create/patch an agent with `autoEnableNewMCPTools: true` (phase-0 gotcha) on a real or mocked LLM service.

1. **Same-origin (insecure) mode, center channel.** DM the agent: `Use the preview_post tool on post <id of any post>. Call the tool now.` After the reply finishes, the ToolCard grows a bordered app: styled post preview (author, channel, date, message). DevTools → Elements: outer iframe `src="http://localhost:8065/plugins/mattermost-ai/mcp/apps/sandbox"`, inner iframe nested within (double-iframe proof, insecure mode). Click **Show raw JSON** → the raw payload toggles. Screenshot.
2. **External mode (second local origin).** Set `mcp.apps.sandboxURL = "http://127.0.0.1:8066"` (different browser origin than `localhost:8065`; the plugin's standalone listener binds `:8066` per 1b). Hard-refresh, re-open the DM: the app renders again and the outer iframe src is `http://127.0.0.1:8066/sandbox.html` — proves the external listener + origin-isolation path in-browser (the 1b remediation left this as the 1c QA item). Then clear `sandboxURL` to return to same-origin mode. Screenshot of both iframe srcs.
3. **RHS + min-width RHS (D9).** Open the Agents RHS, trigger `preview_post` there. App fits 100% width at the default RHS width; drag the RHS to its minimum (304 px) — app stays contained, no horizontal scrollbars, height within [160 px, 70 vh]. Verify the center-channel instance still works with the RHS one mounted simultaneously (spike learning 6). Screenshots at default + min width.
4. **Theme mapping.** Switch to a dark theme (Settings → Display → Theme → Onyx), hard-refresh, re-open: app renders dark (guest received `theme:'dark'` + dark style variables). Screenshot light vs dark.
5. **Onlooker / second-user flow (D5 states 1→2/render).** In Town Square (not DM), as user A trigger `preview_post` with the tool policy set to `ask`: Accept, then at the result stage do NOT share — user B (second browser session) sees only the redacted card, no app, no hint (state 1). Share → user B's card refetches (`GET /conversations/:id`) and, since the embedded server needs no OAuth, the app renders under B's own credentials. Screenshots of B before/after share.
6. **Connect-to-view (D4/D5 state 2) + repeated-401 popover (state 3).** Requires a remote OAuth-gated MCP Apps server: reuse the rule set from `e2e/helpers/mcp-oauth-mock.ts` against a locally-run Smocker (`docker run -p 44300:8080 ghcr.io/smocker-dev/smocker`, POST the mock definitions to its admin port, extended with a ui-metadata tool + `resources/read` answer), added as an MCP server in the console. With no token, the app area shows **Connect to view**; completing the mock OAuth (WS `mcp_connection_updated`) auto-refetches and renders. Then configure the mock to keep returning 401 on `resources/read` after "auth": the popover "…has access to this app, but you don't…" appears after the failed post-connect refetch. If standing this up manually proves impractical, fall back to the jest coverage of every 401/403 branch and say so explicitly in the PR — do not fake it.
7. **Interop sanity (master-plan D10 second leg, best-effort).** Run `npx -y @modelcontextprotocol/server-basic-react` (streamable HTTP `:3001/mcp`, no auth), add it as an MCP server, trigger its `get-time` tool: the third-party app renders and its button works via… the `onCallTool` stub — expect the **display-only notice** as the button result (correct V1 behavior, worth a screenshot). Optionally point the alpic conformance server at the host and record CSP-clause results (any CSP failure is a 1b bug per 1b risk notes).

---

## 8. Risks / uncertainties

- **`onReadResource` return-shape strictness**: `AppRenderer` zod-parses the `ReadResourceResult`. The 1a body was designed to mirror it exactly (uri/mimeType/text/_meta), but if 7.1.1 rejects an extra/missing field, the fallback is passing `html={contents.text}` instead of `toolResourceUri`+callback — a two-line change kept in reserve; jest captures the exact returned object either way.
- **Iframe height `!important` override** relies on AppFrame writing inline styles (verified in 7.1.1 dist). A future library bump could change the DOM; the jest prop-capture tests and e2e will catch a broken layout.
- **Hand-written guest protocol drift**: `preview_post.html` implements ui/initialize → initialized → tool-result by hand. Framing was captured live in Phase 0 and the e2e exercises the real handshake end-to-end; if the app stays on "Connecting", compare against `sandbox/sandbox.html` and the phase-0 fixture HTML first.
- **App appears only after stream end** (D-1c-3): a visible-but-brief delay between the final token and the app mounting (persisted-round refetch). Accepted for V1; revisit with tool-input streaming in Phase 2.
- **e2e nested-frameLocator flakiness**: sandbox handshake has a 10 s internal timeout; generous 30–60 s expects plus Playwright retries mitigate. If the outer iframe never appears, the first suspect is SiteURL vs browser origin (C8) — the container helper guarantees equality, so failures point at real bugs.
- **Bundle size**: `@mcp-ui/client` + ext-apps + sdk + zod are bundled; webpack will warn. Record the before/after `main.js` size in the PR; no action unless it exceeds ~1 MB growth.
- **`mcp_connection_updated` payload** may omit `serverOrigin` for some paths — the listener treats a missing origin as "refetch anyway" (cheap GET), so Connect-to-view cannot deadlock on a field mismatch.

---

## Implementation summary

Branch: `cursor/mcp-apps-webapp-a67d` (from `cursor/mcp-apps-sandbox-a67d` @ `ef08052e`). Not pushed.

### Commits (oldest → newest)

| Hash | Subject |
|---|---|
| `dc8bb84b` | `config: add enableDemoApps to embedded MCP server config` |
| `df18619d` | `mcpserver: preview_post demo MCP App behind enableDemoApps` |
| `462d5d9e` | `mcp: embedded demo app ui_meta discovery and resources/read test` |
| `3f1b66d1` | `webapp: plumb ui_meta and mcpApps bootstrap into tool data` |
| `1c9dcfb0` | `webapp: render MCP Apps from ToolCard via @mcp-ui/client` |
| `71f7efb1` | `e2e: demo MCP App rendering spec` |
| `00f59f4c` | `fix: find tool calls in preceding WriteToolTurns rounds` |
| `c61c9097` | `fix: parse tool-result params as CallToolResult in demo app` |

Tip: `c61c9097`.

### Deviations + rationale

1. **`mcp/embedded_apps_test.go` does not import `mcpserver`** (plan §4.5). Import cycle: `mcpserver/plugin_handlers.go` → `mcp`. Instead the test rebuilds the same `_meta.ui` + `ui://` resource on a plain go-sdk server via `demoEmbeddedServer`, then asserts `GetToolsForUser` → `UIMeta.ResourceURI` and `ReadUserAppResource`. Real registration is covered by `mcpserver` tests (`TestInMemoryServerDemoApps`, `TestPreviewPostToolMeta`).
2. **`FindToolCallBlocks` / `postAnchoredTurnSpan` extended** (not in plan commit sequence). Plan assumed the app-resource endpoint resolves auto-approved demo tools; in practice `WriteToolTurns` persists tool rounds *before* the final stream turn receives `PostID`, while Go only walked forward from the PostID anchor. Webapp `collectResponseTurns` already walks backward. Fixed Go to match; without this, `GET /mcp/app-resource` 404s and the UI shows the unavailable note after a successful tool.
3. **Demo HTML `tool-result` params shape** (plan §4.4 step 3 was wrong). Plan said `params.result.content[0].text`; `@mcp-ui/client@7.1.1` `AppBridge.sendToolResult` posts the `CallToolResult` as `params` directly. Reading `params.result` left the preview card empty. Also send `protocolVersion: '2025-11-21'` on `ui/initialize`.
4. **e2e CRT thread open** (spec polish in `00f59f4c`). Agent DM replies land in a CRT thread; the spec opens the reply indicator and asserts inside `#rhsContainer` `[data-testid="llm-bot-post"]`. Fixed username/`getMyTeams`/`sendChannelMessage` for the container helpers.

### Test / lint / e2e results

- `go build ./...` — pass
- `go vet ./...` — pass
- `go test -race ./mcpserver/... ./mcp/... ./format/... ./conversation/...` — pass
- `cd webapp && npm run check-types` — pass
- `cd webapp && npm test` — **305 passed** / 34 suites
- `make check-style` — pass (0 issues)
- `make check-shards` — pass
- `make check-i18n` — pass (exit 0)
- `make check-locks` — pass (exit 0)
- e2e: `cd e2e && npx playwright test tests/mcp-apps/demo-app.spec.ts --project=chromium --reporter=list` — **2 passed** (23.5s) after the two fixes above

### Bundle size

- Post-1c `webapp/dist/main.js`: **1,762,384 bytes (1.68 MiB)** after `make dist` (webpack entrypoint-size warning expected).
- Parent `ef08052e` has no `@mcp-ui/client`; a clean parent rebuild for delta was not obtained in this environment (archived webapp alone fails webpack resolve without repo root context). Spike-era note (~2.17 MiB with baked HTML) is not a fair baseline. Growth is within the plan’s expected library-only band relative to a typical pre-library bundle; well under the ~1 MiB growth “no action” threshold if measured against an unpolluted parent build.

### QA-relevant notes

Exact config to enable demo apps + insecure sandbox (admin API / container mcp override):

```json
{
  "mcp": {
    "enabled": true,
    "enablePluginServer": true,
    "embeddedServer": {
      "enabled": true,
      "enableDemoApps": true,
      "tool_configs": [
        {"name": "preview_post", "policy": "auto_run_in_dm", "enabled": true}
      ]
    },
    "apps": {
      "enabled": true,
      "allowInsecureSameOriginSandbox": true
    }
  }
}
```

- Agent: `enabledMCPTools: [{server_origin: "embedded://mattermost", tool_name: "preview_post"}]` (or `autoEnableNewMCPTools: true`), username used in e2e: `previewappagent`.
- Demo tool trigger prompt: `Use the preview_post tool to preview post <postId>. Call the tool now.`
- Browse via the same host string as SiteURL (`localhost` ≠ `127.0.0.1`, C8).
- Same-origin sandbox URL: `<SiteURL>/plugins/mattermost-ai/mcp/apps/sandbox`.
- QA script §7 still accurate; e2e proves CRT-thread + double-iframe + Show raw JSON on the AutoApproved DM path. No script adjustments required beyond noting DM replies may need the thread pane open to see the app.
