// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// toolNamesOnWire returns the tool names of a captured request body in the
// order they were sent, for the chat, Responses and Anthropic formats.
func toolNamesOnWire(body map[string]any) []string {
	tools, _ := body["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if fn, ok := tool["function"].(map[string]any); ok {
			names = append(names, fn["name"].(string))
			continue
		}
		if name, ok := tool["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// sendWithTools sends one request with the given tools through the production
// client for svc, which rejects it so a local fallback answers, and returns
// the body svc received.
func sendWithTools(t *testing.T, svc llm.ServiceConfig, bot llm.BotConfig, tools []llm.Tool) map[string]any {
	t.Helper()
	store := llm.NewToolStore()
	store.AddTools(tools)
	reqCtx := llm.NewContext()
	reqCtx.Tools = store

	client, captures := newCaptureChain(t, []captureHop{{svc: svc}, localHop}, bot)
	_, err := client.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
		Context: reqCtx,
	})
	require.NoError(t, err)
	body := loadCaptures(captures)[0]
	require.NotNil(t, body, "the service must have been called")
	return body
}

// TestToolsAreSentInStableOrder checks that every request lists its tools in
// the same order. Tools lead the prompt prefix, so an order that changes
// between requests defeats prompt caching on every round of a tool loop.
func TestToolsAreSentInStableOrder(t *testing.T) {
	tools := make([]llm.Tool, 16)
	want := make([]string, len(tools))
	for i := range tools {
		name := fmt.Sprintf("tool_%02d", len(tools)-i)
		tools[i] = llm.Tool{Name: name, Description: "does " + name, Schema: &jsonschema.Schema{Type: "object"}}
		want[i] = name
	}
	slices.Sort(want)

	tests := []struct {
		name string
		svc  llm.ServiceConfig
	}{
		{name: "OpenAI chat completions", svc: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-6.1-terra"}},
		{name: "OpenAI Responses", svc: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-6.1-terra", UseResponsesAPI: true}},
		{name: "Anthropic", svc: llm.ServiceConfig{Type: llm.ServiceTypeAnthropic, DefaultModel: "claude-sonnet-5-5"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for range 3 {
				body := sendWithTools(t, tc.svc, llm.BotConfig{}, tools)
				assert.Equal(t, want, toolNamesOnWire(body))
			}
		})
	}
}
