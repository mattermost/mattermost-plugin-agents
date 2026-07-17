// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package delegation

import (
	"sort"
	"strings"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

// WebsocketEventDelegationUpdate is the plugin websocket event name for live
// delegation progress (webapp: custom_mattermost-ai_delegation_update).
const WebsocketEventDelegationUpdate = "delegation_update"

// Activity values for the running phase.
const (
	ActivityUsingTools = "using_tools"
	ActivityWriting    = "writing"
)

// emitUpdate publishes a delegation_update websocket event scoped to the
// initiator. Progress is initiator-private: the delegation thread lives in
// their DM and the parent card only renders full data for the requester.
func emitUpdate(mmClient mmapi.Client, record Record, phase, activity, tools, permalink string) {
	payload := map[string]interface{}{
		"delegation_id":            record.DelegationID,
		"parent_tool_call_id":      record.ParentToolCallID,
		"phase":                    phase,
		"task_post_id":             record.TaskPostID,
		"permalink":                permalink,
		"target_agent_id":          record.TargetBotID,
		"target_agent_username":    record.TargetBotUsername,
		"target_agent_displayname": record.TargetBotDisplayName,
	}
	if activity != "" {
		payload["activity"] = activity
	}
	if tools != "" {
		payload["tools"] = tools
	}

	mmClient.PublishWebSocketEvent(WebsocketEventDelegationUpdate, payload, &model.WebsocketBroadcast{
		UserId:              record.InitiatorUserID,
		ReliableClusterSend: true,
	})
}

// progressObserver converts delegated sub-turn stream events into running
// phase updates. Consecutive duplicate updates are coalesced.
type progressObserver struct {
	emit func(activity, tools string)

	mu       sync.Mutex
	lastSig  string
	sawFirst bool
}

func newProgressObserver(emit func(activity, tools string)) *progressObserver {
	return &progressObserver{emit: emit}
}

// observe runs on the streaming goroutine; keep it light and non-blocking
// (PublishWebSocketEvent is fire-and-forget).
func (o *progressObserver) observe(event llm.TextStreamEvent) {
	switch event.Type {
	case llm.EventTypeToolCalls:
		toolCalls, ok := event.Value.([]llm.ToolCall)
		if !ok || len(toolCalls) == 0 {
			return
		}
		names := make([]string, 0, len(toolCalls))
		seen := make(map[string]struct{}, len(toolCalls))
		for _, tc := range toolCalls {
			name := llm.BareMCPToolName(tc.Name)
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		sort.Strings(names)
		o.maybeEmit(ActivityUsingTools, strings.Join(names, ", "))
	case llm.EventTypeText, llm.EventTypeReasoning:
		o.maybeEmit(ActivityWriting, "")
	}
}

func (o *progressObserver) maybeEmit(activity, tools string) {
	sig := activity + "\x00" + tools
	o.mu.Lock()
	if o.sawFirst && o.lastSig == sig {
		o.mu.Unlock()
		return
	}
	o.sawFirst = true
	o.lastSig = sig
	o.mu.Unlock()

	o.emit(activity, tools)
}
