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
    response, err := p.llmClient.AgentCompletion("gpt4", bridgeclient.CompletionRequest{
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
// Request by agent username
response, err := client.AgentCompletion("gpt4", request)

// Request by service name
response, err := client.ServiceCompletion("openai", request)
```

### Streaming

```go
import "github.com/mattermost/mattermost-plugin-ai/llm"

// Start streaming request
result, err := client.AgentCompletionStream("gpt4", request)
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
response, err := client.AgentCompletion("gpt4", request)
```

If not using built-in permission checks, your plugin must verify permissions before making requests.

## Agent vs Service

- **Agent**: Target a specific bot by username (e.g., "gpt4")
  - Uses bot's custom configuration, tools, and prompts

- **Service**: Target an LLM service by name (e.g., "openai", "anthropic")
  - Uses any bot configured with that service
  - Useful when bot-specific configuration doesn't matter
