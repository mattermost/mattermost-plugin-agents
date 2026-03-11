# Mattermost AI Plugin - LLM Bridge Client

Go client library for Mattermost plugins and the server to interact with the AI plugin's LLM Bridge API.

## Quick Start

### From a Plugin

```go
import "github.com/mattermost/mattermost-plugin-ai/public/bridgeclient"

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
import "github.com/mattermost/mattermost-plugin-ai/public/bridgeclient"

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

// Request by service name
response, err := client.ServiceCompletion("openai", request)
```

`allowed_tools` is supported only on agent endpoints. Service endpoints reject it.

### Streaming

```go
import "github.com/mattermost/mattermost-plugin-ai/llm"

// Start streaming request (using Bot ID)
result, err := client.AgentCompletionStream("bot-user-id", request)
if err != nil {
    return err
}

// Process events
for event := range result.Stream {
    switch event.Type {
    case llm.EventTypeText:
        fmt.Print(event.Value.(string))
    case llm.EventTypeError:
        return event.Value.(error)
    case llm.EventTypeEnd:
        return nil
    }
}
```

For `llm.EventTypeError`, the client normalizes server error payloads into Go `error` values.

For non-200 HTTP responses, the client surfaces `{"error":"..."}` when available and falls back
to raw response body text if the payload is not in that shape.

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

### Agent tool allowlist (service-account eligible tools only)

```go
request := bridgeclient.CompletionRequest{
    Posts: []bridgeclient.Post{
        {Role: "user", Message: "Use the eligible MCP tool"},
    },
    AllowedTools: []string{"eligible_tool_name"},
}

response, err := client.AgentCompletion("bot-user-id", request)
```

When `AllowedTools` is provided:
- only tools in the list may run
- tool execution is auto-run (no approval flow)
- tools must be bridge-eligible (service-account style / non user-scoped auth)
- empty lists and blank tool names are rejected by the bridge API

## Permission Checking

By default, the bridge does not check permissions. To enable permission checking, include `UserID` and optionally `ChannelID` in your request:

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

If not using built-in permission checks, your plugin must verify permissions before making requests.

The bridge API trims surrounding whitespace on `UserID` and `ChannelID` values. Whitespace-only
values are treated as unset. Non-empty malformed IDs are rejected by the bridge with `400 Bad Request`.
For backward compatibility, channel permission checks only run when `UserID` is also provided.
The same whitespace normalization for optional `user_id` applies to discovery endpoint query params.

## Token Usage Dimensions

Bridge callers can optionally provide `Operation` and `OperationSubType` in `CompletionRequest` to customize token usage categorization in logs.

If omitted, the bridge keeps current defaults:

- `Operation`: `bridge_agent` or `bridge_service` (based on endpoint)
- `OperationSubType`: `streaming` or `nostream` (based on request mode)

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

- **Service**: Target an LLM service by ID or name (e.g., "openai", "anthropic")
  - Uses any bot configured with that service
  - Useful when bot-specific configuration doesn't matter

## Discovery Endpoints

The bridge API provides discovery endpoints to help clients find available agents and services before making completion requests.
Agent and service discovery results are returned in deterministic sorted order (`display_name`/`name`, then `id`).

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

### Get Eligible Tools for an Agent

```go
// Get bridge-eligible tools for an agent
tools, err := client.GetAgentTools("bot-user-id", "")
if err != nil {
    return err
}

for _, tool := range tools {
    fmt.Printf("Tool: %s - %s\n", tool.Name, tool.Description)
}
```

This endpoint returns only tools that are currently eligible for `AllowedTools`.
In practice, this is limited to service-account style MCP tools configured for bridge eligibility.
If no eligible tools are available, this returns an empty list.
Results are returned in a deterministic order by tool name.

You can optionally pass `userID` to apply user-level permission filtering:

```go
tools, err := client.GetAgentTools("bot-user-id", userID)
```

If `userID` does not have access to the agent, the request fails with a permission error.

### Discovery with User Permissions

Like completion endpoints, discovery endpoints support optional user filtering:

```go
// Get agents accessible to a specific user
agents, err := client.GetAgents(userID)

// Get services accessible to a specific user (via their permitted agents)
services, err := client.GetServices(userID)
```

This is useful for showing users only the agents and services they have permission to use.

## Input Validation Behavior

The client performs local validation before issuing requests:

- `AgentCompletion`, `AgentCompletionStream`, and `GetAgentTools` trim surrounding whitespace and require a valid Mattermost Bot User ID format for `agent`.
- `GetAgents`, `GetServices`, and `GetAgentTools` trim surrounding whitespace and validate optional `userID` when provided. Whitespace-only `userID` values are treated as unset.
- `ServiceCompletion` and `ServiceCompletionStream` trim surrounding whitespace and reject empty service values.
- `UserID` and `ChannelID` in completion requests are validated by the bridge API (server-side) when non-empty.
- For raw HTTP callers (without this client), bridge handlers also validate path identifiers: malformed agent IDs and whitespace-only service path values return `400 Bad Request`.

When validation fails, methods return an error immediately without making an HTTP request.
