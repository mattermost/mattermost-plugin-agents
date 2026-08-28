// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func waitArgsGetter(t *testing.T, args WaitForAsyncWorkArgs) llm.ToolArgumentGetter {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	return func(out any) error {
		return json.Unmarshal(raw, out)
	}
}

func validWaitLLMContext() *llm.Context {
	return &llm.Context{
		ConversationID: "conv-id",
		PostID:         "post-id",
		BotUserID:      "bot-id",
		RequestingUser: &model.User{Id: "user-id"},
		Channel:        &model.Channel{Id: "channel-id", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"},
	}
}

func TestNewWaitForAsyncWorkTool(t *testing.T) {
	tool := NewWaitForAsyncWorkTool(&WakeScheduler{})

	require.Equal(t, WaitForAsyncWorkToolName, tool.Name)
	require.True(t, tool.AutoExecute)
	require.Empty(t, tool.ServerOrigin)
	require.Empty(t, tool.UserInteraction)
	require.NotEmpty(t, tool.Description)
	require.NotNil(t, tool.Schema)
	require.NotNil(t, tool.Resolver)
}

func TestWaitForAsyncWorkResolverValidation(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		args        WaitForAsyncWorkArgs
		badArgs     bool
		llmCtx      *llm.Context
		nilSched    bool
		wantResult  string
		wantErr     bool
		wantMinutes int
		wantReason  string
	}{
		{
			name:        "clamps minutes below minimum",
			args:        WaitForAsyncWorkArgs{Minutes: 0, Reason: "poll agent"},
			llmCtx:      validWaitLLMContext(),
			wantResult:  WaitSuccessMessage(1, "poll agent"),
			wantMinutes: 1,
			wantReason:  "poll agent",
		},
		{
			name:        "clamps minutes above maximum",
			args:        WaitForAsyncWorkArgs{Minutes: 90, Reason: "poll agent"},
			llmCtx:      validWaitLLMContext(),
			wantResult:  WaitSuccessMessage(30, "poll agent"),
			wantMinutes: 30,
			wantReason:  "poll agent",
		},
		{
			name:        "in-range minutes",
			args:        WaitForAsyncWorkArgs{Minutes: 5, Reason: "cursor cloud agent"},
			llmCtx:      validWaitLLMContext(),
			wantResult:  WaitSuccessMessage(5, "cursor cloud agent"),
			wantMinutes: 5,
			wantReason:  "cursor cloud agent",
		},
		{
			name:        "trims reason",
			args:        WaitForAsyncWorkArgs{Minutes: 2, Reason: "  check status  "},
			llmCtx:      validWaitLLMContext(),
			wantResult:  WaitSuccessMessage(2, "check status"),
			wantMinutes: 2,
			wantReason:  "check status",
		},
		{
			name:       "empty reason",
			args:       WaitForAsyncWorkArgs{Minutes: 5, Reason: "   "},
			llmCtx:     validWaitLLMContext(),
			wantResult: "reason is required",
			wantErr:    true,
		},
		{
			name:       "invalid arguments",
			badArgs:    true,
			llmCtx:     validWaitLLMContext(),
			wantResult: "invalid parameters to function",
			wantErr:    true,
		},
		{
			name:       "missing conversation resume ids",
			args:       WaitForAsyncWorkArgs{Minutes: 5, Reason: "poll agent"},
			llmCtx:     &llm.Context{RequestingUser: &model.User{Id: "user-id"}, Channel: &model.Channel{Id: "ch"}, BotUserID: "bot"},
			wantResult: "waiting is not available in this context",
			wantErr:    true,
		},
		{
			name:       "nil scheduler",
			args:       WaitForAsyncWorkArgs{Minutes: 5, Reason: "poll agent"},
			llmCtx:     validWaitLLMContext(),
			nilSched:   true,
			wantResult: "waiting is not available",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMemoryWakeStore()
			var scheduled []time.Duration
			var scheduler *WakeScheduler
			if !tt.nilSched {
				scheduler = NewWakeScheduler(store, nopWakeLog{}, WakeSchedulerOptions{
					Now: func() time.Time { return now },
					AfterFunc: func(d time.Duration, _ func()) {
						scheduled = append(scheduled, d)
					},
				})
			}

			var getter llm.ToolArgumentGetter
			if tt.badArgs {
				getter = func(any) error { return json.Unmarshal([]byte(`{`), &struct{}{}) }
			} else {
				getter = waitArgsGetter(t, tt.args)
			}

			result, err := resolveWaitForAsyncWork(context.Background(), scheduler, tt.llmCtx, getter)
			require.Equal(t, tt.wantResult, result)
			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, scheduled)
				return
			}
			require.NoError(t, err)
			require.Len(t, scheduled, 1)
			require.Equal(t, time.Duration(tt.wantMinutes)*time.Minute, scheduled[0])

			keys, listErr := store.ListPrefix(wakeKVKeyPrefix)
			require.NoError(t, listErr)
			require.Len(t, keys, 1)

			var rec WakeRecord
			require.NoError(t, store.Get(keys[0], &rec))
			require.Equal(t, tt.wantReason, rec.Reason)
			require.Equal(t, now.Add(time.Duration(tt.wantMinutes)*time.Minute).UnixMilli(), rec.FireAt)
			require.Equal(t, "conv-id", rec.ConversationID)
			require.Equal(t, "post-id", rec.PostID)
			require.Equal(t, "bot-id", rec.BotID)
			require.Equal(t, "user-id", rec.UserID)
			require.Equal(t, "channel-id", rec.ChannelID)
			require.True(t, rec.IsDM)
		})
	}
}

