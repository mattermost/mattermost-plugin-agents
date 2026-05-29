// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildCompletionRequestComposition pins that the derived breakdown sees
// every kind of content the assembled request carries: system prompt, history
// (including folded-in attachment text), tool results, images, and tool defs.
// ComputeComposition can then attribute the provider's authoritative token
// total back to those sources.
func TestBuildCompletionRequestComposition(t *testing.T) {
	mmClient := mmapimocks.NewMockClient(t)

	mmClient.On("GetFileInfo", "img1").Return(&model.FileInfo{
		Id: "img1", Name: "diagram.png", MimeType: "image/png", Size: 100,
	}, nil)
	mmClient.On("GetFile", "img1").Return(io.NopCloser(strings.NewReader("PNGDATA")), nil)
	mmClient.On("GetFileInfo", "doc1").Return(&model.FileInfo{
		Id: "doc1", Name: "notes.txt", MimeType: "text/plain", Size: 5,
	}, nil)
	mmClient.On("GetFile", "doc1").Return(io.NopCloser(strings.NewReader("hello world")), nil)

	botID := model.NewId()
	userID := model.NewId()
	bots := &testBotLookup{
		botUserIDs: map[string]bool{},
		configByID: map[string]testBotConfig{
			botID: {enableVision: true, maxFileSize: 0},
		},
	}

	svc, s := setupTestServiceWithClient(t, mmClient, bots)

	// User turn carries both an image and a text file.
	res, err := svc.CreateConversation(CreateConversationParams{
		UserID:       userID,
		BotID:        botID,
		Operation:    "conversation",
		SystemPrompt: "you are helpful",
		UserMessage:  "have a look",
		FileIDs:      []string{"img1", "doc1"},
	})
	require.NoError(t, err)

	// Assistant turn that called a tool, paired with a tool_result.
	assistantBlocks := []ContentBlock{
		{Type: BlockTypeText, Text: "let me check"},
		{
			Type: BlockTypeToolUse, ID: "tc1", Name: "get_weather",
			Input: json.RawMessage(`{"city":"NYC"}`), Status: StatusSuccess, Shared: BoolPtr(true),
		},
	}
	assistantContent, err := json.Marshal(assistantBlocks)
	require.NoError(t, err)
	require.NoError(t, s.CreateTurn(&store.Turn{
		ID: model.NewId(), ConversationID: res.ConversationID, Role: "assistant",
		Content: assistantContent, Sequence: 2, CreatedAt: model.GetMillis(),
	}))

	resultBlocks := []ContentBlock{
		{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "72F, sunny", Status: StatusSuccess, Shared: BoolPtr(true)},
	}
	resultContent, err := json.Marshal(resultBlocks)
	require.NoError(t, err)
	require.NoError(t, s.CreateTurn(&store.Turn{
		ID: model.NewId(), ConversationID: res.ConversationID, Role: "tool_result",
		Content: resultContent, Sequence: 3, CreatedAt: model.GetMillis(),
	}))

	conv, err := s.GetConversation(res.ConversationID)
	require.NoError(t, err)

	tools := llm.NewToolStore()
	tools.AddTools([]llm.Tool{
		{Name: "get_weather", Description: "Returns weather for a city", Schema: &jsonschema.Schema{}},
		{Name: "get_time", Description: "Returns current time", Schema: &jsonschema.Schema{}},
	})

	req, err := svc.BuildCompletionRequest(conv, &llm.Context{Tools: tools})
	require.NoError(t, err)

	bySource := map[llm.CompositionSource][]llm.CompositionInput{}
	for _, in := range req.Composition() {
		bySource[in.Source] = append(bySource[in.Source], in)
	}

	t.Run("system prompt tagged", func(t *testing.T) {
		require.Len(t, bySource[llm.SourceSystem], 1)
		assert.Equal(t, "you are helpful", bySource[llm.SourceSystem][0].Text)
	})

	t.Run("history captures user, assistant, and folded attachment text", func(t *testing.T) {
		var combined string
		for _, h := range bySource[llm.SourceHistory] {
			combined += h.Text + "\n"
		}
		assert.Contains(t, combined, "have a look")
		assert.Contains(t, combined, "let me check")
		assert.Contains(t, combined, "hello world",
			"text-file content is folded into the user message, so it counts as history")
	})

	t.Run("image tagged", func(t *testing.T) {
		require.Len(t, bySource[llm.SourceImage], 1)
	})

	t.Run("tool result content tagged", func(t *testing.T) {
		require.NotEmpty(t, bySource[llm.SourceToolResults])
		var combined string
		for _, r := range bySource[llm.SourceToolResults] {
			combined += r.Text + "\n"
		}
		assert.Contains(t, combined, "72F, sunny")
	})

	t.Run("tool definitions tagged from context", func(t *testing.T) {
		var combined string
		for _, d := range bySource[llm.SourceToolDefs] {
			combined += d.Text + " "
		}
		assert.Contains(t, combined, "get_weather")
		assert.Contains(t, combined, "get_time")
	})
}

// TestBuildCompletionRequestComposition_NoTools makes sure the derive path
// emits no tool_defs entries when Context.Tools is nil.
func TestBuildCompletionRequestComposition_NoTools(t *testing.T) {
	svc, _ := setupTestService(t)

	res, err := svc.CreateConversation(CreateConversationParams{
		UserID:       model.NewId(),
		BotID:        model.NewId(),
		Operation:    "conversation",
		SystemPrompt: "sys",
		UserMessage:  "hi",
	})
	require.NoError(t, err)

	conv, err := svc.GetConversation(res.ConversationID)
	require.NoError(t, err)

	req, err := svc.BuildCompletionRequest(conv, &llm.Context{})
	require.NoError(t, err)

	for _, in := range req.Composition() {
		assert.NotEqual(t, llm.SourceToolDefs, in.Source,
			"no Context.Tools ⇒ no tool_defs composition entries")
	}
}
