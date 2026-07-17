// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package delegation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	promptsassets "github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestBots(t *testing.T) *bots.MMBots {
	t.Helper()
	mmBots := bots.New(nil, nil, nil, nil, nil, nil, nil)
	mmBots.SetBotsForTesting([]*bots.Bot{
		bots.NewBot(
			llm.BotConfig{ID: "matty-cfg", Name: "matty", DisplayName: "Matty", UserAccessLevel: llm.UserAccessLevelAll},
			llm.ServiceConfig{},
			&model.Bot{UserId: "matty-bot-id", Username: "matty"},
			nil,
		),
		bots.NewBot(
			llm.BotConfig{ID: "projects-cfg", Name: "projects", DisplayName: "Projects Agent", UserAccessLevel: llm.UserAccessLevelAll},
			llm.ServiceConfig{},
			&model.Bot{UserId: "projects-bot-id", Username: "projects"},
			nil,
		),
		bots.NewBot(
			llm.BotConfig{ID: "locked-cfg", Name: "locked", DisplayName: "Locked Agent", UserAccessLevel: llm.UserAccessLevelNone},
			llm.ServiceConfig{},
			&model.Bot{UserId: "locked-bot-id", Username: "locked"},
			nil,
		),
	})
	return mmBots
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name       string
		req        Request
		user       *model.User
		userErr    error
		wantErr    error
		wantTarget string
	}{
		{
			name:       "valid delegation by username",
			req:        Request{InitiatorUserID: "user-1", DelegatingBotUserID: "matty-bot-id", TargetAgent: "projects"},
			user:       &model.User{Id: "user-1", Username: "alice"},
			wantTarget: "projects-bot-id",
		},
		{
			name:       "valid delegation with @ prefix",
			req:        Request{InitiatorUserID: "user-1", DelegatingBotUserID: "matty-bot-id", TargetAgent: "@projects"},
			user:       &model.User{Id: "user-1", Username: "alice"},
			wantTarget: "projects-bot-id",
		},
		{
			name:       "valid delegation by bot user ID",
			req:        Request{InitiatorUserID: "user-1", DelegatingBotUserID: "matty-bot-id", TargetAgent: "projects-bot-id"},
			user:       &model.User{Id: "user-1", Username: "alice"},
			wantTarget: "projects-bot-id",
		},
		{
			name:    "unknown agent",
			req:     Request{InitiatorUserID: "user-1", DelegatingBotUserID: "matty-bot-id", TargetAgent: "nonexistent"},
			user:    &model.User{Id: "user-1", Username: "alice"},
			wantErr: ErrUnknownAgent,
		},
		{
			name:    "self delegation",
			req:     Request{InitiatorUserID: "user-1", DelegatingBotUserID: "matty-bot-id", TargetAgent: "matty"},
			user:    &model.User{Id: "user-1", Username: "alice"},
			wantErr: ErrSelfDelegation,
		},
		{
			name:    "access denied by target agent restrictions",
			req:     Request{InitiatorUserID: "user-1", DelegatingBotUserID: "matty-bot-id", TargetAgent: "locked"},
			user:    &model.User{Id: "user-1", Username: "alice"},
			wantErr: ErrAccessDenied,
		},
		{
			name:    "missing initiator",
			req:     Request{DelegatingBotUserID: "matty-bot-id", TargetAgent: "projects"},
			wantErr: ErrAccessDenied,
		},
		{
			name:    "delegating bot is not a known agent",
			req:     Request{InitiatorUserID: "user-1", DelegatingBotUserID: "stranger-id", TargetAgent: "projects"},
			user:    &model.User{Id: "user-1", Username: "alice"},
			wantErr: ErrAccessDenied,
		},
		{
			name:    "bot initiator is refused",
			req:     Request{InitiatorUserID: "bot-user", DelegatingBotUserID: "matty-bot-id", TargetAgent: "projects"},
			user:    &model.User{Id: "bot-user", Username: "otherbot", IsBot: true},
			wantErr: ErrAccessDenied,
		},
		{
			name:    "initiator lookup failure",
			req:     Request{InitiatorUserID: "user-1", DelegatingBotUserID: "matty-bot-id", TargetAgent: "projects"},
			userErr: errors.New("boom"),
			wantErr: ErrAccessDenied,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := mocks.NewMockClient(t)
			if tc.req.InitiatorUserID != "" && tc.req.DelegatingBotUserID == "matty-bot-id" {
				client.EXPECT().GetUser(tc.req.InitiatorUserID).Return(tc.user, tc.userErr).Maybe()
			}

			svc := New(client, newTestBots(t), nil, nil, nil, nil)
			_, target, initiator, err := svc.validate(tc.req)

			if tc.wantErr != nil {
				require.Error(t, err)
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantTarget, target.GetMMBot().UserId)
			require.Equal(t, tc.req.InitiatorUserID, initiator.Id)
		})
	}
}

