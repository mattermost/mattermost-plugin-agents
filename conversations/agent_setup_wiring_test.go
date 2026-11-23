// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentconfig"
	"github.com/mattermost/mattermost-plugin-agents/v2/agentsetup"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wiringAgentID = "agent_setup_wiring_agent"

// setupBackend is an in-memory agentconfig backend: one agent, every change
// recorded with the version metadata it was written with.
type setupBackend struct {
	mu       sync.Mutex
	agent    llm.BotConfig
	versions []store.AgentVersionMeta
}

func (b *setupBackend) GetAgent(id string) (*llm.BotConfig, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id != b.agent.ID {
		return nil, nil
	}
	cfg := b.agent
	return &cfg, nil
}

func (b *setupBackend) UpdateAgent(cfg *llm.BotConfig, meta store.AgentVersionMeta) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.agent = *cfg
	b.versions = append(b.versions, meta)
	return nil
}

func (b *setupBackend) UpdateAgentIfUnchanged(cfg *llm.BotConfig, _ int64, meta store.AgentVersionMeta) error {
	return b.UpdateAgent(cfg, meta)
}

func (b *setupBackend) PluginConfig() (*config.Config, error) {
	return &config.Config{Services: []llm.ServiceConfig{{ID: "svc-1"}}}, nil
}

func (b *setupBackend) CheckAgentTransition(llm.BotConfig, *llm.BotConfig) error { return nil }

func (b *setupBackend) ValidateAgentWrite(context.Context, string, *llm.BotConfig, *llm.BotConfig) error {
	return nil
}

func (b *setupBackend) DeleteAgentPolicy(context.Context, string, string) error { return nil }

func (b *setupBackend) GetUserByUsername(string) (*model.User, error) {
	return nil, errors.New("unexpected username lookup")
}

func (b *setupBackend) PatchBot(string, *model.BotPatch) error { return nil }

func (b *setupBackend) BindAgentConfigReferences(string, string) error { return nil }

func (b *setupBackend) AgentChanged(string) bool { return true }

func (b *setupBackend) CanManage(_ context.Context, actorID string, agent *llm.BotConfig) bool {
	return actorID == agent.CreatorID
}

func (b *setupBackend) CanManageServiceAccounts(context.Context, string, *llm.BotConfig) bool {
	return false
}

func (b *setupBackend) Error(string, ...any) {}

func (b *setupBackend) MarkAgentSetupCompleted(string, int64) (bool, error) { return true, nil }

func (b *setupBackend) EnableAgentAutoSetup() bool { return true }

func (b *setupBackend) Record(*model.AuditRecord) {}

func (b *setupBackend) storedAgent() llm.BotConfig {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.agent
}

func (b *setupBackend) versionMetas() []store.AgentVersionMeta {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]store.AgentVersionMeta(nil), b.versions...)
}

// wiringExtension claims every agent and adds one tool to its setup turns.
type wiringExtension struct{}

func (wiringExtension) Claims(*llm.BotConfig) bool { return true }
func (wiringExtension) CanSetUpInChannel(context.Context, *llm.BotConfig, string) bool {
	return true
}
func (wiringExtension) Instructions(*llm.BotConfig) string { return "" }
func (wiringExtension) Tools(context.Context, *llm.BotConfig, *llm.Context) []llm.Tool {
	return []llm.Tool{{Name: wiringExtensionTool, Description: "extension tool", Schema: llm.NewJSONSchemaFromStruct[struct{}]()}}
}

const wiringExtensionTool = "wiring_extension_tool"

