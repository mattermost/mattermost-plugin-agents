// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

func channelArtifactMCPProvider() *selectionHonoringMCPProvider {
	return &selectionHonoringMCPProvider{tools: []llm.Tool{
		testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
		testMCPTool("mattermost__"+channelartifacts.ListToolName, mcp.EmbeddedClientKey, "list saved artifacts"),
		testMCPTool("mattermost__"+channelartifacts.GetToolName, mcp.EmbeddedClientKey, "read a saved artifact"),
		testMCPTool("jira__"+channelartifacts.ListToolName, licenseTestRemoteOrigin, "a remote server's own tool of the same name"),
	}}
}

// TestChannelArtifactToolsOnlyInChannels pins the catalog rule: the library's
// MCP read tools are offered only to a request in a public or private channel.
// Direct and group messages, and requests with no channel, never receive them,
// whether the catalog is built flat or for dynamic tool loading.
func TestChannelArtifactToolsOnlyInChannels(t *testing.T) {
	channelTools := []string{"mattermost__read_channel", "mattermost__list_channel_artifacts", "mattermost__get_channel_artifact", "jira__list_channel_artifacts"}
	otherTools := []string{"mattermost__read_channel", "jira__list_channel_artifacts"}

	tests := []struct {
		name    string
		channel *model.Channel
		want    []string
	}{
		{name: "public channel", channel: &model.Channel{Id: "channel-id", Type: model.ChannelTypeOpen}, want: channelTools},
		{name: "private channel", channel: &model.Channel{Id: "channel-id", Type: model.ChannelTypePrivate}, want: channelTools},
		{name: "direct message", channel: &model.Channel{Id: "channel-id", Type: model.ChannelTypeDirect}, want: otherTools},
		{name: "group message", channel: &model.Channel{Id: "channel-id", Type: model.ChannelTypeGroup}, want: otherTools},
		{name: "no channel", channel: nil, want: otherTools},
	}
	for _, tt := range tests {
		for _, dynamic := range []bool{false, true} {
			name := tt.name
			if dynamic {
				name += " with dynamic tool loading"
			}
			t.Run(name, func(t *testing.T) {
				builder := newLicenseTestBuilderAt(t, enterprise.LevelEnterprise,
					&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
					channelArtifactMCPProvider(),
				)
				bot := newTestBotWithConfig(llm.BotConfig{
					ID: "bot-id", Name: "matty", DisplayName: "Matty",
					AutoEnableNewMCPTools: true, MCPDynamicToolLoading: dynamic,
				})
				c := builder.BuildLLMContextUserRequest(bot, testUser(), tt.channel, builder.WithLLMContextTools(t.Context(), bot))

				if !dynamic {
					require.ElementsMatch(t, append([]string{"builtin"}, tt.want...), toolNames(c.Tools))
					return
				}
				for _, name := range channelTools {
					require.Equal(t, slices.Contains(tt.want, name), c.Tools.IsUnloadedMCPTool(name), name)
				}
			})
		}
	}
}
