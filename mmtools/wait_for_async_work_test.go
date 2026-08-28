// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
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

type scheduledWake struct {
	postID string
	reason string
	wait   time.Duration
}

func TestNewWaitForAsyncWorkTool(t *testing.T) {
	tool := NewWaitForAsyncWorkTool(func(string, string, time.Duration) error { return nil })

	require.Equal(t, WaitForAsyncWorkToolName, tool.Name)
	require.True(t, tool.AutoExecute)
	require.Empty(t, tool.ServerOrigin)
	require.Empty(t, tool.UserInteraction)
	require.NotEmpty(t, tool.Description)
	require.NotNil(t, tool.Schema)
	require.NotNil(t, tool.Resolver)
}

func TestWaitForAsyncWorkResolver(t *testing.T) {
	tests := []struct {
		name        string
		args        WaitForAsyncWorkArgs
		badArgs     bool
		llmCtx      *llm.Context
		scheduleErr error
		wantResult  string
		wantErr     bool
		wantWake    *scheduledWake
	}{
		{
			name:       "clamps minutes below minimum",
			args:       WaitForAsyncWorkArgs{Minutes: 0, Reason: "poll agent"},
			llmCtx:     &llm.Context{ResponsePostID: "post-id"},
			wantResult: waitSuccessMessage(1, "poll agent"),
			wantWake:   &scheduledWake{postID: "post-id", reason: "poll agent", wait: time.Minute},
		},
		{
			name:       "clamps minutes above maximum",
			args:       WaitForAsyncWorkArgs{Minutes: 90, Reason: "poll agent"},
			llmCtx:     &llm.Context{ResponsePostID: "post-id"},
			wantResult: waitSuccessMessage(30, "poll agent"),
			wantWake:   &scheduledWake{postID: "post-id", reason: "poll agent", wait: 30 * time.Minute},
		},
		{
			name:       "in-range minutes",
			args:       WaitForAsyncWorkArgs{Minutes: 5, Reason: "cursor cloud agent"},
			llmCtx:     &llm.Context{ResponsePostID: "post-id"},
			wantResult: waitSuccessMessage(5, "cursor cloud agent"),
			wantWake:   &scheduledWake{postID: "post-id", reason: "cursor cloud agent", wait: 5 * time.Minute},
		},
		{
			name:       "trims reason",
			args:       WaitForAsyncWorkArgs{Minutes: 2, Reason: "  check status  "},
			llmCtx:     &llm.Context{ResponsePostID: "post-id"},
			wantResult: waitSuccessMessage(2, "check status"),
			wantWake:   &scheduledWake{postID: "post-id", reason: "check status", wait: 2 * time.Minute},
		},
		{
			name:       "empty reason",
			args:       WaitForAsyncWorkArgs{Minutes: 5, Reason: "   "},
			llmCtx:     &llm.Context{ResponsePostID: "post-id"},
			wantResult: "reason is required",
			wantErr:    true,
		},
		{
			name:       "invalid arguments",
			badArgs:    true,
			llmCtx:     &llm.Context{ResponsePostID: "post-id"},
			wantResult: "invalid parameters to function",
			wantErr:    true,
		},
		{
			name:       "missing response post",
			args:       WaitForAsyncWorkArgs{Minutes: 5, Reason: "poll agent"},
			llmCtx:     &llm.Context{},
			wantResult: "waiting is not available in this context",
			wantErr:    true,
		},
		{
			name:        "schedule failure",
			args:        WaitForAsyncWorkArgs{Minutes: 5, Reason: "poll agent"},
			llmCtx:      &llm.Context{ResponsePostID: "post-id"},
			scheduleErr: fmt.Errorf("kv unavailable"),
			wantResult:  "failed to schedule wait",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var scheduled []scheduledWake
			schedule := func(postID, reason string, wait time.Duration) error {
				if tt.scheduleErr != nil {
					return tt.scheduleErr
				}
				scheduled = append(scheduled, scheduledWake{postID: postID, reason: reason, wait: wait})
				return nil
			}

			var getter llm.ToolArgumentGetter
			if tt.badArgs {
				getter = func(any) error { return json.Unmarshal([]byte(`{`), &struct{}{}) }
			} else {
				getter = waitArgsGetter(t, tt.args)
			}

			result, err := resolveWaitForAsyncWork(schedule, tt.llmCtx, getter)
			require.Equal(t, tt.wantResult, result)
			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, scheduled)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []scheduledWake{*tt.wantWake}, scheduled)
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
			require.Equal(t, tt.want, clampWaitMinutes(tt.in))
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
			success := waitSuccessMessage(7, tt.reason)
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
				provider.SetScheduleWake(func(string, string, time.Duration) error { return nil })
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
