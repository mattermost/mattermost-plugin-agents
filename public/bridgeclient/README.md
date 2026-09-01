# Mattermost AI Plugin - LLM Bridge Client

Go client library for Mattermost plugins and the server to interact with the AI plugin's LLM Bridge API.

## Quick Start

### From a Plugin

```go
import "github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"

type MyPlugin struct {
    plugin.MattermostPlugin
    llmClient *bridgeclient.Client
}

func (p *MyPlugin) OnActivate() error {
    p.llmClient = bridgeclient.NewClient(p.API)
    return nil
}

func (p *MyPlugin) handleCommand() {
    // Get the bot ID first (e.g., from discovery or configuration)
    botID := "bot-user-id-here"
    response, err := p.llmClient.AgentCompletion(botID, bridgeclient.CompletionRequest{
        Posts: []bridgeclient.Post{
            {Role: "user", Message: "What is the capital of France?"},
        },
    })
    // Handle response...
}
```

### From Mattermost Server

```go
import "github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"

type MyService struct {
    app       *app.App
    llmClient *bridgeclient.Client
}

func NewMyService(app *app.App, userID string) *MyService {
    return &MyService{
        app:       app,
        llmClient: bridgeclient.NewClientFromApp(app, userID),
    }
}

func (s *MyService) process() {
    response, err := s.llmClient.ServiceCompletion("anthropic", bridgeclient.CompletionRequest{
        Posts: []bridgeclient.Post{
            {Role: "user", Message: "Write a haiku"},
        },
    })
    // Handle response...
}
```

## API Methods

### Non-Streaming

```go
// Request by agent Bot ID
response, err := client.AgentCompletion("bot-user-id", request)

// Request by service ID or service name
response, err := client.ServiceCompletion("openai", request)
```

`allowed_tools` is supported only on agent endpoints. Service endpoints reject it, tools are
disabled there, and `tool_hooks` are ignored.

### Streaming

The streaming methods return `*llm.TextStreamResult`, so a streaming caller also imports
the plugin's `llm` package for the event types:

```go
import "github.com/mattermost/mattermost-plugin-agents/v2/llm"

result, err := client.AgentCompletionStream("bot-user-id", request)
if err != nil {
    return err
}

for event := range result.Stream {
    switch event.Type {
    case llm.EventTypeText:
        fmt.Print(event.Value.(string))

    case llm.EventTypeError:
        // Errors reported by the server arrive as a string. Errors the client hit
        // while reading the stream arrive as an error. Handle both: asserting
        // event.Value.(error) on its own panics on every server-side failure.
        if err, ok := event.Value.(error); ok {
            return err
        }
        return fmt.Errorf("%v", event.Value)

    case llm.EventTypeEnd:
        return nil
    }
}
```

Three things to know when writing that loop:

- **More event types exist than the three above.** The bridge forwards every event the
  model produces. Reasoning-enabled agents also emit `EventTypeReasoning` and
  `EventTypeReasoningEnd`, and you may see `EventTypeUsage`, `EventTypeAnnotations`, and
  `EventTypeToolCalls`. Ignoring them is fine, as the example does; assuming they cannot
  arrive is not.
- **`Value` is `any`, and it has been through JSON.** `EventTypeText` is always a
  `string`. Structured payloads arrive as `map[string]interface{}` or `[]interface{}`
  rather than their original Go types, so assert defensively.
- **Read the stream to the end.** The channel closes after `EventTypeEnd` or
  `EventTypeError`, so ranging over it terminates. If you stop reading earlier, the
  client's reader goroutine stays blocked on its next send and leaks.

### Multi-turn Conversations

```go
request := bridgeclient.CompletionRequest{
    Posts: []bridgeclient.Post{
        {Role: "system", Message: "You are a helpful assistant"},
        {Role: "user", Message: "What is AI?"},
        {Role: "assistant", Message: "AI stands for..."},
        {Role: "user", Message: "Can you give examples?"},
    },
}
```

