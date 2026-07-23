// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"errors"
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

func TestShouldClaimToolDecisions(t *testing.T) {
	tests := []struct {
		name               string
		conv               *store.Conversation
		executesDelegation bool
		want               bool
	}{
		{name: "parent ask_agent batch", conv: &store.Conversation{Operation: llm.OperationConversation}, executesDelegation: true, want: true},
		{name: "delegated approval batch", conv: &store.Conversation{Operation: llm.OperationDelegation}, want: true},
		{name: "ordinary approval batch", conv: &store.Conversation{Operation: llm.OperationConversation}, want: false},
		{name: "missing conversation", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldClaimToolDecisions(tc.conv, tc.executesDelegation))
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

// turnContentRecordingStore implements conversation.Store's conditional
// content update and panics on anything else — async decision persistence and
// failure finalization must only claim turn content.
type turnContentRecordingStore struct {
	conversation.Store
	claimWins     bool
	claimedTurnID string
	expected      json.RawMessage
	updated       json.RawMessage
}

func (f *turnContentRecordingStore) UpdateTurnContentIfMatches(id string, expected, updated json.RawMessage) (bool, error) {
	f.claimedTurnID = id
	f.expected = expected
	f.updated = updated
	return f.claimWins, nil
}

func TestPersistToolDecisions(t *testing.T) {
	recording := &turnContentRecordingStore{claimWins: true}
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

	originalContent, err := json.Marshal(blocks)
	require.NoError(t, err)

	turn := &store.Turn{ID: "turn-1", Content: originalContent}
	claimedContent, err := c.persistToolDecisions(turn, blocks, []string{"d1"}, func(llm.ToolCall) bool { return true })
	require.NoError(t, err)

	require.Equal(t, "turn-1", recording.claimedTurnID)
	assert.Equal(t, originalContent, []byte(recording.expected), "CAS must compare against the content this request read")
	assert.Equal(t, []byte(recording.updated), []byte(claimedContent))

	var persisted []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(recording.updated, &persisted))
	require.Len(t, persisted, 4)
	assert.Equal(t, conversation.StatusAccepted, persisted[0].Status, "accepted delegation block is claimed")
	assert.Equal(t, conversation.StatusRejected, persisted[1].Status, "the full user decision is persisted before detaching")
	assert.Equal(t, conversation.StatusSuccess, persisted[2].Status, "resolved block is untouched")
	assert.Equal(t, conversation.StatusAccepted, persisted[3].Status, "auto-resumed block is claimed")

	// The in-memory snapshot must remain pending: the asynchronous
	// continuation of this request executes from it.
	assert.Equal(t, conversation.StatusPending, blocks[0].Status)
	assert.Equal(t, conversation.StatusPending, blocks[3].Status)
}

func TestPersistToolDecisionsLostClaim(t *testing.T) {
	recording := &turnContentRecordingStore{claimWins: false}
	c := &Conversations{convService: conversation.NewService(recording, nil, nil, nil)}

	blocks := []conversation.ContentBlock{delegationBlock("d1", conversation.StatusPending, false)}
	content, err := json.Marshal(blocks)
	require.NoError(t, err)

	_, err = c.persistToolDecisions(&store.Turn{ID: "turn-1", Content: content}, blocks, []string{"d1"}, func(llm.ToolCall) bool { return false })
	require.ErrorIs(t, err, ErrStaleToolClick, "losing the claim must surface as a stale click, never a second execution")
}

func TestPersistToolDecisionsNoChanges(t *testing.T) {
	recording := &turnContentRecordingStore{claimWins: true}
	c := &Conversations{convService: conversation.NewService(recording, nil, nil, nil)}

	blocks := []conversation.ContentBlock{plainBlock("p1", conversation.StatusSuccess)}
	claimedContent, err := c.persistToolDecisions(&store.Turn{ID: "turn-1"}, blocks, []string{"p1"}, func(llm.ToolCall) bool { return false })
	require.NoError(t, err)
	assert.Nil(t, claimedContent)
	assert.Empty(t, recording.claimedTurnID, "nothing to claim means nothing is written")
}

type recordingDelegationNotifier struct {
	conversationIDs []string
}

func (n *recordingDelegationNotifier) SubTurnCompleted(conversationID string) {
	n.conversationIDs = append(n.conversationIDs, conversationID)
}

func TestHandleClaimedToolBatchFailure(t *testing.T) {
	recording := &turnContentRecordingStore{claimWins: true}
	notifier := &recordingDelegationNotifier{}
	c := &Conversations{
		convService:        conversation.NewService(recording, nil, nil, nil),
		delegationNotifier: notifier,
	}

	claimedContent, err := json.Marshal([]conversation.ContentBlock{
		delegationBlock("d1", conversation.StatusAccepted, false),
		plainBlock("p1", conversation.StatusRejected),
	})
	require.NoError(t, err)

	conv := &store.Conversation{ID: "conv-1", Operation: llm.OperationDelegation}
	c.handleClaimedToolBatchFailure(conv, "turn-1", claimedContent, nil, errors.New("persistence failed"))

	assert.Equal(t, claimedContent, []byte(recording.expected), "failure finalization only applies to the claimed snapshot")
	var finalized []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(recording.updated, &finalized))
	require.Len(t, finalized, 2)
	assert.Equal(t, conversation.StatusError, finalized[0].Status, "accepted work becomes terminal instead of waiting forever")
	assert.Equal(t, conversation.StatusRejected, finalized[1].Status, "rejected decisions remain intact")
	assert.Equal(t, []string{"conv-1"}, notifier.conversationIDs, "the waiting parent is always woken")
}

func TestMaxToolTurnsForConversation(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		conv       *store.Conversation
		want       int
	}{
		{name: "ordinary conversation keeps configured limit", configured: 30, conv: &store.Conversation{Operation: llm.OperationConversation}, want: 30},
		{name: "delegation uses lower configured limit", configured: 5, conv: &store.Conversation{Operation: llm.OperationDelegation}, want: 5},
		{name: "delegation caps higher configured limit", configured: 30, conv: &store.Conversation{Operation: llm.OperationDelegation}, want: DelegationMaxToolTurns},
		{name: "nil conversation keeps configured limit", configured: 30, want: 30},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, maxToolTurnsForConversation(tc.configured, tc.conv))
		})
	}
}
