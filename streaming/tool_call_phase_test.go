// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedLLM replays one canned response per ChatCompletion call.
type scriptedLLM struct {
	mu     sync.Mutex
	rounds [][]llm.TextStreamEvent
}

func (s *scriptedLLM) ChatCompletion(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := []llm.TextStreamEvent{{Type: llm.EventTypeText, Value: "done"}, {Type: llm.EventTypeEnd}}
	if len(s.rounds) > 0 {
		events, s.rounds = s.rounds[0], s.rounds[1:]
	}
	ch := make(chan llm.TextStreamEvent, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return &llm.TextStreamResult{Stream: ch}, nil
}

func (s *scriptedLLM) ChatCompletionNoStream(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (string, error) {
	return "", nil
}

func (s *scriptedLLM) CountTokens(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}

func (s *scriptedLLM) InputTokenLimit() int  { return 100000 }
func (s *scriptedLLM) OutputTokenLimit() int { return 8192 }

func modelRequestsTools(names ...string) []llm.TextStreamEvent {
	calls := make([]llm.ToolCall, len(names))
	for i, name := range names {
		calls[i] = llm.ToolCall{ID: name + "-call", Name: name, Arguments: json.RawMessage(`{}`)}
	}
	return []llm.TextStreamEvent{
		{Type: llm.EventTypeText, Value: "Working on it."},
		{Type: llm.EventTypeToolCalls, Value: calls},
		{Type: llm.EventTypeEnd},
	}
}

// Every shape of tool round the runner produces must reach the webapp in an
// order it can render without guessing: executing before resolved (so
// spinners show while tools run), nothing stale after a round resolves, and a
// batch awaiting a decision only once its turn is persisted, before the end
// event.
func TestToolRunnerBatchesReachWebappInPhaseOrder(t *testing.T) {
	const (
		postID         = "post-id"
		channelID      = "channel-id"
		botID          = "bot-id"
		requesterID    = "requester-id"
		conversationID = "conv-id"
	)

	tests := []struct {
		name       string
		rounds     [][]llm.TextStreamEvent
		wantPhases []llm.ToolCallPhase
	}{
		{
			name:       "auto-run batch",
			rounds:     [][]llm.TextStreamEvent{modelRequestsTools("auto_tool")},
			wantPhases: []llm.ToolCallPhase{llm.ToolCallPhaseExecuting, llm.ToolCallPhaseResolved},
		},
		{
			name:   "two auto-run rounds",
			rounds: [][]llm.TextStreamEvent{modelRequestsTools("auto_tool"), modelRequestsTools("auto_tool")},
			wantPhases: []llm.ToolCallPhase{
				llm.ToolCallPhaseExecuting, llm.ToolCallPhaseResolved,
				llm.ToolCallPhaseExecuting, llm.ToolCallPhaseResolved,
			},
		},
		{
			name:       "mixed batch awaits a decision",
			rounds:     [][]llm.TextStreamEvent{modelRequestsTools("auto_tool", "manual_tool")},
			wantPhases: []llm.ToolCallPhase{llm.ToolCallPhaseAwaitingDecision},
		},
		{
			name:       "approval-only batch awaits a decision",
			rounds:     [][]llm.TextStreamEvent{modelRequestsTools("manual_tool")},
			wantPhases: []llm.ToolCallPhase{llm.ToolCallPhaseAwaitingDecision},
		},
		{
			name:   "auto-run round followed by a batch awaiting a decision",
			rounds: [][]llm.TextStreamEvent{modelRequestsTools("auto_tool"), modelRequestsTools("manual_tool")},
			wantPhases: []llm.ToolCallPhase{
				llm.ToolCallPhaseExecuting, llm.ToolCallPhaseResolved,
				llm.ToolCallPhaseAwaitingDecision,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeStreamingClient{
				channels: map[string]*model.Channel{
					channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
				},
			}
			ts := &eventCountingTurnStore{client: client, eventsAtCreate: -1}
			service := NewMMPostStreamService(client, i18n.Init())
			service.SetTurnStore(ts)

			post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
			post.AddProp(ConversationIDProp, conversationID)

			tools := llm.NewToolStore()
			resolver := func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) { return "ok", nil }
			tools.AddTools([]llm.Tool{
				{Name: "auto_tool", Resolver: resolver},
				{Name: "manual_tool", Resolver: resolver},
			})

			run, err := toolrunner.New(&scriptedLLM{rounds: tt.rounds}).Run(context.Background(), llm.CompletionRequest{
				Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "go"}},
				Context: &llm.Context{Tools: tools},
			}, func(tc llm.ToolCall) bool { return tc.Name == "auto_tool" }, nil)
			require.NoError(t, err)

			service.StreamToPost(context.Background(), run.Stream, post, "en", requesterID)

			require.GreaterOrEqual(t, ts.eventsAtCreate, 0, "turn was never persisted")
			var phases []llm.ToolCallPhase
			endIdx := -1
			for i, ev := range client.events {
				if ev.payload["control"] == PostStreamingControlEnd {
					endIdx = i
					continue
				}
				if ev.payload["control"] != "tool_call" || ev.broadcast.UserId != requesterID {
					continue
				}
				phase := llm.ToolCallPhase(ev.payload["tool_call_phase"].(string))
				phases = append(phases, phase)
				if phase == llm.ToolCallPhaseAwaitingDecision {
					assert.GreaterOrEqual(t, i, ts.eventsAtCreate, "batch awaiting a decision broadcast before its turn was persisted")
				}
				assert.Less(t, endIdx, 0, "tool_call event broadcast after the end event")
			}
			require.NotEqual(t, -1, endIdx, "end event was never broadcast")
			assert.Equal(t, tt.wantPhases, phases)
		})
	}
}
