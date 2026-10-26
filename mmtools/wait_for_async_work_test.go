// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"encoding/json"
	"fmt"
	"strings"
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

type scheduledWake struct {
	postID string
	userID string
	wait   time.Duration
}

func waitCtx() *llm.Context {
	return &llm.Context{ResponsePostID: "post-id", RequestingUser: &model.User{Id: "user-id"}}
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
			name:     "clamps minutes below minimum",
			args:     WaitForAsyncWorkArgs{Minutes: 0, Reason: "poll agent"},
			llmCtx:   waitCtx(),
			wantWake: &scheduledWake{postID: "post-id", userID: "user-id", wait: time.Minute},
		},
		{
			name:     "clamps minutes above maximum",
			args:     WaitForAsyncWorkArgs{Minutes: 90, Reason: "poll agent"},
			llmCtx:   waitCtx(),
			wantWake: &scheduledWake{postID: "post-id", userID: "user-id", wait: 30 * time.Minute},
		},
		{
			name:     "in-range minutes",
			args:     WaitForAsyncWorkArgs{Minutes: 5, Reason: "cursor cloud agent"},
			llmCtx:   waitCtx(),
			wantWake: &scheduledWake{postID: "post-id", userID: "user-id", wait: 5 * time.Minute},
		},
		{
			name:     "trims reason",
			args:     WaitForAsyncWorkArgs{Minutes: 2, Reason: "  check status  "},
			llmCtx:   waitCtx(),
			wantWake: &scheduledWake{postID: "post-id", userID: "user-id", wait: 2 * time.Minute},
		},
		{
			name:       "empty reason",
			args:       WaitForAsyncWorkArgs{Minutes: 5, Reason: "   "},
			llmCtx:     waitCtx(),
			wantResult: "reason is required",
			wantErr:    true,
		},
		{
			name:       "invalid arguments",
			badArgs:    true,
			llmCtx:     waitCtx(),
			wantResult: "invalid parameters to function",
			wantErr:    true,
		},
		{
			name:        "wait already scheduled is rejected as a result, not an error",
			args:        WaitForAsyncWorkArgs{Minutes: 5, Reason: "poll agent"},
			llmCtx:      waitCtx(),
			scheduleErr: fmt.Errorf("wrapped: %w", ErrWaitAlreadyScheduled),
			wantResult:  waitAlreadyScheduledMessage,
		},
		{
			name:        "schedule failure",
			args:        WaitForAsyncWorkArgs{Minutes: 5, Reason: "poll agent"},
			llmCtx:      waitCtx(),
			scheduleErr: fmt.Errorf("kv unavailable"),
			wantResult:  "failed to schedule wait",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var scheduled []scheduledWake
			schedule := func(postID, userID string, wait time.Duration) error {
				if tt.scheduleErr != nil {
					return tt.scheduleErr
				}
				scheduled = append(scheduled, scheduledWake{postID: postID, userID: userID, wait: wait})
				return nil
			}

			var getter llm.ToolArgumentGetter
			if tt.badArgs {
				getter = func(any) error { return json.Unmarshal([]byte(`{`), &struct{}{}) }
			} else {
				getter = waitArgsGetter(t, tt.args)
			}

			result, err := resolveWaitForAsyncWork(schedule, tt.llmCtx, getter)
			if tt.wantWake != nil {
				// The result tells the model how long it waits and what for.
				require.Contains(t, result, fmt.Sprintf("~%d minutes", int(tt.wantWake.wait/time.Minute)))
				require.Contains(t, result, strings.TrimSpace(tt.args.Reason))
			} else {
				require.Equal(t, tt.wantResult, result)
			}
			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, scheduled)
				return
			}
			require.NoError(t, err)
			if tt.wantWake == nil {
				require.Empty(t, scheduled)
				return
			}
			require.Equal(t, []scheduledWake{*tt.wantWake}, scheduled)
		})
	}
}

func TestGetToolsWaitForAsyncWorkCatalog(t *testing.T) {
	responseFilesCtx := func() *llm.Context {
		return &llm.Context{ToolCatalog: llm.ToolCatalogContext{ResponseFilesSupported: true}, RequestingUser: &model.User{Id: "user-id"}}
	}

	tests := []struct {
		name       string
		scheduler  bool
		llmContext *llm.Context
		want       bool
	}{
		{name: "cataloged when scheduler and response files set", scheduler: true, llmContext: responseFilesCtx(), want: true},
		{name: "not cataloged without scheduler", scheduler: false, llmContext: responseFilesCtx(), want: false},
		{name: "not cataloged without a requesting user", scheduler: true, llmContext: &llm.Context{ToolCatalog: llm.ToolCatalogContext{ResponseFilesSupported: true}}, want: false},
		{name: "not cataloged without response files", scheduler: true, llmContext: &llm.Context{}, want: false},
		{name: "not cataloged with nil context", scheduler: true, llmContext: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewMMToolProvider(nil, nil, nil)
			if tt.scheduler {
				provider.SetScheduleWake(func(string, string, time.Duration) error { return nil })
			}

			var waitTool *llm.Tool
			for _, tool := range provider.GetTools(nil, tt.llmContext) {
				if tool.Name == WaitForAsyncWorkToolName {
					waitTool = &tool
				}
			}

			if !tt.want {
				require.Nil(t, waitTool)
				return
			}
			require.NotNil(t, waitTool)
			// The wake must not wait on an approval nobody is present to give.
			require.True(t, waitTool.AutoExecute)
		})
	}
}
