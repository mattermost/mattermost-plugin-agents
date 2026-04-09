// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"errors"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost/server/public/model"
)

// ErrChannelToolCallingDisabled is returned when tool calling is attempted in a channel
// but the EnableChannelMentionToolCalling config flag is disabled.
var ErrChannelToolCallingDisabled = errors.New("channel tool calling is disabled")

func allowToolsInChannelFromPost(post *model.Post) bool {
	if post == nil {
		return false
	}

	value := post.GetProp(streaming.AllowToolsInChannelProp)
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func setAllowToolsInChannelProp(post *model.Post, allow bool) {
	if post == nil || !allow {
		return
	}
	post.AddProp(streaming.AllowToolsInChannelProp, "true")
}

func applyToolAvailability(context *llm.Context, isDM bool, allowToolsInChannel bool) bool {
	toolsDisabled := !isDM && !allowToolsInChannel
	if context != nil {
		if toolsDisabled && context.Tools != nil {
			context.DisabledToolsInfo = context.Tools.GetToolsInfo()
		} else {
			context.DisabledToolsInfo = nil
		}
	}
	return toolsDisabled
}

// filterAutomatedInvokerTools removes tools that require human approval or channel result-sharing
// when the request is from an automated Mattermost invoker.
func filterAutomatedInvokerTools(store *llm.ToolStore, automated bool, isDM bool, checker streaming.ToolPolicyChecker) {
	if store == nil || !automated || checker == nil {
		return
	}
	var remove []string
	for _, t := range store.GetTools() {
		// Built-in / native tools use an empty ServerOrigin. The production policy checker
		// only knows MCP servers and returns ("ask", false) for unknown origins, which would
		// strip every native tool from automated invokers. Only apply MCP policy filtering
		// when the tool is tied to a server origin.
		if t.ServerOrigin == "" {
			continue
		}
		policy, enabled := checker.GetToolPolicy(t.ServerOrigin, t.Name)
		keep := false
		if isDM {
			keep = mcp.IsToolPolicyAutoRun(policy) && enabled
		} else {
			keep = mcp.IsToolPolicyAutoRunEverywhere(policy) && enabled
		}

		if !keep {
			remove = append(remove, t.Name)
		}
	}
	store.RemoveTools(remove)
}
