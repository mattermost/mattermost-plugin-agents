// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// ProcessDMRequest runs processDMRequest for convID as a DM turn of bot
// whose requester is present.
func (c *Conversations) ProcessDMRequest(ctx context.Context, convID string, bot *bots.Bot, llmCtx *llm.Context) (*DMStreamResult, error) {
	return c.processDMRequest(ctx, convID, testDMTurnPolicy(c, bot), llmCtx, nil)
}
