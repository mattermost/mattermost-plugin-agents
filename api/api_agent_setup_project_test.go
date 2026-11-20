// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentsetup"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestSetupToolsInProjectChannel(t *testing.T) {
	const setupProjectChannelID = "setupproject00000000000000"
	setupToolNames := []string{agentsetup.ToolGetMyConfig, agentsetup.ToolUpdateMyInstructions, agentsetup.ToolSetMyDisplayName, agentsetup.ToolCompleteSetup}
	channelCtx := func(channelID string, interactive bool) *llm.Context {
		ctx := &llm.Context{
			RequestingUser: &model.User{Id: testUserID},
			Channel:        &model.Channel{Id: channelID, Type: model.ChannelTypeOpen, Name: "project"},
		}
		ctx.ToolCatalog.InteractiveUserPresent = interactive
		return ctx
	}
	tests := []struct {
		name         string
		projectAgent bool
		creatorID    string
		llmCtx       *llm.Context
		wantOffered  bool
	}{
		{name: "project agent in its project channel", projectAgent: true, llmCtx: channelCtx(setupProjectChannelID, true), wantOffered: true},
		{name: "project agent in its channel with nobody present", projectAgent: true, llmCtx: channelCtx(setupProjectChannelID, false)},
		{name: "project agent in another channel", projectAgent: true, llmCtx: channelCtx("otherchannel00000000000000", true)},
		{name: "project agent for a requester who can't manage it", projectAgent: true, creatorID: "someoneelse000000000000000", llmCtx: channelCtx(setupProjectChannelID, true)},
		{name: "regular agent in a channel", llmCtx: channelCtx(setupProjectChannelID, true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupAgentSetupEnv(t, true)
			defer env.Cleanup(t)
			env.setup.RegisterExtension(&projectOnlyExtension{})

			cfg := env.bot.GetConfig()
			if tt.projectAgent {
				cfg.ProjectChannelID = setupProjectChannelID
			}
			if tt.creatorID != "" {
				cfg.CreatorID = tt.creatorID
			}
			stored := cfg
			env.agentStore.agents[setupAgentID] = &stored
			bot := bots.NewBot(cfg, env.bot.GetService(), env.bot.GetMMBot(), nil)

			tools := env.setup.OperationTools(context.Background(), bot, tt.llmCtx)
			if !tt.wantOffered {
				assert.Empty(t, tools)
				return
			}
			for _, name := range setupToolNames {
				setupTool(t, tools, name)
			}

			_, err := callSetupTool(t, setupTool(t, tools, agentsetup.ToolUpdateMyInstructions), tt.llmCtx, map[string]any{"instructions": setupNewPromptText})
			require.NoError(t, err)
			assert.Equal(t, setupNewPromptText, env.agentStore.agents[setupAgentID].CustomInstructions)
		})
	}
}

// projectOnlyExtension claims project agents only, like the projects service.
type projectOnlyExtension struct{}

func (projectOnlyExtension) Claims(agent *llm.BotConfig) bool        { return agent.IsProjectAgent() }
func (projectOnlyExtension) AutoExecute(*llm.BotConfig, string) bool { return false }
func (projectOnlyExtension) Instructions(*llm.BotConfig) string      { return "" }
func (projectOnlyExtension) Tools(context.Context, *llm.BotConfig, *llm.Context) []llm.Tool {
	return nil
}