func TestDelegateNotConfigured(t *testing.T) {
	svc := New(mocks.NewMockClient(t), newTestBots(t), nil, nil, nil, nil)
	_, err := svc.Delegate(context.Background(), Request{
		InitiatorUserID:     "user-1",
		DelegatingBotUserID: "matty-bot-id",
		TargetAgent:         "projects",
	})
	require.ErrorIs(t, err, ErrNotConfigured)
}

// fakeConversationStore backs a real conversation.Service with canned turns.
type fakeConversationStore struct {
	conversation.Store
	turns []store.Turn
}

func (f *fakeConversationStore) GetTurnsForConversation(string) ([]store.Turn, error) {
	return f.turns, nil
}

// completeForStatusTests wires the minimum dependencies StatusByParentToolCall
// needs (a conversation service over canned turns).
func completeForStatusTests(t *testing.T, svc *Service, turns []store.Turn) {
	t.Helper()
	convService := conversation.NewService(&fakeConversationStore{turns: turns}, nil, nil, nil)
	svc.Complete(
		convService,
		conversations.New(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil),
		llmcontext.NewLLMContextBuilder(nil, nil, nil, nil),
		nil,
		nil,
	)
	// prompts/streaming are unused by StatusByParentToolCall but gate Available.
	prompts, err := llm.NewPrompts(promptsassets.PromptsFolder)
	require.NoError(t, err)
	svc.prompts = prompts
	svc.streaming = streaming.NewMMPostStreamService(nil, nil)
}

func TestStatusByParentToolCall(t *testing.T) {
	answerTurns := []store.Turn{
		turnOf(t, "user", []map[string]any{{"type": "text", "text": "task"}}),
		turnOf(t, "assistant", []map[string]any{{"type": "text", "text": "the answer"}}),
	}
	waitingTurns := []store.Turn{
		turnOf(t, "user", []map[string]any{{"type": "text", "text": "task"}}),
		turnOf(t, "assistant", []map[string]any{
			{"type": "tool_use", "id": "t1", "name": "create_post", "status": "pending"},
		}),
	}
	record := Record{
		DelegationID:         "conv-1",
		ParentToolCallID:     "ptc-1",
		InitiatorUserID:      "alice-id",
		TargetBotID:          "projects-bot-id",
		TargetBotUsername:    "projects",
		TargetBotDisplayName: "Projects Agent",
		TaskPostID:           "task-post-id",
		CreatedAt:            123,
	}

	tests := []struct {
		name        string
		userID      string
		toolCallID  string
		record      *Record
		turns       []store.Turn
		wantNil     bool
		wantPhase   string
		wantPreview string
	}{
		{
			name:        "initiator sees completed with preview",
			userID:      "alice-id",
			toolCallID:  "ptc-1",
			record:      &record,
			turns:       answerTurns,
			wantPhase:   PhaseCompleted,
			wantPreview: "the answer",
		},
		{
			name:       "initiator sees waiting_on_you",
			userID:     "alice-id",
			toolCallID: "ptc-1",
			record:     &record,
			turns:      waitingTurns,
			wantPhase:  PhaseWaitingOnYou,
		},
		{
			name:       "non-initiator gets nothing",
			userID:     "bob-id",
			toolCallID: "ptc-1",
			record:     &record,
			turns:      answerTurns,
			wantNil:    true,
		},
		{
			name:       "unknown tool call gets nothing",
			userID:     "alice-id",
			toolCallID: "ptc-unknown",
			record:     nil,
			turns:      answerTurns,
			wantNil:    true,
		},
		{
			name:       "empty user gets nothing",
			userID:     "",
			toolCallID: "ptc-1",
			record:     &record,
			turns:      answerTurns,
			wantNil:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := mocks.NewMockClient(t)
			if tc.userID != "" && tc.toolCallID != "" {
				call := client.EXPECT().KVGet(recordKeyByParentToolCall(tc.toolCallID), mock.Anything)
				if tc.record != nil {
					rec := *tc.record
					call.RunAndReturn(func(_ string, value any) error {
						*(value.(*Record)) = rec
						return nil
					})
				} else {
					call.Return(mmapi.ErrKVNotFound)
				}
				call.Maybe()
			}

			svc := New(client, newTestBots(t), nil, nil, nil, nil)
			completeForStatusTests(t, svc, tc.turns)
			client.EXPECT().GetConfig().Return(&model.Config{ServiceSettings: model.ServiceSettings{SiteURL: model.NewPointer("https://mm.example.com")}}).Maybe()

			status, err := svc.StatusByParentToolCall(tc.userID, tc.toolCallID)
			require.NoError(t, err)

			if tc.wantNil {
				require.Nil(t, status)
				return
			}
			require.NotNil(t, status)
			require.Equal(t, tc.wantPhase, status.Phase)
			require.Equal(t, tc.wantPreview, status.AnswerPreview)
			require.Equal(t, "https://mm.example.com/_redirect/pl/task-post-id", status.Permalink)
			require.Equal(t, "projects", status.TargetAgentUsername)
		})
	}
}

