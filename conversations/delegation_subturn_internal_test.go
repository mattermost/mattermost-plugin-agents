// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeepNonDelegationTool(t *testing.T) {
	tests := []struct {
		name string
		tool llm.Tool
		keep bool
	}{
		{
			name: "embedded ask_agent is dropped",
			tool: llm.Tool{Name: "mattermost__ask_agent", ServerOrigin: mcp.EmbeddedClientKey},
			keep: false,
		},
		{
			name: "embedded ask_agent bare name is dropped",
			tool: llm.Tool{Name: "ask_agent", ServerOrigin: mcp.EmbeddedClientKey},
			keep: false,
		},
		{
			name: "embedded ask_agent with trailing-slash origin is dropped",
			tool: llm.Tool{Name: "mattermost__ask_agent", ServerOrigin: mcp.EmbeddedClientKey + "/"},
			keep: false,
		},
		{
			name: "other embedded tools are kept",
			tool: llm.Tool{Name: "mattermost__search_posts", ServerOrigin: mcp.EmbeddedClientKey},
			keep: true,
		},
		{
			name: "remote tool that happens to be named ask_agent is kept",
			tool: llm.Tool{Name: "other__ask_agent", ServerOrigin: "https://other.example.com/mcp"},
			keep: true,
		},
		{
			name: "built-in tools are kept",
			tool: llm.Tool{Name: "WebSearch"},
			keep: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.keep, keepNonDelegationTool(tc.tool))
		})
	}
}

func TestAnyUnresolvedToolCall(t *testing.T) {
	tests := []struct {
		name  string
		calls []llm.ToolCall
		want  bool
	}{
		{name: "empty batch", calls: nil, want: false},
		{
			name:  "pending call",
			calls: []llm.ToolCall{{Status: llm.ToolCallStatusPending}},
			want:  true,
		},
		{
			name:  "accepted call still unresolved",
			calls: []llm.ToolCall{{Status: llm.ToolCallStatusAccepted}},
			want:  true,
		},
		{
			name: "all terminal statuses resolved",
			calls: []llm.ToolCall{
				{Status: llm.ToolCallStatusSuccess},
				{Status: llm.ToolCallStatusError},
				{Status: llm.ToolCallStatusAutoApproved},
				{Status: llm.ToolCallStatusRejected},
			},
			want: false,
		},
		{
			name: "mixed batch with one pending",
			calls: []llm.ToolCall{
				{Status: llm.ToolCallStatusSuccess},
				{Status: llm.ToolCallStatusPending},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, anyUnresolvedToolCall(tc.calls))
		})
	}
}

func TestDelegationStreamObserverPendingDetection(t *testing.T) {
	tests := []struct {
		name   string
		events []llm.TextStreamEvent
		want   bool
	}{
		{
			name: "plain text stream is not pending",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeText, Value: "answer"},
				{Type: llm.EventTypeEnd},
			},
			want: false,
		},
		{
			name: "pending then resolved is not pending",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{Status: llm.ToolCallStatusPending}}},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{Status: llm.ToolCallStatusAutoApproved}}},
				{Type: llm.EventTypeText, Value: "answer"},
			},
			want: false,
		},
		{
			name: "stream ending on pending approval is pending",
			events: []llm.TextStreamEvent{
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{Status: llm.ToolCallStatusAutoApproved}}},
				{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{Status: llm.ToolCallStatusPending}}},
				{Type: llm.EventTypeEnd},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			forwarded := 0
			observer := &delegationStreamObserver{onEvent: func(llm.TextStreamEvent) { forwarded++ }}
			for _, ev := range tc.events {
				observer.observe(ev)
			}
			assert.Equal(t, tc.want, observer.endedPending())
			assert.Equal(t, len(tc.events), forwarded)
		})
	}
}

func TestTeeTextStreamForwardsAllEvents(t *testing.T) {
	in := make(chan llm.TextStreamEvent, 3)
	in <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "a"}
	in <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "b"}
	in <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	close(in)

	var observed []llm.TextStreamEvent
	out := teeTextStream(&llm.TextStreamResult{Stream: in}, func(ev llm.TextStreamEvent) {
		observed = append(observed, ev)
	})

	var received []llm.TextStreamEvent
	for ev := range out.Stream {
		received = append(received, ev)
	}

	require.Len(t, received, 3)
	require.Len(t, observed, 3)
	assert.Equal(t, received, observed)
}

func delegationBlock(id, status string, wouldAutoExecute bool) conversation.ContentBlock {
	return conversation.ContentBlock{
		Type:             conversation.BlockTypeToolUse,
		ID:               id,
		Name:             "mattermost__ask_agent",
		MCPBareName:      "ask_agent",
		ServerOrigin:     mcp.EmbeddedClientKey,
		Status:           status,
		WouldAutoExecute: wouldAutoExecute,
	}
}

func plainBlock(id, status string) conversation.ContentBlock {
	return conversation.ContentBlock{
		Type:         conversation.BlockTypeToolUse,
		ID:           id,
		Name:         "mattermost__search_posts",
		MCPBareName:  "search_posts",
		ServerOrigin: mcp.EmbeddedClientKey,
		Status:       status,
	}
}

