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

// TestBuildCompletionRequest_PopulatesComposition pins the assembly-side
// contract that every piece of content reaching the LLM is tagged with its
// source. ComputeComposition can then attribute the provider's authoritative
// token total back to those tags without making us trust the heuristic
// estimator's absolute numbers.
func TestBuildCompletionRequest_PopulatesComposition(t *testing.T) {
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

	// User-1 turn carries both an image and a text file.
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

	// Build the request with a Context that exposes two tool definitions.
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
	for _, in := range req.Composition {
		bySource[in.Source] = append(bySource[in.Source], in)
	}

	t.Run("system prompt tagged", func(t *testing.T) {
		require.Len(t, bySource[llm.SourceSystem], 1)
		assert.Equal(t, "you are helpful", bySource[llm.SourceSystem][0].Text)
	})

	t.Run("history captures user + assistant text", func(t *testing.T) {
		// "have a look" (user) + "let me check" (assistant) → at least 2 entries.
		hist := bySource[llm.SourceHistory]
		require.GreaterOrEqual(t, len(hist), 2)
		var combined string
		for _, h := range hist {
			combined += h.Text + "\n"
		}
		assert.Contains(t, combined, "have a look")
		assert.Contains(t, combined, "let me check")
	})

	t.Run("file attachment tagged with id and name", func(t *testing.T) {
		require.Len(t, bySource[llm.SourceAttachment], 1)
		att := bySource[llm.SourceAttachment][0]
		assert.Equal(t, "doc1", att.ID)
		assert.Equal(t, "notes.txt", att.Name)
		assert.Contains(t, att.Text, "hello world",
			"attachment composition input must carry the actual text that flowed into the post so proportions reflect file size")
	})

	t.Run("image attachment tagged with id and name", func(t *testing.T) {
		require.Len(t, bySource[llm.SourceImage], 1)
		img := bySource[llm.SourceImage][0]
		assert.Equal(t, "img1", img.ID)
		assert.Equal(t, "diagram.png", img.Name)
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
		defs := bySource[llm.SourceToolDefs]
		require.NotEmpty(t, defs)
		var combined string
		for _, d := range defs {
			combined += d.Text + " "
		}
		assert.Contains(t, combined, "get_weather")
		assert.Contains(t, combined, "get_time")
	})
}

// TestBuildCompletionRequest_PopulatesComposition_NoTools makes sure assembly
// doesn't crash when Context.Tools is nil and emits no tool_defs entries.
func TestBuildCompletionRequest_PopulatesComposition_NoTools(t *testing.T) {
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

	for _, in := range req.Composition {
		assert.NotEqual(t, llm.SourceToolDefs, in.Source,
			"no Context.Tools ⇒ no tool_defs composition entries")
	}
}
