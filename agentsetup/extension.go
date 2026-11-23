// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentsetup

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// Extension customizes setup for the agents it claims, such as an agent that
// leads a project channel. Extensions can add instructions and tools, but never
// widen what the setup tools may write: agentconfig enforces that for every
// self_setup write.
type Extension interface {
	// Claims reports whether the extension handles agent's setup. A claimed
	// agent's setup conversation runs even when EnableAgentAutoSetup is off,
	// which gates only the regular-agent entry point.
	Claims(agent *llm.BotConfig) bool
	// CanSetUpInChannel reports whether requesterID may run agent's setup in
	// the channel the agent leads.
	CanSetUpInChannel(ctx context.Context, agent *llm.BotConfig, requesterID string) bool
	// Instructions are appended to the setup system prompt.
	Instructions(agent *llm.BotConfig) string
	// Tools are offered beside the setup tools on every setup turn.
	Tools(ctx context.Context, agent *llm.BotConfig, llmCtx *llm.Context) []llm.Tool
}
