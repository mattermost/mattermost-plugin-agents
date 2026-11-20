// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentconfig"
	"github.com/mattermost/mattermost-plugin-agents/v2/agentsetup"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/projects"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
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

const (
	setupLeadID         = "projectlead000000000000000"
	projectMemberID     = "projectmember0000000000000"
	projectGuestID      = "projectguest00000000000000"
	setupProjectAgentID = "projectagent00000000000000"
	setupProjectBotID   = "projectbot0000000000000000"
	projectChannelP     = "projectchanp00000000000000"
	projectSetupTitle   = "Launch agent"
)

type projectThreadsStub struct{ projects.Threads }

// projectStoreStub serves one project; the projects service calls nothing
// else on the self-setup path.
type projectStoreStub struct {
	projects.Store
	project *store.Project
}

func (s projectStoreStub) GetProject(channelID string) (*store.Project, error) {
	if channelID != s.project.ChannelID {
		return nil, store.ErrProjectNotFound
	}
	cp := *s.project
	return &cp, nil
}

// projectPlatformStub knows the project's users and members, and that nobody
// can manage the channel's roles.
type projectPlatformStub struct {
	projects.Platform
	users   map[string]*model.User
	members []string
}

func (p projectPlatformStub) GetUser(id string) (*model.User, error) {
	if u, ok := p.users[id]; ok {
		return u, nil
	}
	return nil, errors.New("not found")
}

func (p projectPlatformStub) GetChannelMember(channelID, userID string) (*model.ChannelMember, error) {
	if channelID == projectChannelP && slices.Contains(p.members, userID) {
		return &model.ChannelMember{ChannelId: channelID, UserId: userID}, nil
	}
	return nil, errors.New("not a member")
}

func (p projectPlatformStub) HasPermissionToChannel(string, string, *model.Permission) bool {
	return false
}

func (p projectPlatformStub) LogError(string, ...any) {}

func (p projectPlatformStub) BotChannels(string) ([]*model.Channel, error) {
	return []*model.Channel{{Id: projectChannelP, TeamId: "team", Name: "launch"}, {Id: "townsquare0000000000000000", TeamId: "team", Name: model.DefaultChannelName}}, nil
}

type projectAutoReplyStub struct{ projects.AutoReply }

type projectsEnabled struct{}

func (projectsEnabled) EnableProjectChannels() bool     { return true }
func (projectsEnabled) ProjectDefaultServiceID() string { return "svc-1" }
func (projectsEnabled) ProjectDefaultModel() string     { return "" }

func TestProjectLeadSetsUpTheProjectAgentWithoutAgentPermissions(t *testing.T) {
	tests := []struct {
		name      string
		actor     string
		wantSaved bool
	}{
		{name: "project lead", actor: setupLeadID, wantSaved: true},
		{name: "ordinary member", actor: projectMemberID},
		{name: "guest member", actor: projectGuestID},
		{name: "non-member", actor: testUserID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := setupAgentSetupEnv(t, true)
			defer env.Cleanup(t)

			agent := &llm.BotConfig{
				ID: setupProjectAgentID, CreatorID: setupLeadID, BotUserID: setupProjectBotID,
				Name: "project-launch", DisplayName: projectSetupTitle, ServiceID: "svc-1",
				ProjectChannelID: projectChannelP, UseServiceAccountAuth: true, ScopedChannelMode: true,
			}
			env.agentStore.agents[setupProjectAgentID] = agent
			projectBot := bots.NewBot(*agent, llm.ServiceConfig{ID: "svc-1"}, &model.Bot{UserId: setupProjectBotID, Username: agent.Name, DisplayName: projectSetupTitle}, nil)
			env.setBots(env.bot, projectBot)

			service := projects.New(projects.Deps{
				Store: projectStoreStub{project: &store.Project{
					ChannelID: projectChannelP, TeamID: "team", AgentID: setupProjectAgentID, AgentBotUserID: setupProjectBotID,
					State: store.ProjectStateActive, LeadUserID: setupLeadID,
				}},
				Platform: projectPlatformStub{
					users: map[string]*model.User{
						setupLeadID:     {Id: setupLeadID, Roles: model.SystemUserRoleId},
						projectMemberID: {Id: projectMemberID, Roles: model.SystemUserRoleId},
						projectGuestID:  {Id: projectGuestID, Roles: model.SystemGuestRoleId},
						testUserID:      {Id: testUserID, Roles: model.SystemUserRoleId},
					},
					members: []string{setupLeadID, projectMemberID, projectGuestID, setupProjectBotID},
				},
				Bots:      env.bots,
				Threads:   projectThreadsStub{},
				Config:    projectsEnabled{},
				AutoReply: projectAutoReplyStub{},
			})
			configs := env.api.AgentConfigService()
			configs.SetAuthorizer(service.Authorizer(configs.Authorizer()))
			env.setup.RegisterExtension(service)

			llmCtx := &llm.Context{
				RequestingUser: &model.User{Id: tt.actor},
				Channel:        &model.Channel{Id: projectChannelP, Type: model.ChannelTypeOpen, Name: "launch"},
			}
			llmCtx.ToolCatalog.InteractiveUserPresent = true
			tools := env.setup.OperationTools(context.Background(), projectBot, llmCtx)
			if !tt.wantSaved {
				assert.Empty(t, tools)
				// The write path re-checks on its own, so a tool offered to
				// someone else can't be replayed by this actor.
				lead := &llm.Context{RequestingUser: &model.User{Id: setupLeadID}, Channel: llmCtx.Channel}
				lead.ToolCatalog.InteractiveUserPresent = true
				leadTools := env.setup.OperationTools(context.Background(), projectBot, lead)
				_, err := callSetupTool(t, setupTool(t, leadTools, agentsetup.ToolUpdateMyInstructions), llmCtx, map[string]any{"instructions": setupNewPromptText})
				require.Error(t, err)
				assert.Empty(t, env.agentStore.agents[setupProjectAgentID].CustomInstructions)
				return
			}

			_, err := callSetupTool(t, setupTool(t, tools, agentsetup.ToolUpdateMyInstructions), llmCtx, map[string]any{"instructions": setupNewPromptText})
			require.NoError(t, err)
			_, err = callSetupTool(t, setupTool(t, tools, agentsetup.ToolSetMyDisplayName), llmCtx, map[string]any{"display_name": "Launch crew"})
			require.NoError(t, err)
			_, err = callSetupTool(t, setupTool(t, tools, agentsetup.ToolCompleteSetup), llmCtx, map[string]any{})
			require.NoError(t, err)

			saved := env.agentStore.agents[setupProjectAgentID]
			assert.Equal(t, setupNewPromptText, saved.CustomInstructions)
			assert.Equal(t, "Launch crew", saved.DisplayName)
			assert.NotZero(t, saved.SetupCompletedAt)

			_, err = configs.Update(context.Background(), agentconfig.UpdateRequest{
				ActorID: setupLeadID,
				AgentID: setupAgentID,
				Apply:   func(cfg *llm.BotConfig) error { cfg.CustomInstructions = "hijacked"; return nil },
			})
			require.ErrorIs(t, err, agentconfig.ErrForbidden, "the lead can't edit unrelated agents")

			recorder := doRequest(env.api, http.MethodDelete, "/agents/"+setupProjectAgentID, nil, setupLeadID)
			assert.Equal(t, http.StatusBadRequest, recorder.Result().StatusCode, "general agent routes refuse project agents")
		})
	}
}
