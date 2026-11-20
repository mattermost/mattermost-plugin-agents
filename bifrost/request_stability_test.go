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
func sendWithTools(t *testing.T, svc llm.ServiceConfig, bot llm.BotConfig, tools []llm.Tool, opts ...llm.LanguageModelOption) map[string]any {
	t.Helper()
	store := llm.NewToolStore()
	store.AddTools(tools)
	reqCtx := llm.NewContext()
	reqCtx.Tools = store

	client, captures := newCaptureChain(t, []captureHop{{svc: svc}, localHop}, bot)
	_, err := client.ChatCompletionNoStream(context.Background(), llm.CompletionRequest{
		Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
		Context: reqCtx,
	}, opts...)
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

// TestServiceTierIsSentToOpenAIOnly checks that a requested processing tier
// reaches OpenAI hops and is left off providers whose tiers differ.
func TestServiceTierIsSentToOpenAIOnly(t *testing.T) {
	tests := []struct {
		name     string
		svc      llm.ServiceConfig
		wantTier any
	}{
		{name: "OpenAI chat completions", svc: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-6.1-terra"}, wantTier: "flex"},
		{name: "OpenAI Responses", svc: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-6.1-terra", UseResponsesAPI: true}, wantTier: "flex"},
		{name: "Anthropic", svc: llm.ServiceConfig{Type: llm.ServiceTypeAnthropic, DefaultModel: "claude-sonnet-5-5"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := sendWithTools(t, tc.svc, llm.BotConfig{}, nil, llm.WithServiceTier("flex"))
			assert.Equal(t, tc.wantTier, body["service_tier"])
		})
	}
}

// TestReasoningOffIsSentToOpenAI checks that turning reasoning off reaches
// OpenAI reasoning models, which otherwise reason at their default effort and
// can spend a small output budget before writing any text.
func TestReasoningOffIsSentToOpenAI(t *testing.T) {
	effortOnWire := func(body map[string]any) any {
		if effort, ok := body["reasoning_effort"]; ok {
			return effort
		}
		if reasoning, ok := body["reasoning"].(map[string]any); ok {
			return reasoning["effort"]
		}
		return nil
	}
	tests := []struct {
		name       string
		svc        llm.ServiceConfig
		wantEffort any
	}{
		{name: "chat completions model that can turn reasoning off", svc: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-6-luna"}, wantEffort: "none"},
		{name: "Responses model that can turn reasoning off", svc: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-6-luna", UseResponsesAPI: true}, wantEffort: "none"},
		{name: "Responses model that always reasons gets its lowest effort", svc: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-6.1-terra", UseResponsesAPI: true}, wantEffort: "low"},
		{name: "model that does not reason gets no effort", svc: llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-4o"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := sendWithTools(t, tc.svc, llm.BotConfig{}, nil, llm.WithReasoningDisabled())
			assert.Equal(t, tc.wantEffort, effortOnWire(body))
		})
	}
}
