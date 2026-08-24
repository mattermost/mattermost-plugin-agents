// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// TestServerToolActivityRecord covers what the model is told about work it
// already did. The provider drops its own server-tool results from every
// request after the one that ran them, so this record is the model's only
// memory of them on later rounds.
func TestServerToolActivityRecord(t *testing.T) {
	tests := []struct {
		name     string
		uses     []llm.ServerToolUse
		contains []string
		absent   []string
		empty    bool
	}{
		{
			name:   "no activity replays nothing",
			uses:   nil,
			absent: []string{serverToolReplayHeader},
		},
		{
			name: "code execution reports command and output",
			uses: []llm.ServerToolUse{{
				ID:      "srvtoolu_1",
				Tool:    llm.NativeToolCodeInterpreter,
				SubTool: "bash",
				Status:  llm.ServerToolStatusSuccess,
				Command: "python make_report.py",
				Output:  "wrote report.csv",
			}},
			contains: []string{
				serverToolReplayHeader,
				"code_interpreter (bash)",
				"python make_report.py",
				"wrote report.csv",
			},
		},
		{
			// The model cannot see provider file ids, and attachment may
			// still fail after capture, so replay must not claim success.
			name: "captured output files are reported as pending attachment",
			uses: []llm.ServerToolUse{{
				ID:      "srvtoolu_2",
				Tool:    llm.NativeToolCodeInterpreter,
				Status:  llm.ServerToolStatusSuccess,
				FileIDs: []string{"file_1", "file_2"},
			}},
			contains: []string{"2 output file(s) were captured for attachment to the reply"},
			absent:   []string{"file_1", "file_2"},
		},
		{
			name: "failed execution reports its error code",
			uses: []llm.ServerToolUse{{
				ID:        "srvtoolu_3",
				Tool:      llm.NativeToolCodeInterpreter,
				Status:    llm.ServerToolStatusError,
				ErrorCode: "execution_time_exceeded",
			}},
			contains: []string{"error=execution_time_exceeded"},
		},
		{
			name: "web search and fetch report their targets",
			uses: []llm.ServerToolUse{
				{ID: "s1", Tool: llm.NativeToolWebSearch, Query: "mattermost plugins"},
				{ID: "s2", Tool: llm.NativeToolWebFetch, URL: "https://example.com", Title: "Example"},
			},
			contains: []string{"mattermost plugins", "https://example.com", "Example"},
		},
		{
			name:  "an entry with no tool produces no replay record",
			uses:  []llm.ServerToolUse{{ID: "s1", Status: llm.ServerToolStatusSuccess}},
			empty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := serverToolActivityRecord(tt.uses)
			if tt.empty {
				assert.Empty(t, record)
			}
			for _, want := range tt.contains {
				assert.Contains(t, record, want)
			}
			for _, unwanted := range tt.absent {
				assert.NotContains(t, record, unwanted)
			}
		})
	}
}

// TestServerToolActivityReplayedInRequest pins the wiring end to end: an
// assistant post carrying server-tool activity must preserve its position
// relative to the assistant text around it.
func TestServerToolActivityReplayedInRequest(t *testing.T) {
	llmClient, err := New(Config{
		ProviderSettings: ProviderSettings{
			Provider:         schemas.Anthropic,
			APIKey:           "test-key",
			DefaultModel:     "claude-sonnet-4-6",
			StreamingTimeout: 10 * time.Second,
		},
	})
	require.NoError(t, err)
	defer llmClient.Shutdown()

	request := llm.CompletionRequest{Posts: []llm.Post{
		{Role: llm.PostRoleUser, Message: "make me a chart"},
		{
			Role:    llm.PostRoleBot,
			Message: "I'll make it. Here is the chart.",
			ServerTools: []llm.ServerToolUse{{
				ID:      "srvtoolu_1",
				Tool:    llm.NativeToolCodeInterpreter,
				SubTool: "bash",
				Status:  llm.ServerToolStatusSuccess,
				Command: "python chart.py",
				FileIDs: []string{"file_1"},
			}},
			AssistantSegments: []llm.TurnSegment{
				{Kind: llm.TurnSegmentText, Text: "I'll make it. "},
				{Kind: llm.TurnSegmentServerTool, ServerToolID: "srvtoolu_1"},
				{Kind: llm.TurnSegmentText, Text: "Here is the chart."},
			},
		},
		{Role: llm.PostRoleUser, Message: "now make it blue"},
	}}

	messages := llmClient.convertToResponsesMessages(request.Posts)

	var texts []string
	for _, msg := range messages {
		if msg.Content != nil && msg.Content.ContentStr != nil {
			texts = append(texts, *msg.Content.ContentStr)
		}
	}

	require.Len(t, texts, 5, "the activity record is an extra assistant message")
	assert.Equal(t, "make me a chart", texts[0])
	assert.Equal(t, "I'll make it. ", texts[1])
	assert.Contains(t, texts[2], "python chart.py")
	assert.Contains(t, texts[2], "for attachment to the reply")
	assert.Equal(t, "Here is the chart.", texts[3])
	assert.Equal(t, "now make it blue", texts[4])
}

// TestServerToolActivityNotReplayedWhenAbsent pins that ordinary turns are
// unchanged — no stray assistant message when there was no server activity.
func TestServerToolActivityNotReplayedWhenAbsent(t *testing.T) {
	llmClient, err := New(Config{
		ProviderSettings: ProviderSettings{
			Provider:         schemas.Anthropic,
			APIKey:           "test-key",
			DefaultModel:     "claude-sonnet-4-6",
			StreamingTimeout: 10 * time.Second,
		},
	})
	require.NoError(t, err)
	defer llmClient.Shutdown()

	messages := llmClient.convertToResponsesMessages([]llm.Post{
		{Role: llm.PostRoleUser, Message: "hi"},
		{Role: llm.PostRoleBot, Message: "hello"},
	})
	require.Len(t, messages, 2)
}
