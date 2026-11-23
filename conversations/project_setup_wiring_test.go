// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentconfig"
	"github.com/mattermost/mattermost-plugin-agents/v2/agentsetup"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
)

// channelAdminExtension claims every agent and trusts only admins with its
// setup in the channel it leads.
type channelAdminExtension struct{ admins []string }

func (channelAdminExtension) Claims(*llm.BotConfig) bool { return true }
func (x channelAdminExtension) CanSetUpInChannel(_ context.Context, _ *llm.BotConfig, requesterID string) bool {
	return slices.Contains(x.admins, requesterID)
}
func (channelAdminExtension) Instructions(*llm.BotConfig) string { return "" }
func (channelAdminExtension) Tools(context.Context, *llm.BotConfig, *llm.Context) []llm.Tool {
	return nil
}

// channelAdminAuthorizer lets channel admins manage the agent, as the projects
// service's authorizer does for a project agent.
type channelAdminAuthorizer struct{ admins []string }

func (a channelAdminAuthorizer) CanManage(_ context.Context, actorID string, _ *llm.BotConfig) bool {
	return slices.Contains(a.admins, actorID)
}

func (channelAdminAuthorizer) CanManageServiceAccounts(context.Context, string, *llm.BotConfig) bool {
	return false
}

type singleBot struct{ bot *bots.Bot }

func (s singleBot) GetBotByID(id string) *bots.Bot {
	if s.bot.BotUserID() == id {
		return s.bot
	}
	return nil
}

// A project channel's setup thread, started from the create dialog, runs its
// first turn for the creator: a channel admin's setup write runs without
// approval and its round is shared, while anyone else gets no setup tools.
func TestProjectSetupThreadRunsChannelAdminWritesWithoutApproval(t *testing.T) {
	tests := []struct {
		name        string
		admins      []string
		wantWritten bool
	}{
		{name: "channel admin", admins: []string{scopedUserID}, wantWritten: true},
		{name: "member who can't manage the channel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &model.Channel{Id: scopedChannelID, TeamId: scopedTeamID, Type: model.ChannelTypeOpen, Name: "launch"}
			responses := []*llm.TextStreamResult{dmMakeTextStream("What should done look like?")}
			if tt.wantWritten {
				responses = []*llm.TextStreamResult{
					dmMakeToolCallStream([]llm.ToolCall{{
						ID: "call_name", Name: agentsetup.ToolSetMyDisplayName, Arguments: json.RawMessage(`{"display_name":"Launch Crew"}`),
					}}),
					dmMakeTextStream("Renamed."),
				}
			}
			env := setupScopedTestEnv(t, channel, scopedEnvOpts{projectAgent: true}, responses...)
			env.builder.SetProjectRuntime(projectRunRuntime{})

			backend := &setupBackend{agent: env.botConfig}
			backend.agent.ServiceID = "svc-1"
			configs := agentconfig.New(agentconfig.Deps{
				Store: backend, Settings: backend, License: backend, Access: backend, Accounts: backend,
				References: backend, Notifier: backend, Authorizer: channelAdminAuthorizer{admins: tt.admins}, Logger: backend,
			})
			promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
			require.NoError(t, err)
			setup := agentsetup.New(agentsetup.Deps{
				Configs: configs, Threads: env.conversations, Bots: singleBot{bot: env.bot}, Store: backend,
				Config: backend, Prompts: promptsManager, I18n: i18n.Init(), Audit: backend,
			})
			setup.RegisterExtension(channelAdminExtension{admins: tt.admins})
			env.conversations.RegisterOperation(llm.OperationAgentSetup, setup)

			_, err = env.conversations.StartAgentThread(context.Background(), conversations.AgentThreadRequest{
				Bot:              env.bot,
				User:             env.mm.users[scopedUserID],
				Channel:          channel,
				Operation:        llm.OperationAgentSetup,
				Message:          "Setting up **Launch**.",
				Instruction:      "Set up this project from its purpose.",
				RequesterPresent: true,
			})
			require.NoError(t, err)
			select {
			case <-env.streaming.finished:
			case <-time.After(10 * time.Second):
				require.FailNow(t, "the setup turn did not finish")
			}

			env.lm.mu.Lock()
			requests := slices.Clone(env.lm.requests)
			env.lm.mu.Unlock()
			require.NotEmpty(t, requests)
			assert.True(t, requests[0].Context.ToolCatalog.InteractiveUserPresent, "the creator is present for the first turn")
			names := visibleToolNames(requests[0].Context)

			if !tt.wantWritten {
				for _, name := range []string{agentsetup.ToolGetMyConfig, agentsetup.ToolSetMyDisplayName, agentsetup.ToolUpdateMyInstructions, agentsetup.ToolDisableMyTools, agentsetup.ToolCompleteSetup} {
					assert.NotContains(t, names, name, "someone who can't manage the channel gets no setup tools")
				}
				assert.Equal(t, env.botConfig.DisplayName, backend.storedAgent().DisplayName)
				return
			}

			assert.Contains(t, names, agentsetup.ToolSetMyDisplayName)
			assert.Equal(t, "Launch Crew", backend.storedAgent().DisplayName, "the write ran without approval")
			toolUse, toolResult := scopedToolBlocks(t, env)
			require.Len(t, toolUse, 1)
			assert.Equal(t, conversation.StatusAutoApproved, toolUse[0].Status)
			require.NotNil(t, toolUse[0].Shared)
			assert.True(t, *toolUse[0].Shared, "the round is shared, so there is no Share or Keep private step")
			require.Len(t, toolResult, 1)
			require.NotNil(t, toolResult[0].Shared)
			assert.True(t, *toolResult[0].Shared)
		})
	}
}

func TestStartAgentThreadRefusesRequesterPresentOutsideChannels(t *testing.T) {
	tests := []struct {
		name    string
		request func(env *dmTestEnv, bot *bots.Bot) conversations.AgentThreadRequest
	}{
		{
			name: "in the user's DM",
			request: func(env *dmTestEnv, bot *bots.Bot) conversations.AgentThreadRequest {
				return conversations.AgentThreadRequest{Bot: bot, User: env.user, Operation: testThreadOperation, Message: "hi", Instruction: "go", RequesterPresent: true}
			},
		},
		{
			name: "posted as the user",
			request: func(env *dmTestEnv, bot *bots.Bot) conversations.AgentThreadRequest {
				return conversations.AgentThreadRequest{Bot: bot, User: env.user, Channel: env.channel, Operation: testThreadOperation, Message: "hi", PostAsUser: true, RequesterPresent: true}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, bot := setupSearchTestEnv(t, nil, nil)
			_, err := env.conversations.StartAgentThread(context.Background(), tt.request(env, bot))
			require.ErrorIs(t, err, conversations.ErrAgentThreadRequesterPresentInDM)
			assert.Empty(t, env.mmClient.dmPosts, "nothing is posted")
			assert.Empty(t, env.mmClient.createdPosts, "nothing is posted")
		})
	}
}
