// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient_test

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// Example demonstrates basic usage of the LLM Bridge API client
func Example() {
	// In a real plugin, you would embed plugin.MattermostPlugin in your plugin struct
	type MyPlugin struct {
		plugin.MattermostPlugin
		llmClient *bridgeclient.Client
	}

	var p MyPlugin

	// Create the client in OnActivate
	p.llmClient = bridgeclient.NewClient(p.API)

	// Make a non-streaming request to an agent
	response, err := p.llmClient.AgentCompletion("gpt4", bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{
				Role:    "user",
				Message: "What is the capital of France?",
			},
		},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Response: %s\n", response)
}

// ExampleNewClientFromApp shows how to create a client from the app layer
func ExampleNewClientFromApp() {
	// This example shows how to use the client from the Mattermost app layer
	// instead of from a plugin. The app layer uses a different API.

	// In real code, you would pass your *app.App instance
	// llmClient := client.NewClientFromApp(appInstance)

	fmt.Println("Client can be created from app layer using NewClientFromApp")
}

// ExampleClient_AgentCompletion shows how to make a simple non-streaming request
func ExampleClient_AgentCompletion() {
	type MyPlugin struct {
		plugin.MattermostPlugin
	}

	var p MyPlugin
	llmClient := bridgeclient.NewClient(p.API)

	response, err := llmClient.AgentCompletion("gpt4", bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Hello!"},
		},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Response: %s\n", response)
}

// ExampleClient_AgentCompletionStream shows how to make a streaming request
func ExampleClient_AgentCompletionStream() {
	type MyPlugin struct {
		plugin.MattermostPlugin
	}

	var p MyPlugin
	llmClient := bridgeclient.NewClient(p.API)

	result, err := llmClient.AgentCompletionStream("gpt4", bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Tell me a story"},
		},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Process events from the stream
	for event := range result.Stream {
		switch event.Type {
		case llm.EventTypeText:
			if text, ok := event.Value.(string); ok {
				fmt.Print(text)
			}
		case llm.EventTypeError:
			fmt.Printf("Error: %v\n", event.Value)
			return
		case llm.EventTypeEnd:
			return
		}
	}
}

// ExampleClient_ServiceCompletion shows how to use a specific LLM service
func ExampleClient_ServiceCompletion() {
	type MyPlugin struct {
		plugin.MattermostPlugin
	}

	var p MyPlugin
	llmClient := bridgeclient.NewClient(p.API)

	response, err := llmClient.ServiceCompletion("openai", bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "system", Message: "You are a helpful assistant"},
			{Role: "user", Message: "What is 2+2?"},
		},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Response: %s\n", response)
}

// ExampleCompletionRequest_withFiles shows how to include files in a request
func ExampleCompletionRequest_withFiles() {
	type MyPlugin struct {
		plugin.MattermostPlugin
	}

	var p MyPlugin
	llmClient := bridgeclient.NewClient(p.API)

	// Example with a file attachment (base64 encoded)
	response, err := llmClient.AgentCompletion("gpt4", bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{
				Role:    "user",
				Message: "What's in this image?",
				Files: []bridgeclient.File{
					{
						ID:       "file123",
						Name:     "image.png",
						MimeType: "image/png",
						Data:     "base64encodeddata...",
					},
				},
			},
		},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Response: %s\n", response)
}

// ExampleCompletionRequest_multiTurn shows a multi-turn conversation
func ExampleCompletionRequest_multiTurn() {
	type MyPlugin struct {
		plugin.MattermostPlugin
	}

	var p MyPlugin
	llmClient := bridgeclient.NewClient(p.API)

	// Multi-turn conversation
	response, err := llmClient.AgentCompletion("gpt4", bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "system", Message: "You are a helpful math tutor"},
			{Role: "user", Message: "What is calculus?"},
			{Role: "assistant", Message: "Calculus is a branch of mathematics..."},
			{Role: "user", Message: "Can you give me an example?"},
		},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Response: %s\n", response)
}

// Example_fullPlugin shows a complete plugin implementation
func Example_fullPlugin() {
	type MyPlugin struct {
		plugin.MattermostPlugin
		llmClient *bridgeclient.Client
	}

	var p MyPlugin

	// OnActivate - create the client
	p.llmClient = bridgeclient.NewClient(p.API)

	// Use the client in a command or other handler
	response, err := p.llmClient.AgentCompletion("gpt4", bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{
				Role:    "system",
				Message: "You are a helpful assistant for my plugin",
			},
			{
				Role:    "user",
				Message: "Help me with this task",
			},
		},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("AI Response: %s\n", response)
}

// ExampleClient_AgentCompletionStream_readAll shows using ReadAll helper
func ExampleClient_AgentCompletionStream_readAll() {
	type MyPlugin struct {
		plugin.MattermostPlugin
	}

	var p MyPlugin
	llmClient := bridgeclient.NewClient(p.API)

	result, err := llmClient.AgentCompletionStream("gpt4", bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "What is 2+2?"},
		},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Use the ReadAll helper to get the complete text
	text, err := result.ReadAll()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Response: %s\n", text)
}

// ExampleClient_ServiceCompletionStream shows service streaming
func ExampleClient_ServiceCompletionStream() {
	type MyPlugin struct {
		plugin.MattermostPlugin
	}

	var p MyPlugin
	llmClient := bridgeclient.NewClient(p.API)

	result, err := llmClient.ServiceCompletionStream("anthropic", bridgeclient.CompletionRequest{
		Posts: []bridgeclient.Post{
			{Role: "user", Message: "Explain quantum computing"},
		},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Accumulate the full response
	var fullText string
	for event := range result.Stream {
		switch event.Type {
		case llm.EventTypeText:
			if text, ok := event.Value.(string); ok {
				fullText += text
			}
		case llm.EventTypeError:
			fmt.Printf("Error: %v\n", event.Value)
			return
		case llm.EventTypeEnd:
			fmt.Printf("Response: %s\n", fullText)
			return
		}
	}
}