### Agent tool allowlist

Each entry in `allowed_tools` is a string tool name. The bridge accepts **both**
the namespaced runtime name (`server__tool`, e.g. `mattermost__search_posts`) and
the bare name (`search_posts`). `GetAgentTools` returns both forms per tool
(`Name` is namespaced, `BareName` is bare). Bare names remain supported for backward compatibility.

```go
request := bridgeclient.CompletionRequest{
    Posts: []bridgeclient.Post{
        {Role: "user", Message: "Use the eligible MCP tool"},
    },
    AllowedTools: []string{"mattermost__search_posts", "read_channel"},
    UserID:       userID, // Required when using AllowedTools
}

response, err := client.AgentCompletion("bot-user-id", request)
```

When `AllowedTools` is provided:
- only tools in the list may run
- tool execution is auto-run (no approval flow)
- tools must come from enabled MCP servers or embedded MCP servers (built-in agent tools are not exposed for bridge allowlists)
- empty lists and blank tool names are rejected by the bridge API
- a bare name that exists on more than one MCP server is ambiguous and is
  rejected; pass the namespaced name (`server__tool`) to target a specific
  server's tool

### Tool hooks

`ToolHooks` lets your plugin approve or reject each tool call before it runs. Keys are tool
names (bare or namespaced, same as `allowed_tools`); each value carries an HTTP path in your
own plugin that the agents plugin calls first. Don't key the same tool twice — supplying
both `search_posts` and `mattermost__search_posts` is rejected.

```go
import "github.com/mattermost/mattermost-plugin-agents/v2/public/mcptool"

request := bridgeclient.CompletionRequest{
    Posts:        []bridgeclient.Post{{Role: "user", Message: "Search for the incident"}},
    AllowedTools: []string{"mattermost__search_posts"},
    UserID:       userID,
    ToolHooks: map[string]bridgeclient.ToolHookConfig{
        "mattermost__search_posts": {BeforeCallback: "/hooks/tool-check/run-abc123"},
    },
}
```

`BeforeCallback` must start with `/` and is resolved under `/plugins/<your-plugin-id>`; a
path that escapes that namespace is rejected. It must be a **path only — a query string is
rejected**, so encode run context in path segments (`/hooks/tool-check/run-abc123`, not
`/hooks/tool-check?run=abc123`). The agents plugin never inspects the path beyond those
checks.

Your handler receives a `POST` with `Content-Type: application/json` and this body:

```go
type BeforeHookRequest struct {
    ToolName string          `json:"tool_name"`
    Args     json.RawMessage `json:"args"` // validated, decoded tool arguments
    UserID   string          `json:"user_id"`
}
```

An `Authorization: Bearer` header carrying the acting user's token is included when the
MCP client has one, so treat it as optional. Authorize on `user_id` from the body rather
than depending on that header.

Reply `2xx` with a `mcptool.BeforeHookResponse`. An empty `error` lets the call proceed; a
non-empty `error` rejects it, and that string is handed to the model as the tool's error:

```go
func (p *MyPlugin) handleToolCheck(w http.ResponseWriter, r *http.Request) {
    var hookReq mcptool.BeforeHookRequest
    _ = json.NewDecoder(r.Body).Decode(&hookReq)

    resp := mcptool.BeforeHookResponse{}
    if !p.userMayRunTool(hookReq.UserID, hookReq.ToolName) {
        resp.Error = "not permitted for this user"
    }

    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(resp)
}
```

Hooks are **fail-closed**: if your endpoint is unreachable, returns a non-2xx, or returns a
body that doesn't parse, the tool call fails.

Three requirements, each a `400` when missing:

