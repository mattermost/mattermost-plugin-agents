// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const (
	// WaitForAsyncWorkToolName is the runtime name of the built-in wait tool.
	WaitForAsyncWorkToolName = "wait_for_async_work"

	minWaitMinutes = 1
	maxWaitMinutes = 30

	waitAlreadyScheduledMessage = "A wait is already pending in this thread, so no new wait was added. Wrap up your current response now."

	waitForAsyncWorkDescription = "Use this when you have started long-running asynchronous work via another tool and need to check on it later. " +
		"You will be re-invoked after approximately the given number of minutes with a reminder. " +
		"When re-invoked, poll the relevant status tool and either report results to the user or call this tool again. " +
		"Do not keep polling in the current turn."
)

// ErrWaitAlreadyScheduled is returned by a ScheduleWake when the thread
// already has a pending wait for this bot.
var ErrWaitAlreadyScheduled = errors.New("a wait is already pending in this thread")

// ScheduleWake schedules a conversation resume onto the given bot response
// post after the wait elapses. The wake runs as userID, the user whose turn
// requested the wait.
type ScheduleWake func(postID, userID string, wait time.Duration) error

// WaitForAsyncWorkArgs is the LLM-visible input schema for wait_for_async_work.
type WaitForAsyncWorkArgs struct {
	Minutes int    `json:"minutes" jsonschema_description:"Minutes to wait before being re-invoked (1-30). Call again after waking if the work needs more time."`
	Reason  string `json:"reason" jsonschema_description:"Short note about what you are waiting for. Echoed back when you are re-invoked."`
}

// NewWaitForAsyncWorkTool returns the built-in wait tool. The resolver
// schedules a later resume and returns immediately; it never blocks.
func NewWaitForAsyncWorkTool(schedule ScheduleWake) llm.Tool {
	return llm.Tool{
		Name:        WaitForAsyncWorkToolName,
		Description: waitForAsyncWorkDescription,
		Schema:      llm.NewJSONSchemaFromStruct[WaitForAsyncWorkArgs](),
		AutoExecute: true,
		Resolver: func(_ context.Context, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			return resolveWaitForAsyncWork(schedule, llmCtx, argsGetter)
		},
	}
}

func resolveWaitForAsyncWork(schedule ScheduleWake, llmCtx *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
	var args WaitForAsyncWorkArgs
	if err := argsGetter(&args); err != nil {
		return "invalid parameters to function", fmt.Errorf("failed to get arguments for wait_for_async_work: %w", err)
	}

	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		return "reason is required", fmt.Errorf("wait_for_async_work requires a reason")
	}

	minutes := min(max(args.Minutes, minWaitMinutes), maxWaitMinutes)
	if err := schedule(llmCtx.ResponsePostID, llmCtx.RequestingUser.Id, time.Duration(minutes)*time.Minute); err != nil {
		if errors.Is(err, ErrWaitAlreadyScheduled) {
			return waitAlreadyScheduledMessage, nil
		}
		return "failed to schedule wait", fmt.Errorf("wait_for_async_work schedule failed: %w", err)
	}

	return fmt.Sprintf("Waiting ~%d minutes. You will be re-invoked to check on: %s. Wrap up your current response now; do not keep polling.", minutes, reason), nil
}

// WakeUserMessage is the synthetic user-turn text injected when a wait fires.
// It is a fixed string: the model-supplied reason must never be voiced as
// the user, so the model recovers it from its own earlier tool result.
const WakeUserMessage = "The wait you requested with wait_for_async_work has elapsed; your earlier call to that tool notes what you were waiting on. Check the status of the async work now using the appropriate tools. Report results to the user if complete, or call wait_for_async_work again if still running. If you have already waited many times without progress, report the situation to the user instead of waiting again."

// WakeCanceledUserMessage is the synthetic user-turn text recorded when a new
// message cancels a pending wait. It is written to the invoking conversation,
// which may not be the one that scheduled the wait, so it stays generic. Like
// WakeUserMessage it is fixed text.
const WakeCanceledUserMessage = "A pending wait_for_async_work in this thread was canceled because a new message arrived before it elapsed. Earlier replies in this thread (and your earlier wait call, if you made one) describe what was being waited on. Address the new message. If waiting on that async work is still relevant, you may call wait_for_async_work again."