func TestClampWaitMinutes(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{in: -5, want: 1},
		{in: 0, want: 1},
		{in: 1, want: 1},
		{in: 15, want: 15},
		{in: 30, want: 30},
		{in: 31, want: 30},
		{in: 100, want: 30},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("minutes=%d", tt.in), func(t *testing.T) {
			require.Equal(t, tt.want, ClampWaitMinutes(tt.in))
		})
	}
}

func TestWaitMessages(t *testing.T) {
	tests := []struct {
		name   string
		reason string
	}{
		{name: "success echoes reason", reason: "cursor agent abc"},
		{name: "wake echoes reason and asks to stop looping", reason: "jira job 42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			success := WaitSuccessMessage(7, tt.reason)
			require.Contains(t, success, tt.reason)
			require.Contains(t, success, "~7 minutes")
			require.Contains(t, success, "Wrap up your current response")

			wake := WakeUserMessage(tt.reason)
			require.Contains(t, wake, tt.reason)
			require.Contains(t, wake, "Check the status of the async work")
			require.Contains(t, wake, "call wait_for_async_work again")
			require.Contains(t, wake, "already waited many times without progress")
		})
	}
}

func TestGetToolsWaitForAsyncWorkCatalog(t *testing.T) {
	responseFilesCtx := func() *llm.Context {
		return &llm.Context{ToolCatalog: llm.ToolCatalogContext{ResponseFilesSupported: true}}
	}

	tests := []struct {
		name       string
		scheduler  bool
		llmContext *llm.Context
		want       bool
	}{
		{name: "cataloged when scheduler and response files set", scheduler: true, llmContext: responseFilesCtx(), want: true},
		{name: "not cataloged without scheduler", scheduler: false, llmContext: responseFilesCtx(), want: false},
		{name: "not cataloged without response files", scheduler: true, llmContext: &llm.Context{}, want: false},
		{name: "not cataloged with nil context", scheduler: true, llmContext: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewMMToolProvider(nil, nil)
			if tt.scheduler {
				provider.SetWakeScheduler(&WakeScheduler{})
			}

			names := []string{}
			for _, tool := range provider.GetTools(nil, tt.llmContext) {
				names = append(names, tool.Name)
			}

			if tt.want {
				require.Contains(t, names, WaitForAsyncWorkToolName)
			} else {
				require.NotContains(t, names, WaitForAsyncWorkToolName)
			}
		})
	}
}
