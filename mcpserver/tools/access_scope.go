// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

func (p *MattermostToolProvider) ensureChannelAccessible(_ string, scope *llm.MattermostAccessScope, channel *model.Channel) error {
	if scope == nil || channel == nil || scope.AllowsChannel(channel) {
		return nil
	}

	return scope.ChannelDeniedError(channel.Id)
}
