# public/AGENTS.md

Consumer-facing Go packages that other Mattermost plugins / server code import. Root `/AGENTS.md` applies.

## Not a separate module

`public/bridgeclient` and `public/mcptool` are subpackages of the root module `github.com/mattermost/mattermost-plugin-agents` (there is no nested `go.mod`). Consumers `go get` the plugin module and import the subpath; the streaming APIs pull in `…/llm` transitively. The `Makefile` clears `HAS_PUBLIC` so these sources are **not** shipped as plugin HTTP assets — keep that override.

## bridgeclient

Inter-plugin client for the LLM Bridge API on plugin `mattermost-ai` (`bridgeclient.AiPluginID`).

- Constructors: `NewClient(PluginAPI)` (from a plugin) and `NewClientFromApp(AppAPI, userID)` (from server code).
- Calls `/{mattermost-ai}/bridge/v1/...`; non-streaming completion URLs end in `/nostream`. An agent is a 26-char bot user ID (`ValidateID`).
- Discovery: `GetAgents`, `GetServices`, `GetAgentTools` (optional `userID` query for permission filtering).
- Permissions are off by default — set `CompletionRequest.UserID`/`ChannelID` to enable bridge-side checks. `AllowedTools` are MCP/embedded tools only, auto-run with no approval, and require `UserID`.
- `ToolHooks` before-callbacks dispatch back to the **calling** plugin (needs the `Mattermost-Plugin-ID` header); wire types are in `public/mcptool/hooks.go`.

## When editing here

Preserve backward compatibility on JSON field names and bridge paths (external consumers depend on them). Tests: `go test ./public/bridgeclient/...`. Human-facing examples are in `public/bridgeclient/README.md`.
