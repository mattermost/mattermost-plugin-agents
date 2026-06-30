# mcp/AGENTS.md

MCP **client** (`mcp/`), tool execution (`toolrunner/`), and plugin-native LLM tools (`mmtools/`). Root `/AGENTS.md` applies; `mcpserver/AGENTS.md` covers the server side.

## Client (`mcp/`)

- Config types live in `config/` and are aliased here (`mcp.Config = config.MCPConfig`). Per-tool policy `MCPToolConfig.policy` ∈ `ask` | `auto_run_in_dm` | `auto_run_everywhere`; unconfigured tools default to `("ask", true)` (`config/mcp_config.go`).
- `ClientManager` caches one client per user; idle clients close after `IdleTimeoutMinutes` (≤0 → 30).
- **Remote** connects use `cacheableContext(ctx)` (`context.WithoutCancel`) so a cancelled request doesn't break cached OAuth/connect state. **Embedded** and **plugin** connections use the cancelable request ctx and are not cached. Remote transport is Streamable HTTP with SSE fallback; header `X-Mattermost-UserID` on all remote calls.
- Embedded server: key `embedded://mattermost`, requires an auto-created MCP session in KV (`mcp_embedded_session_id_<userID>`); reconnect on `ErrConnectionClosed` needs a non-empty session ID.
- Tool list cache: KV prefix `mcp_tools_cache_v1_`, ~8-day TTL; shared across users only when the server has no static OAuth client ID.
- Namespacing: bare tool names are namespaced `server__tool` (`llm.NamespaceMCPToolName`, slug from `dedupeMCPServerSlug`). If a namespaced name still collides, **first wins, second is skipped with a Warn** (`user_clients.go`). Policy lookup uses `ToolPolicyLookupName` (bare name unless an exact configured name exists).

## Dynamic MCP loading (bot `MCPDynamicToolLoading`, default on)

The active store gets only meta-tools `search_tools` / `load_tool` (`mcp/meta_tools.go`); full MCP tools go to `ToolStore.SetUnloadedMCPTools`. `LoadMCPTools` needs exact namespaced names. Meta-tool calls auto-execute (`mcp.IsMCPMetaTool`); unloaded calls return `mcp.UnloadedMCPToolUserHint(name)`. This is the usual cause of "a tool isn't visible" — check it before debugging discovery.

## Execution (`toolrunner/`)

- Build with `toolrunner.New(lm, toolrunner.WithMaxRounds(bot.GetConfig().EffectiveMaxToolTurns()))`. `Run()` returns the stream immediately; `ToolTurns` is populated async — only read it after fully consuming the stream.
- If **any** call in a batch needs approval, the whole batch is left unresolved on the stream; resume via `conversations.HandleToolCall`.
- Unknown/unloaded tools yield synthetic errors with no approval UI. ≥3 consecutive failures disables tools; the penultimate round forces synthesis (`WithToolsDisabled()`).
- Default max rounds is `llm.DefaultMaxToolTurns` (30). `toolrunner/limits.MaxToolRounds` (10) is the loadtest cap only.
- Span `"resolve tool"`; approval spans live in `conversations/tool_approval.go`.

## Plugin-native tools (`mmtools/`)

`mmtools/` ≠ Mattermost MCP tools. Entity tools (`read_post`, `search_posts`, …) live in `mcpserver/tools/`. `mmtools/` are direct `llm.Tool` builtins wired through `MMToolProvider` in `llmcontext/`. To add one: new file, schema via `llm.NewJSONSchemaFromStruct[T]()`, append in `MMToolProvider.GetTools`. `AskUserQuestion` sets `UserInteraction` and its resolver must error (answered via `Conversations.HandleToolCall`); only cataloged when interactive. Format Mattermost entity output through `format/`, never inline.

## Testing

Table-driven, in-package fakes; no new mocking libraries. MCP integration tests are build-tagged: `go test -tags=integration ./mcp/...` (shared Mattermost container). `toolrunner` tests use a fake `llm.LanguageModel` (no network).
