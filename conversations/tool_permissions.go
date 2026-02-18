// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"errors"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
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