// fakeTurnSource implements turnSource for state derivation tests.
type fakeTurnSource struct {
	turns []store.Turn
	err   error
}

func (f *fakeTurnSource) GetTurns(string) ([]store.Turn, error) {
	return f.turns, f.err
}

func turnOf(t *testing.T, role string, blocks []map[string]any) store.Turn {
	t.Helper()
	content, err := json.Marshal(blocks)
	require.NoError(t, err)
	return store.Turn{Role: role, Content: content}
}

func TestLatestSubTurnState(t *testing.T) {
	tests := []struct {
		name      string
		turns     []store.Turn
		err       error
		wantState subTurnState
		wantText  string
	}{
		{
			name:      "no turns",
			wantState: subTurnStateNone,
		},
		{
			name: "only the task user turn",
			turns: []store.Turn{
				turnOf(t, "user", []map[string]any{{"type": "text", "text": "do the thing"}}),
			},
			wantState: subTurnStateNone,
		},
		{
			name: "final answer",
			turns: []store.Turn{
				turnOf(t, "user", []map[string]any{{"type": "text", "text": "do the thing"}}),
				turnOf(t, "assistant", []map[string]any{{"type": "text", "text": "the answer"}}),
			},
			wantState: subTurnStateCompleted,
			wantText:  "the answer",
		},
		{
			name: "pending approval",
			turns: []store.Turn{
				turnOf(t, "user", []map[string]any{{"type": "text", "text": "do the thing"}}),
				turnOf(t, "assistant", []map[string]any{
					{"type": "tool_use", "id": "t1", "name": "create_post", "status": "pending"},
				}),
			},
			wantState: subTurnStateWaiting,
		},
		{
			name: "claimed accepted approval still waiting",
			turns: []store.Turn{
				turnOf(t, "user", []map[string]any{{"type": "text", "text": "do the thing"}}),
				turnOf(t, "assistant", []map[string]any{
					{"type": "tool_use", "id": "t1", "name": "create_post", "status": "accepted"},
				}),
			},
			wantState: subTurnStateWaiting,
		},
		{
			name: "resolved tool round with preamble text is not the answer",
			turns: []store.Turn{
				turnOf(t, "user", []map[string]any{{"type": "text", "text": "do the thing"}}),
				turnOf(t, "assistant", []map[string]any{
					{"type": "text", "text": "let me check"},
					{"type": "tool_use", "id": "t1", "name": "search_posts", "status": "success"},
				}),
				turnOf(t, "tool_result", []map[string]any{{"type": "tool_result", "tool_use_id": "t1", "content": "found"}}),
			},
			wantState: subTurnStateNone,
		},
		{
			name: "answer after resolved tool round",
			turns: []store.Turn{
				turnOf(t, "user", []map[string]any{{"type": "text", "text": "do the thing"}}),
				turnOf(t, "assistant", []map[string]any{
					{"type": "tool_use", "id": "t1", "name": "search_posts", "status": "success"},
				}),
				turnOf(t, "tool_result", []map[string]any{{"type": "tool_result", "tool_use_id": "t1", "content": "found"}}),
				turnOf(t, "assistant", []map[string]any{{"type": "text", "text": "final synthesis"}}),
			},
			wantState: subTurnStateCompleted,
			wantText:  "final synthesis",
		},
		{
			name: "rejected round without follow-up settles without answer",
			turns: []store.Turn{
				turnOf(t, "user", []map[string]any{{"type": "text", "text": "do the thing"}}),
				turnOf(t, "assistant", []map[string]any{
					{"type": "tool_use", "id": "t1", "name": "create_post", "status": "rejected"},
				}),
				turnOf(t, "tool_result", []map[string]any{{"type": "tool_result", "tool_use_id": "t1", "content": "Tool call rejected by user"}}),
			},
			wantState: subTurnStateNone,
		},
		{
			name: "later user follow-up resets the window",
			turns: []store.Turn{
				turnOf(t, "user", []map[string]any{{"type": "text", "text": "task"}}),
				turnOf(t, "assistant", []map[string]any{{"type": "text", "text": "first answer"}}),
				turnOf(t, "user", []map[string]any{{"type": "text", "text": "follow-up"}}),
			},
			wantState: subTurnStateNone,
		},
		{
			name:      "turn fetch error",
			err:       errors.New("db down"),
			wantState: subTurnStateNone,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state, text := latestSubTurnState(&fakeTurnSource{turns: tc.turns, err: tc.err}, "conv-1")
			require.Equal(t, tc.wantState, state)
			require.Equal(t, tc.wantText, text)
		})
	}
}