- `allowed_tools` must be set — hooks only apply to allowlisted tools
- `user_id` must be set
- the request must carry the `Mattermost-Plugin-ID` header, which identifies the plugin
  whose namespace the callback resolves in (the client's transports set this for you)

`ToolHooks` is ignored on service endpoints, where tools are disabled.

## Permission Checking

By default, the bridge does not check permissions. On **agent** endpoints, `UserID` and
optionally `ChannelID` turn on permission checking against that agent's access
configuration:

```go
request := bridgeclient.CompletionRequest{
    Posts: []bridgeclient.Post{
        {Role: "user", Message: "Hello"},
    },
    UserID:    userID,    // Checks user-level permissions
    ChannelID: channelID, // Also checks channel-level permissions
}

// Returns 403 Forbidden if user lacks permission
response, err := client.AgentCompletion("bot-user-id", request)
```

On **service** endpoints the same two fields are attribution only: they are recorded for
user, channel, and team attribution in token usage logs and request context, and no user or
channel permission check runs. Inter-plugin trust of the bridge remains the security
boundary for service completions, so your plugin must verify permissions itself before
calling them — and before any agent call where you don't pass `UserID`.

## Structured Output

Supplying `JSONOutputFormat` (a JSON schema) is the only signal this client sends to request
structured output; there is no separate flag:

```go
request := bridgeclient.CompletionRequest{
    Posts: []bridgeclient.Post{
        {Role: "user", Message: "Extract the incident fields"},
    },
    JSONOutputFormat: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "severity": map[string]interface{}{"type": "string"},
        },
    },
}
```

How the schema is fulfilled is not a client or agent decision. It's controlled by the
structured output policy an administrator sets on the target service in the System Console:

- `auto` (default, and what an empty stored value means): the schema is sent natively to the
  provider only for provider, model, and API-path combinations positively known to support
  native structured output. Everything else uses the prompt fallback, where the schema is
  converted into prompt instructions and not sent to the provider.
- `native`: the administrator asserts the service is natively capable. This exists for
  custom and OpenAI-compatible endpoints whose capabilities can't be detected.
- `prompt_fallback`: the schema is never sent natively.

The decision is made once per request and covers the target service's whole fallback chain:
native output is used only when every possible attempt — the primary service and every
service in its fallback chain — is known-capable or asserted capable. Otherwise the prompt
fallback applies to the entire request. Either way, the response arrives in the same
response and SSE formats, so callers parse JSON from the completion text as usual.

Agent-level structured output configuration is deprecated and ignored; the service policy
applies to both agent and direct-service completions.

## Token Usage Dimensions

Bridge callers can optionally provide `Operation` and `OperationSubType` in `CompletionRequest` to customize token usage categorization in logs.

If omitted, the bridge keeps current defaults:

- `Operation`: `bridge_agent` or `bridge_service` (based on endpoint)
- `OperationSubType`: `streaming` or `nostream` (based on request mode)

Token usage records also identify the service that served the request (`service_id` and
`service_name`). On service completions there is no agent, so the agent dimensions
(`agent_name`, `agent_username`, `bot_username`, `agent_user_id`) are logged as empty
strings.

```go
request := bridgeclient.CompletionRequest{
    Posts: []bridgeclient.Post{
        {Role: "user", Message: "Summarize incident timeline"},
    },
    Operation:        "playbooks_summary",
    OperationSubType: "incident_report",
}
```

## Agent vs Service

- **Agent**: Target a specific bot by its Bot ID (the immutable Mattermost Bot User ID)
  - Uses bot's custom configuration, tools, and prompts
  - Get bot IDs via the `GetAgents()` discovery endpoint

- **Service**: Target a configured LLM service by ID or name (e.g., "openai", "anthropic")
  - Config-backed and independent of agents: the bridge resolves the stored service
    configuration and calls the provider directly, so a service is usable even when no agent
    references it
  - Uses the service's own `defaultModel`. No agent settings apply — no model override,
    reasoning, native tools, custom instructions, or agent access restrictions
  - No user or channel permission check runs (see [Permission Checking](#permission-checking))
  - Useful when agent-specific configuration doesn't matter
  - Get service IDs and names via the `GetServices()` discovery endpoint

### Service resolution

The `service` path value is matched against stored configuration in this order:

1. exact service ID
2. service name

An ID match always wins, so a service whose *name* happens to equal another service's ID
never shadows that ID. When several configured services share an ID or a name, the first
matching entry in configuration order is used. A service stored with a blank name is listed
by discovery and callable, but by ID only.

### Service completion errors

| Status | Message | What it means |
|---|---|---|
| 404 | `service not found: <value>` | Nothing matched, **or** it matched a service the bridge can't call: invalid config, no `defaultModel`, or an unsupported provider type |
| 400 | `posts array cannot be empty` | `Posts` was empty |
| 400 | `allowed_tools is only supported for agent completion endpoints` | Tools are disabled on service endpoints; drop the field |
| 500 | `service "<id>" is not usable: ...` | The service itself is fine, but its fallback chain is broken (missing, invalid, or cyclic member). A server misconfiguration, not a bad request |
| 500 | `failed to build a language model for service "<id>": ...` | The provider client couldn't be constructed, e.g. bad credentials |

A 404 covers both "no such service" and "service exists but isn't callable", so don't treat
it as proof the name is wrong. `GetServices()` only lists callable services, so anything it
returns will resolve.


## Discovery Endpoints

The bridge API provides discovery endpoints to help clients find available agents and services before making completion requests.

### Get Available Agents

```go
// Get all agents
agents, err := client.GetAgents("")
if err != nil {
    return err
}

for _, agent := range agents {
    fmt.Printf("Agent: %s (ID: %s, Username: %s) - Service: %s (%s)\n",
        agent.DisplayName, agent.ID, agent.Username, agent.ServiceID, agent.ServiceType)
    
    // Use agent.ID when making completion requests
    // response, err := client.AgentCompletion(agent.ID, request)
}
```

### Get Available Services

```go
// Get all services
services, err := client.GetServices("")
if err != nil {
    return err
}

for _, service := range services {
    fmt.Printf("Service: %s (%s) - Type: %s\n",
        service.Name, service.ID, service.Type)
}
```

This returns every configured service the bridge can actually construct and call: valid
configuration, a non-empty default model, a provider type the bridge supports, and a
fallback chain (if any) whose members are themselves bridge-capable. Services that no agent
references are included, and `Name` is empty for a service stored with a blank name.

### Get Eligible Tools for an Agent

```go
// Get bridge-eligible tools for an agent
tools, err := client.GetAgentTools("bot-user-id", "")
if err != nil {
    return err
}

for _, tool := range tools {
    fmt.Printf("Tool: %s (bare: %s) - %s\n", tool.Name, tool.BareName, tool.Description)
}
```

This endpoint returns only tools that are currently eligible for `AllowedTools`.
Eligible tools come from enabled MCP servers and embedded MCP servers.
If no eligible tools are available, this returns an empty list.

Each entry exposes both `Name` (namespaced) and `BareName`. Either may be passed
in `allowed_tools`; validate stored allowlists against either field.

You can optionally pass `userID` to apply user-level permission filtering:

```go
tools, err := client.GetAgentTools("bot-user-id", userID)
```

If `userID` does not have access to the agent, the request fails with a permission error.

### Discovery with User Permissions

`GetAgents` and `GetAgentTools` support optional user filtering, which is useful for showing
users only the agents and tools they have permission to use:

```go
// Get agents accessible to a specific user
agents, err := client.GetAgents(userID)
```

`GetServices` keeps the same signature and still sends `user_id`, but service discovery no
longer depends on agent access:

```go
// userID is sent for compatibility with older servers; new servers ignore it
services, err := client.GetServices(userID)
```

Older servers filtered services by the agents a user could access. New servers accept and
syntax-validate `user_id` — an invalid ID is still rejected — and then ignore it, returning
all bridge-capable configured services. Keep passing the user ID if you support both server
versions; don't rely on it to restrict what a user may call.
