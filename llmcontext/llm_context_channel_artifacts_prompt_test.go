// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
)

// TestChannelArtifactPromptHintFollowsTheOfferedTools builds the catalog the
// way every turn does and formats the production system prompt: the agent is
// told about channel libraries only where the tools are offered, so never in a
// direct or group message.
func TestChannelArtifactPromptHintFollowsTheOfferedTools(t *testing.T) {
	promptsEngine, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	tests := []struct {
		name     string
		channel  *model.Channel
		wantHint bool
	}{
		{name: "public channel", channel: &model.Channel{Id: "channel-id", Type: model.ChannelTypeOpen}, wantHint: true},
		{name: "private channel", channel: &model.Channel{Id: "channel-id", Type: model.ChannelTypePrivate}, wantHint: true},
		{name: "direct message", channel: &model.Channel{Id: "channel-id", Type: model.ChannelTypeDirect}},
		{name: "group message", channel: &model.Channel{Id: "channel-id", Type: model.ChannelTypeGroup}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newLicenseTestBuilderAt(t, enterprise.LevelEnterprise,
				&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
				&selectionHonoringMCPProvider{tools: []llm.Tool{
					testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
					testMCPTool("mattermost__"+channelartifacts.ListToolName, mcp.EmbeddedClientKey, "list saved artifacts"),
					testMCPTool("mattermost__"+channelartifacts.GetToolName, mcp.EmbeddedClientKey, "read a saved artifact"),
				}},
			)
			bot := newTestBotWithConfig(llm.BotConfig{ID: "bot-id", Name: "matty", DisplayName: "Matty", AutoEnableNewMCPTools: true})
			c := builder.BuildLLMContextUserRequest(bot, testUser(), tt.channel, builder.WithLLMContextTools(t.Context(), bot))

			prompt, err := promptsEngine.Format(prompts.PromptDirectMessageQuestionSystem, c)
			require.NoError(t, err)
			require.Equal(t, tt.wantHint, c.IsToolCallable("mattermost__"+channelartifacts.ListToolName))
			if tt.wantHint {
				require.Contains(t, prompt, "list_channel_artifacts")
			} else {
				require.NotContains(t, prompt, "list_channel_artifacts")
				require.NotContains(t, prompt, "library of saved interactive HTML pages")
			}
		})
	}
}
