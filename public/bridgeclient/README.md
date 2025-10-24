# Mattermost AI Plugin - LLM Bridge Client

This package provides a Go client library for Mattermost plugins to interact with the AI plugin's LLM Bridge API.

## Installation

```go
import "github.com/mattermost/mattermost-plugin-ai/public/client"
```

## Quick Start

### Basic Setup

```go
package main

import (
    "github.com/mattermost/mattermost-plugin-ai/public/client"
    "github.com/mattermost/mattermost/server/public/plugin"
)

type MyPlugin struct {
    plugin.MattermostPlugin
    llmClient *client.Client
}

func (p *MyPlugin) OnActivate() error {
    // Create the LLM Bridge client - it's this simple!
    p.llmClient = client.NewClient(&p.MattermostPlugin)
    return nil
}
```

That's it! The client automatically handles inter-plugin communication using the Mattermost plugin API.

### Alternative Setup with PluginAPI

If you want more flexibility or are already using `pluginapi.Client`, you can create the client directly from the API:

```go
import (
    "github.com/mattermost/mattermost-plugin-ai/public/client"
    "github.com/mattermost/mattermost/server/public/plugin"
)

type MyPlugin struct {
    plugin.MattermostPlugin
    llmClient *client.Client
}

func (p *MyPlugin) OnActivate() error {
    // Create client using the API interface directly
    p.llmClient = client.NewClientFromAPI(p.API)
    return nil
}
```

Both constructors work identically - choose whichever fits your plugin's architecture better.

## API Methods

### Non-Streaming Completions

#### Agent Completion

Make a request to a specific agent (bot) by username:

```go
response, err := p.llmClient.AgentCompletion("gpt4", client.CompletionRequest{
    Posts: []client.Post{
        {Role: "user", Message: "What is the capital of France?"},
    },
})
if err != nil {
    // Handle error
}
fmt.Println(response) // "The capital of France is Paris."
```

#### Service Completion

Make a request to a specific LLM service by ID or name:

```go
response, err := p.llmClient.ServiceCompletion("openai", client.CompletionRequest{
    Posts: []client.Post{
        {Role: "user", Message: "Write a haiku about coding"},
    },
})
```

### Streaming Completions

Streaming methods return a `*llm.TextStreamResult` that provides fine-grained control over stream events and matches the internal LLM API:

```go
import "github.com/mattermost/mattermost-plugin-ai/llm"

// Agent streaming
result, err := p.llmClient.AgentCompletionStream("gpt4", client.CompletionRequest{
    Posts: []client.Post{
        {Role: "user", Message: "Tell me a story"},
    },
})
if err != nil {
    return err
}

// Process events from the stream
for event := range result.Stream {
    switch event.Type {
    case llm.EventTypeText:
        // Text chunk received
        fmt.Print(event.Value.(string))
    case llm.EventTypeError:
        // Error occurred
        return event.Value.(error)
    case llm.EventTypeEnd:
        // Stream completed successfully
        return nil
    case llm.EventTypeUsage:
        // Token usage information
        usage := event.Value.(llm.TokenUsage)
        fmt.Printf("Tokens: %d input, %d output\n", usage.InputTokens, usage.OutputTokens)
    }
}
```

The `TextStreamResult` also provides a `ReadAll()` helper method to accumulate all text:

```go
result, err := p.llmClient.AgentCompletionStream("gpt4", request)
if err != nil {
    return err
}

// ReadAll accumulates all text events and returns the complete response
text, err := result.ReadAll()
if err != nil {
    return err
}
fmt.Println(text)
```

Service streaming works the same way:

```go
result, err := p.llmClient.ServiceCompletionStream("anthropic", client.CompletionRequest{
    Posts: []client.Post{
        {Role: "user", Message: "Explain quantum computing"},
    },
})
if err != nil {
    return err
}

for event := range result.Stream {
    // Handle events...
}
```

## Request Structure

### Posts

A `Post` represents a single message in the conversation:

```go
type Post struct {
    Role    string // "user", "assistant", "system"
    Message string // The message content
    Files   []File // Optional file attachments
}
```

### Multi-turn Conversations

```go
request := client.CompletionRequest{
    Posts: []client.Post{
        {Role: "system", Message: "You are a helpful assistant"},
        {Role: "user", Message: "What is AI?"},
        {Role: "assistant", Message: "AI stands for Artificial Intelligence..."},
        {Role: "user", Message: "Can you give me examples?"},
    },
}

response, err := p.llmClient.AgentCompletion("gpt4", request)
```

### Including Files

Files must be base64 encoded:

```go
import "encoding/base64"

fileData, _ := os.ReadFile("image.png")
encodedData := base64.StdEncoding.EncodeToString(fileData)

request := client.CompletionRequest{
    Posts: []client.Post{
        {
            Role:    "user",
            Message: "What's in this image?",
            Files: []client.File{
                {
                    ID:       "file123",
                    Name:     "image.png",
                    MimeType: "image/png",
                    Data:     encodedData,
                },
            },
        },
    },
}
```

