// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package delegation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
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