// setupAgentSetupWiring registers the real setup service on the DM harness
// for an agent the harness user created, with built-in web search offered.
func setupAgentSetupWiring(t *testing.T, responses ...*llm.TextStreamResult) (*dmTestEnv, *setupBackend, *agentsetup.Service) {
	t.Helper()
	env, _ := setupSearchTestEnv(t, nil, func(cfg *llm.BotConfig) {
		cfg.ID = wiringAgentID
		cfg.BotUserID = "bot1"
		cfg.CreatorID = "user12345678901234567890ab"
		cfg.ServiceID = "svc-1"
		cfg.CustomInstructions = "Be brief."
	}, responses...)
	env.toolProvider.tools = []llm.Tool{
		{Name: mmtools.WebSearchToolName, Description: "web search", Schema: llm.NewJSONSchemaFromStruct[struct{}]()},
		{Name: mmtools.WebSearchFetchSourceToolName, Description: "web fetch", Schema: llm.NewJSONSchemaFromStruct[struct{}]()},
		{Name: "CreateFile", Description: "create a file", Schema: llm.NewJSONSchemaFromStruct[struct{}]()},
	}

	backend := &setupBackend{agent: llm.BotConfig{
		ID: wiringAgentID, BotUserID: "bot1", CreatorID: env.userID, ServiceID: "svc-1",
		Name: "ai", DisplayName: "AI", CustomInstructions: "Be brief.",
	}}
	configs := agentconfig.New(agentconfig.Deps{
		Store: backend, Settings: backend, License: backend, Access: backend, Accounts: backend,
		References: backend, Notifier: backend, Authorizer: backend, Logger: backend,
	})
	promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)
	setup := agentsetup.New(agentsetup.Deps{
		Configs: configs, Threads: env.conversations, Bots: env.botService, Store: backend,
		Config: backend, Prompts: promptsManager, I18n: i18n.Init(), Audit: backend,
	})
	setup.RegisterExtension(wiringExtension{})
	env.conversations.RegisterOperation(llm.OperationAgentSetup, setup)
	return env, backend, setup
}

func TestAgentSetupWritesRunInTheRequestersDMWithoutApproval(t *testing.T) {
	env, backend, setup := setupAgentSetupWiring(t,
		dmMakeToolCallStream([]llm.ToolCall{{
			ID: "call_update", Name: agentsetup.ToolUpdateMyInstructions,
			Arguments: json.RawMessage(`{"instructions":"Answer in one sentence."}`),
		}}),
		dmMakeTextStream("Saved."),
	)

	thread, err := setup.Start(context.Background(), env.user, wiringAgentID)
	require.NoError(t, err)
	waitForStreamFinished(t, env)

	assert.Equal(t, "Answer in one sentence.", backend.storedAgent().CustomInstructions)
	versions := backend.versionMetas()
	require.Len(t, versions, 1)
	assert.Equal(t, store.AgentVersionSourceSelfSetup, versions[0].Source)
	assert.Equal(t, env.userID, versions[0].ActorID)

	conv, err := env.convService.GetConversationByThread(thread.RootPost.Id, env.botID, env.userID)
	require.NoError(t, err)
	require.NotNil(t, conv)
	assert.Equal(t, llm.OperationAgentSetup, conv.Operation)

	var sawUse, sawResult bool
	for _, turn := range env.convStore.turnsFor(conv.ID) {
		blocks, unmarshalErr := conversation.UnmarshalBlocks(turn.Content)
		require.NoError(t, unmarshalErr)
		for _, block := range blocks {
			switch block.Type {
			case conversation.BlockTypeToolUse:
				sawUse = true
				assert.Equal(t, agentsetup.ToolUpdateMyInstructions, block.Name)
				assert.NotEqual(t, conversation.StatusPending, block.Status, "the write must not wait for approval")
				assert.Equal(t, conversation.StatusAutoApproved, block.Status)
				require.NotNil(t, block.Shared)
				assert.True(t, *block.Shared, "the round is written shared")
			case conversation.BlockTypeToolResult:
				sawResult = true
				require.NotNil(t, block.Shared)
				assert.True(t, *block.Shared, "the result is shared without a Share or Keep private step")
				assert.NotNil(t, block.DecidedAt)
			}
		}
	}
	assert.True(t, sawUse && sawResult, "the stored turn records the tool round")
}

func TestAgentSetupTurnSendsSetupAndExtensionToolsOnly(t *testing.T) {
	env, _, setup := setupAgentSetupWiring(t, dmMakeTextStream("Hello."))

	_, err := setup.Start(context.Background(), env.user, wiringAgentID)
	require.NoError(t, err)
	waitForStreamFinished(t, env)

	assert.ElementsMatch(t, []string{
		agentsetup.ToolGetMyConfig, agentsetup.ToolUpdateMyInstructions, agentsetup.ToolSetMyDisplayName,
		agentsetup.ToolDisableMyTools, agentsetup.ToolCompleteSetup, wiringExtensionTool,
	}, requestToolNames(t, env, 0), "the catalog's web search, fetch and other tools stay out of a turn that saves on its own")
}
