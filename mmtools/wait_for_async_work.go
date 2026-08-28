// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
)

const (
	// WaitForAsyncWorkToolName is the runtime name of the built-in wait tool.
	WaitForAsyncWorkToolName = "wait_for_async_work"

	minWaitMinutes = 1
	maxWaitMinutes = 30

	waitForAsyncWorkDescription = "Use this when you have started long-running asynchronous work via another tool and need to check on it later. " +
		"You will be re-invoked after approximately the given number of minutes with a reminder. " +
		"When re-invoked, poll the relevant status tool and either report results to the user or call this tool again. " +
		"Do not keep polling in the current turn."
)

// WaitForAsyncWorkArgs is the LLM-visible input schema for wait_for_async_work.
type WaitForAsyncWorkArgs struct {
	Minutes int    `json:"minutes" jsonschema_description:"Minutes to wait before being re-invoked (1-30). Call again after waking if the work needs more time."`
	Reason  string `json:"reason" jsonschema_description:"Short note about what you are waiting for. Echoed back when you are re-invoked."`
}

// NewWaitForAsyncWorkTool returns the built-in wait tool. The resolver
// schedules a later resume and returns immediately; it never blocks.
func NewWaitForAsyncWorkTool(scheduler *WakeScheduler) llm.Tool {
	return llm.Tool{
		Name:        WaitForAsyncWorkToolName,
		Description: waitForAsyncWorkDescription,
		Schema:      llm.NewJSONSchemaFromStruct[WaitForAsyncWorkArgs](),
		AutoExecute: true,
		Resolver: func(ctx context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			return resolveWaitForAsyncWork(ctx, scheduler, llmCtx, argsGetter)
		},
	}
}

func resolveWaitForAsyncWork(ctx context.Context, scheduler *WakeScheduler, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args WaitForAsyncWorkArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for wait_for_async_work: %w", err)
	}

	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		return "reason is required", fmt.Errorf("wait_for_async_work requires a reason")
	}

	minutes := ClampWaitMinutes(args.Minutes)
	rec, err := wakeRecordFromContext(llmCtx, reason)
	if err != nil {
		return "waiting is not available in this context", err
	}

	if scheduler == nil {
		return "waiting is not available", fmt.Errorf("wait_for_async_work scheduler is not configured")
	}

	if err := scheduler.Schedule(ctx, rec, time.Duration(minutes)*time.Minute); err != nil {
		return "failed to schedule wait", fmt.Errorf("wait_for_async_work schedule failed: %w", err)
	}

	return WaitSuccessMessage(minutes, reason), nil
}

// ClampWaitMinutes clamps minutes to the allowed 1-30 range.
func ClampWaitMinutes(minutes int) int {
	switch {
	case minutes < minWaitMinutes:
		return minWaitMinutes
	case minutes > maxWaitMinutes:
		return maxWaitMinutes
	default:
		return minutes
	}
}

// WaitSuccessMessage is the immediate tool result returned to the model.
func WaitSuccessMessage(minutes int, reason string) string {
	return fmt.Sprintf("Waiting ~%d minutes. You will be re-invoked to check on: %s. Wrap up your current response now; do not keep polling.", minutes, reason)
}

// WakeUserMessage is the synthetic user-turn text injected when a wait fires.
func WakeUserMessage(reason string) string {
	return fmt.Sprintf("The wait you requested has elapsed (reason: %s). Check the status of the async work now using the appropriate tools. Report results to the user if complete, or call wait_for_async_work again if still running. If you have already waited many times without progress, report the situation to the user instead of waiting again.", reason)
}

func wakeRecordFromContext(llmCtx *llm.Context, reason string) (WakeRecord, error) {
	if llmCtx == nil || llmCtx.ConversationID == "" || llmCtx.PostID == "" {
		return WakeRecord{}, fmt.Errorf("wait_for_async_work requires a conversation-scoped context")
	}
	if llmCtx.RequestingUser == nil || llmCtx.RequestingUser.Id == "" {
		return WakeRecord{}, fmt.Errorf("wait_for_async_work requires a requesting user")
	}
	if llmCtx.Channel == nil || llmCtx.Channel.Id == "" {
		return WakeRecord{}, fmt.Errorf("wait_for_async_work requires a channel-scoped context")
	}
	if llmCtx.BotUserID == "" {
		return WakeRecord{}, fmt.Errorf("wait_for_async_work requires a bot")
	}

	return WakeRecord{
		ConversationID: llmCtx.ConversationID,
		BotID:          llmCtx.BotUserID,
		UserID:         llmCtx.RequestingUser.Id,
		ChannelID:      llmCtx.Channel.Id,
		PostID:         llmCtx.PostID,
		IsDM:           mmapi.IsDMWith(llmCtx.BotUserID, llmCtx.Channel),
		Reason:         reason,
	}, nil
}