func TestBatchWillExecuteDelegationCall(t *testing.T) {
	autoExecYes := func(llm.ToolCall) bool { return true }
	autoExecNo := func(llm.ToolCall) bool { return false }

	tests := []struct {
		name     string
		blocks   []conversation.ContentBlock
		accepted []string
		autoExec func(llm.ToolCall) bool
		want     bool
	}{
		{
			name:     "accepted delegation call",
			blocks:   []conversation.ContentBlock{delegationBlock("d1", conversation.StatusPending, false)},
			accepted: []string{"d1"},
			autoExec: autoExecNo,
			want:     true,
		},
		{
			name:     "rejected delegation call does not execute",
			blocks:   []conversation.ContentBlock{delegationBlock("d1", conversation.StatusPending, false)},
			accepted: nil,
			autoExec: autoExecNo,
			want:     false,
		},
		{
			name:     "would-auto-execute delegation resumes with policy",
			blocks:   []conversation.ContentBlock{delegationBlock("d1", conversation.StatusPending, true)},
			accepted: nil,
			autoExec: autoExecYes,
			want:     true,
		},
		{
			name:     "would-auto-execute without policy stays off",
			blocks:   []conversation.ContentBlock{delegationBlock("d1", conversation.StatusPending, true)},
			accepted: nil,
			autoExec: autoExecNo,
			want:     false,
		},
		{
			name:     "non-delegation batch",
			blocks:   []conversation.ContentBlock{plainBlock("p1", conversation.StatusPending)},
			accepted: []string{"p1"},
			autoExec: autoExecYes,
			want:     false,
		},
		{
			name: "mixed batch with accepted delegation",
			blocks: []conversation.ContentBlock{
				plainBlock("p1", conversation.StatusPending),
				delegationBlock("d1", conversation.StatusPending, false),
			},
			accepted: []string{"p1", "d1"},
			autoExec: autoExecNo,
			want:     true,
		},
		{
			name:     "already resolved delegation block is ignored",
			blocks:   []conversation.ContentBlock{delegationBlock("d1", conversation.StatusSuccess, false)},
			accepted: []string{"d1"},
			autoExec: autoExecYes,
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, batchWillExecuteDelegationCall(tc.blocks, tc.accepted, tc.autoExec))
		})
	}
}

func TestIsDelegationToolUseBlock(t *testing.T) {
	tests := []struct {
		name  string
		block conversation.ContentBlock
		want  bool
	}{
		{name: "embedded ask_agent by bare name", block: delegationBlock("d1", conversation.StatusPending, false), want: true},
		{
			name: "embedded ask_agent without bare name metadata",
			block: conversation.ContentBlock{
				Type: conversation.BlockTypeToolUse, ID: "d1",
				Name: "mattermost__ask_agent", ServerOrigin: mcp.EmbeddedClientKey,
				Status: conversation.StatusPending,
			},
			want: true,
		},
		{name: "other embedded tool", block: plainBlock("p1", conversation.StatusPending), want: false},
		{
			name: "remote tool named ask_agent",
			block: conversation.ContentBlock{
				Type: conversation.BlockTypeToolUse, ID: "r1",
				Name: "other__ask_agent", ServerOrigin: "https://other.example.com/mcp",
				Status: conversation.StatusPending,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isDelegationToolUseBlock(tc.block))
		})
	}
}

// turnContentRecordingStore implements conversation.Store's UpdateTurnContent
// and panics on anything else — persistAcceptedToolDecisions must only write
// turn content.
type turnContentRecordingStore struct {
	conversation.Store
	updatedTurnID string
	updated       json.RawMessage
}

func (f *turnContentRecordingStore) UpdateTurnContent(id string, content json.RawMessage) error {
	f.updatedTurnID = id
	f.updated = content
	return nil
}

func TestPersistAcceptedToolDecisions(t *testing.T) {
	recording := &turnContentRecordingStore{}
	c := &Conversations{convService: conversation.NewService(recording, nil, nil, nil)}

	blocks := []conversation.ContentBlock{
		delegationBlock("d1", conversation.StatusPending, false),
		plainBlock("p1", conversation.StatusPending),
		plainBlock("p2", conversation.StatusSuccess),
	}
	// p3 passed the auto-exec policy and was paused with the batch: it will
	// execute on resume, so it must be claimed too.
	autoResumed := plainBlock("p3", conversation.StatusPending)
	autoResumed.WouldAutoExecute = true
	blocks = append(blocks, autoResumed)

	turn := &store.Turn{ID: "turn-1"}
	err := c.persistAcceptedToolDecisions(turn, blocks, []string{"d1"}, func(llm.ToolCall) bool { return true })
	require.NoError(t, err)

	require.Equal(t, "turn-1", recording.updatedTurnID)
	var persisted []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(recording.updated, &persisted))
	require.Len(t, persisted, 4)
	assert.Equal(t, conversation.StatusAccepted, persisted[0].Status, "accepted delegation block is claimed")
	assert.Equal(t, conversation.StatusPending, persisted[1].Status, "undecided block stays pending")
	assert.Equal(t, conversation.StatusSuccess, persisted[2].Status, "resolved block is untouched")
	assert.Equal(t, conversation.StatusAccepted, persisted[3].Status, "auto-resumed block is claimed")

	// The in-memory snapshot must remain pending: the asynchronous
	// continuation of this request executes from it.
	assert.Equal(t, conversation.StatusPending, blocks[0].Status)
	assert.Equal(t, conversation.StatusPending, blocks[3].Status)
}

func TestPersistAcceptedToolDecisionsNoChanges(t *testing.T) {
	recording := &turnContentRecordingStore{}
	c := &Conversations{convService: conversation.NewService(recording, nil, nil, nil)}

	blocks := []conversation.ContentBlock{plainBlock("p1", conversation.StatusSuccess)}
	err := c.persistAcceptedToolDecisions(&store.Turn{ID: "turn-1"}, blocks, []string{"p1"}, func(llm.ToolCall) bool { return false })
	require.NoError(t, err)
	assert.Empty(t, recording.updatedTurnID, "nothing to claim means nothing is written")
}