## Error Handling

All methods return errors that should be checked:

```go
response, err := p.llmClient.AgentCompletion("gpt4", request)
if err != nil {
    // Possible errors:
    // - Network errors
    // - Agent/service not found (404)
    // - Invalid request (400)
    // - Internal server errors (500)
    p.API.LogError("LLM request failed", "error", err.Error())
    return
}
```

For streaming requests, errors can occur during streaming:

```go
result, err := p.llmClient.AgentCompletionStream("gpt4", request)
if err != nil {
    p.API.LogError("Failed to start stream", "error", err.Error())
    return
}

for event := range result.Stream {
    switch event.Type {
    case llm.EventTypeText:
        fmt.Print(event.Value.(string))
    case llm.EventTypeError:
        p.API.LogError("Streaming error", "error", event.Value)
        return
    case llm.EventTypeEnd:
        return
    }
}
```

## Complete Example

```go
package main

import (
    "fmt"
    "github.com/mattermost/mattermost-plugin-ai/public/client"
    "github.com/mattermost/mattermost/server/public/plugin"
)

type MyPlugin struct {
    plugin.MattermostPlugin
    llmClient *client.Client
}

func (p *MyPlugin) OnActivate() error {
    p.llmClient = client.NewClient(&p.MattermostPlugin)
    return nil
}

func (p *MyPlugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
    // Example: Use AI to respond to a command
    response, err := p.llmClient.AgentCompletion("gpt4", client.CompletionRequest{
        Posts: []client.Post{
            {
                Role:    "system",
                Message: "You are a helpful assistant for my plugin",
            },
            {
                Role:    "user",
                Message: args.Command,
            },
        },
    })
    if err != nil {
        return nil, model.NewAppError("ExecuteCommand", "app.plugin.ai.error", nil, err.Error(), http.StatusInternalServerError)
    }

    return &model.CommandResponse{
        ResponseType: model.CommandResponseTypeEphemeral,
        Text:         response,
    }, nil
}

func (p *MyPlugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
    // Example: Stream AI response to a channel
    go func() {
        result, err := p.llmClient.AgentCompletionStream("gpt4", client.CompletionRequest{
            Posts: []client.Post{
                {Role: "user", Message: post.Message},
            },
        })
        if err != nil {
            p.API.LogError("Failed to start stream", "error", err.Error())
            return
        }

        // Use ReadAll helper to get the complete response
        fullResponse, err := result.ReadAll()
        if err != nil {
            p.API.LogError("Streaming failed", "error", err.Error())
            return
        }

        // Post the complete response
        p.API.CreatePost(&model.Post{
            ChannelId: post.ChannelId,
            Message:   fullResponse,
        })
    }()
}
```

## Security Notice

⚠️ **Important**: The AI plugin's inter-plugin API does not perform permission checks. Your plugin is responsible for verifying that users have appropriate permissions before making requests on their behalf.

Example permission check:

```go
func (p *MyPlugin) handleUserRequest(userID, message string) error {
    // Check if user has permission to use AI features
    user, err := p.API.GetUser(userID)
    if err != nil {
        return err
    }

    // Add your permission logic here
    if !p.userCanUseAI(user) {
        return fmt.Errorf("user does not have permission to use AI features")
    }

    // Make the AI request
    response, err := p.llmClient.AgentCompletion("gpt4", client.CompletionRequest{
        Posts: []client.Post{
            {Role: "user", Message: message},
        },
    })
    // ... handle response
}
```

## API Endpoints

The client calls these endpoints on the AI plugin:

- `POST /mattermost-ai/api/v1/agent/{agent}/completion` - Streaming agent completion (SSE with JSON events)
- `POST /mattermost-ai/api/v1/agent/{agent}/completion/nostream` - Non-streaming agent completion
- `POST /mattermost-ai/api/v1/service/{service}/completion` - Streaming service completion (SSE with JSON events)
- `POST /mattermost-ai/api/v1/service/{service}/completion/nostream` - Non-streaming service completion

All endpoints use inter-plugin communication via the Mattermost plugin API.

### Streaming Event Format

Streaming endpoints use Server-Sent Events (SSE) with JSON-encoded event payloads:

```
data: {"type":0,"value":"Hello"}
data: {"type":0,"value":" world"}
data: {"type":1,"value":null}
```

Event types:
- `0` - Text chunk
- `1` - End of stream
- `2` - Error
- `3` - Tool calls
- `7` - Token usage

## Differences Between Agent and Service

- **Agent**: Refers to a specific bot by username (e.g., "gpt4", "claude")
  - Use when you want to target a specific bot configuration with custom settings, tools, and prompts

- **Service**: Refers to the underlying LLM service by ID or name (e.g., "openai", "anthropic")
  - Use when you want any bot that uses a particular LLM service
  - Useful when you don't care about bot-specific configuration

## Additional Resources

- See `example_test.go` for more usage examples
- Refer to `API.md` in the plugin root for the full API specification
- Check the AI plugin documentation for available agents and services
