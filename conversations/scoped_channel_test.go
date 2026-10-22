// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	scopedBotUserID   = "scopedbotid12345678901234a"
	scopedBotUsername = "scopedbot"
	scopedUserID      = "scopeduser1234567890123456"
	scopedChannelID   = "scopedchannel1234567890123"
	scopedTeamID      = "scopedteam1234567890123456"
	scopedRemote      = "https://jira.example.com"
	scopedToolName    = "jira__get_issue"
)

type scopedTestMinter struct {
	mu      sync.Mutex
	minted  []*model.Session
	revoked []string
}

func (m *scopedTestMinter) CreateScopedSession(userID string, _ *model.SessionScope, expiresAt int64) (*model.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := &model.Session{Id: model.NewId(), UserId: userID, Token: model.NewId(), ExpiresAt: expiresAt}
	m.minted = append(m.minted, session)
	return session, nil
}

func (m *scopedTestMinter) RevokeSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked = append(m.revoked, sessionID)
	return nil
}

func (m *scopedTestMinter) snapshot() (minted []*model.Session, revoked []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.Session(nil), m.minted...), append([]string(nil), m.revoked...)
}

// scopedCatalogManager mints through a real ClientManager and serves one
// remote "ask" tool.
type scopedCatalogManager struct {
	*mcp.ClientManager
	tool llm.Tool

	mu       sync.Mutex
	scoped   int
	unscoped int
}

func (m *scopedCatalogManager) GetToolsWithSelection(_ context.Context, req mcp.CatalogRequest, selection mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
	m.mu.Lock()
	if req.Scoped != nil {
		m.scoped++
	} else {
		m.unscoped++
	}
	m.mu.Unlock()
	if !selection.Allows(m.tool.ServerOrigin) {
		return nil, nil
	}
	return []llm.Tool{m.tool}, nil
}

type scopedBuilderConfig struct{}

func (scopedBuilderConfig) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

func (scopedBuilderConfig) EnableChannelMentionToolCalling() bool { return true }

type scopedTestEnv struct {
	conversations *conversations.Conversations
	channel       *model.Channel
	trigger       *model.Post
	convStore     *fakeConvStore
	streaming     *fakeStreamingService
	minter        *scopedTestMinter
	catalog       *scopedCatalogManager

	toolRuns int
	runsMu   sync.Mutex

	auditMu sync.Mutex
	audits  []*model.AuditRecord
}

func (env *scopedTestEnv) runs() int {
	env.runsMu.Lock()
	defer env.runsMu.Unlock()
	return env.toolRuns
}

func (env *scopedTestEnv) auditRecords(event string) []*model.AuditRecord {
	env.auditMu.Lock()
	defer env.auditMu.Unlock()
	var out []*model.AuditRecord
	for _, rec := range env.audits {
		if rec.EventName == event {
			out = append(out, rec)
		}
	}
	return out
}

func setupScopedTestEnv(t *testing.T, channel *model.Channel, llmResponses ...*llm.TextStreamResult) *scopedTestEnv {
	t.Helper()
	env := &scopedTestEnv{minter: &scopedTestMinter{}, streaming: &fakeStreamingService{finished: make(chan string, 4)}}

	mockAPI := &plugintest.API{}
	defaultLocale := "en"
	mockAPI.On("GetConfig").Return(&model.Config{LocalizationSettings: model.LocalizationSettings{DefaultServerLocale: &defaultLocale}}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterprise}).Maybe()
	mockAPI.On("GetServerVersion").Return(scopedsession.MinServerVersion).Maybe()
	mockAPI.On("GetTeam", mock.Anything).Return(&model.Team{Id: scopedTeamID, Name: "team"}, nil).Maybe()
	mockAPI.On("GetBotUsers").Return(nil, nil).Maybe()
	mockAPI.On("HasPermissionToChannel", mock.Anything, mock.Anything, model.PermissionReadChannel).Return(true).Maybe()
	mockAPI.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
	mockAPI.On("LogAuditRec", mock.Anything).Run(func(args mock.Arguments) {
		env.auditMu.Lock()
		defer env.auditMu.Unlock()
		env.audits = append(env.audits, args.Get(0).(*model.AuditRecord))
	}).Maybe().Return()
	for i := 1; i <= 12; i++ {
		args := make([]any, i)
		for j := range args {
			args[j] = mock.Anything
		}
		mockAPI.On("LogDebug", args...).Maybe()
		mockAPI.On("LogInfo", args...).Maybe()
		mockAPI.On("LogWarn", args...).Maybe()
		mockAPI.On("LogError", args...).Maybe()
	}
	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)

	tool := llm.Tool{
		Name:         scopedToolName,
		Description:  "fetch a Jira issue",
		ServerOrigin: scopedRemote,
		Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
		Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
			env.runsMu.Lock()
			defer env.runsMu.Unlock()
			env.toolRuns++
			return "JIRA-1 is the evidence", nil
		},
	}
	manager := mcp.NewClientManager(mcp.Config{IdleTimeoutMinutes: 30}, client.Log, client, nil, nil, http.DefaultClient, nil, mcp.RemoteMCPAlwaysAllowed)
	t.Cleanup(manager.Close)
	manager.SetScopedSessions(env.minter, scopedsession.NewRegistry(nil))
	env.catalog = &scopedCatalogManager{ClientManager: manager, tool: tool}

	botService := bots.New(mockAPI, client, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	conversations.SetBotsWithAccounts(mockAPI, botService, bots.NewBot(
		llm.BotConfig{
			ID:                    "scoped-agent",
			Name:                  scopedBotUsername,
			DisplayName:           "Scoped Bot",
			ChannelAccessLevel:    llm.ChannelAccessLevelAll,
			UserAccessLevel:       llm.UserAccessLevelAll,
			UseServiceAccountAuth: true,
			ScopedChannelMode:     true,
			EnabledMCPTools:       []llm.EnabledMCPTool{{ServerOrigin: scopedRemote, ToolName: llm.MCPServerToolWildcard}},
		},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: scopedBotUserID, Username: scopedBotUsername, DisplayName: "Scoped Bot"},
		newDMTestLLM(llmResponses...),
	))

	mmClient := &fakeMMClient{
		users: map[string]*model.User{
			scopedUserID: {Id: scopedUserID, Username: "alice", Locale: "en"},
		},
		channels:        map[string]*model.Channel{channel.Id: channel},
		kv:              make(map[string]any),
		allowCreatePost: true,
		config:          &model.Config{LocalizationSettings: model.LocalizationSettings{DefaultServerLocale: &defaultLocale}},
	}
	promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)
	env.convStore = newFakeConvStore()

	contextBuilder := llmcontext.NewLLMContextBuilder(client, &testToolProvider{}, env.catalog, scopedBuilderConfig{})
	policies := newDMPolicyChecker()
	policies.policies[scopedRemote+":get_issue"] = mcp.ToolPolicyAsk
	env.conversations = conversations.New(promptsManager, mmClient, env.streaming, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil,
		&testToolCallingConfig{enableChannelMentionToolCalling: true}, policies)
	env.conversations.SetConversationService(conversation.NewService(env.convStore, promptsManager, mmClient, botService))

	post := &model.Post{Id: model.NewId(), ChannelId: channel.Id, UserId: scopedUserID, CreateAt: 100, Message: "@" + scopedBotUsername + " find the evidence"}
	mmClient.postThreads = map[string]*model.PostList{post.Id: {Order: []string{post.Id}, Posts: map[string]*model.Post{post.Id: post}}}
	mmClient.posts = map[string]*model.Post{post.Id: post}
	env.channel, env.trigger = channel, post
	env.conversations.MessageHasBeenPosted(nil, post)

	select {
	case <-env.streaming.finished:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "response stream did not finish")
	}
	return env
}

func scopedToolBlocks(t *testing.T, env *scopedTestEnv) (toolUse, toolResult []conversation.ContentBlock) {
	t.Helper()
	convs := allConversations(env.convStore)
	require.Len(t, convs, 1)
	for _, turn := range env.convStore.turnsFor(convs[0].ID) {
		var blocks []conversation.ContentBlock
		require.NoError(t, json.Unmarshal(turn.Content, &blocks))
		for _, block := range blocks {
			switch block.Type {
			case conversation.BlockTypeToolUse:
				toolUse = append(toolUse, block)
			case conversation.BlockTypeToolResult:
				toolResult = append(toolResult, block)
			}
		}
	}
	return toolUse, toolResult
}

// In a channel, a scoped agent runs an "ask" tool without approval and
// shares its result; the same agent in a DM still waits for approval.
func TestScopedChannelTurnAutoRunsTools(t *testing.T) {
	toolCall := []llm.ToolCall{{ID: "call-1", Name: scopedToolName, ServerOrigin: scopedRemote, Arguments: json.RawMessage(`{}`)}}

	t.Run("channel mention runs on a scoped session without approval", func(t *testing.T) {
		channel := &model.Channel{Id: scopedChannelID, TeamId: scopedTeamID, Type: model.ChannelTypePrivate, Name: "evidence"}
		env := setupScopedTestEnv(t, channel, dmMakeToolCallStream(toolCall), dmMakeTextStream("JIRA-1 has it"))

		assert.Equal(t, 1, env.runs(), "the ask tool runs without approval")
		assert.Equal(t, 0, env.catalog.unscoped, "a scoped turn never builds the requester's catalog")
		assert.Positive(t, env.catalog.scoped)

		toolUse, toolResult := scopedToolBlocks(t, env)
		require.Len(t, toolUse, 1)
		assert.Equal(t, conversation.StatusAutoApproved, toolUse[0].Status)
		require.NotNil(t, toolUse[0].Shared)
		assert.True(t, *toolUse[0].Shared, "scoped results are shared with the channel without a Share step")
		require.Len(t, toolResult, 1)
		require.NotNil(t, toolResult[0].Shared)
		assert.True(t, *toolResult[0].Shared)

		minted, _ := env.minter.snapshot()
		require.Len(t, minted, 1)
		assert.Equal(t, scopedBotUserID, minted[0].UserId)
		require.Eventually(t, func() bool {
			_, revoked := env.minter.snapshot()
			return len(revoked) == 1 && revoked[0] == minted[0].Id
		}, 5*time.Second, 10*time.Millisecond, "the session is revoked when the run ends")

		grants := env.auditRecords(audit.EventAgentScopedSessionGrant)
		require.Len(t, grants, 1)
		assert.Equal(t, scopedUserID, grants[0].Actor.UserId)
		calls := env.auditRecords(audit.EventAgentScopedToolCall)
		require.Len(t, calls, 1)
		assert.Equal(t, scopedUserID, calls[0].Actor.UserId, "the record names the human who triggered the run")
		assert.Equal(t, model.AuditStatusSuccess, calls[0].Status)
		params := calls[0].EventData.Parameters
		assert.Equal(t, scopedToolName, params[audit.KeyToolName])
		assert.Equal(t, scopedChannelID, params[audit.KeyChannelID])
		assert.Equal(t, allConversations(env.convStore)[0].ID, params[audit.KeyConversationID])
		encoded, err := json.Marshal(calls[0])
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "JIRA-1 is the evidence", "tool results never enter audit records")
	})

	t.Run("DM still waits for approval", func(t *testing.T) {
		channel := &model.Channel{Id: scopedChannelID, Type: model.ChannelTypeDirect, Name: scopedBotUserID + "__" + scopedUserID}
		env := setupScopedTestEnv(t, channel, dmMakeToolCallStream(toolCall))

		assert.Equal(t, 0, env.runs())
		minted, _ := env.minter.snapshot()
		assert.Empty(t, minted, "DMs never run on a scoped session")
		assert.Empty(t, env.auditRecords(audit.EventAgentScopedToolCall))
		toolUse, _ := scopedToolBlocks(t, env)
		assert.Empty(t, toolUse, "a call awaiting approval is neither run nor persisted as a tool round")
	})
}

// Regenerating and resuming a scoped channel turn mint a fresh session for
// each run and revoke it when that run ends.
func TestScopedChannelTurnResumes(t *testing.T) {
	toolCall := []llm.ToolCall{{ID: "call-1", Name: scopedToolName, ServerOrigin: scopedRemote, Arguments: json.RawMessage(`{}`)}}
	channel := &model.Channel{Id: scopedChannelID, TeamId: scopedTeamID, Type: model.ChannelTypeOpen, Name: "town-square"}
	env := setupScopedTestEnv(t, channel,
		dmMakeToolCallStream(toolCall), dmMakeTextStream("first"),
		dmMakeToolCallStream(toolCall), dmMakeTextStream("regenerated"),
		dmMakeTextStream("resumed"),
	)
	require.Equal(t, 1, env.runs())
	conv := allConversations(env.convStore)[0]

	requireAllRevoked := func(t *testing.T, mints int) {
		t.Helper()
		require.Eventually(t, func() bool {
			minted, revoked := env.minter.snapshot()
			return len(minted) == mints && len(revoked) == mints
		}, 5*time.Second, 10*time.Millisecond, "every run's session is revoked when it ends")
	}
	requireAllRevoked(t, 1)

	t.Run("regenerate", func(t *testing.T) {
		regenPost := &model.Post{Id: model.NewId(), UserId: scopedBotUserID, ChannelId: channel.Id, RootId: env.trigger.Id}
		regenPost.AddProp(streaming.ConversationIDProp, conv.ID)
		regenPost.AddProp(streaming.RespondingToProp, env.trigger.Id)

		require.NoError(t, env.conversations.HandleRegenerate(context.Background(), scopedUserID, regenPost, channel))
		<-env.streaming.finished

		assert.Equal(t, 2, env.runs(), "the regenerated run auto-runs the tool too")
		requireAllRevoked(t, 2)
	})

	t.Run("tool approval resume", func(t *testing.T) {
		approvalPostID := model.NewId()
		blocks, err := json.Marshal([]conversation.ContentBlock{{
			Type:             conversation.BlockTypeToolUse,
			ID:               "call-2",
			Name:             scopedToolName,
			ServerOrigin:     scopedRemote,
			Input:            json.RawMessage(`{}`),
			Status:           conversation.StatusPending,
			WouldAutoExecute: true,
		}})
		require.NoError(t, err)
		require.NoError(t, env.convStore.CreateTurnAutoSequence(&store.Turn{
			ID:             model.NewId(),
			ConversationID: conv.ID,
			PostID:         &approvalPostID,
			Role:           "assistant",
			Content:        blocks,
		}))
		approvalPost := &model.Post{Id: approvalPostID, UserId: scopedBotUserID, ChannelId: channel.Id, RootId: env.trigger.Id}
		approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)

		require.NoError(t, env.conversations.HandleToolCall(context.Background(), scopedUserID, approvalPost, channel, nil, nil))
		<-env.streaming.finished

		assert.Equal(t, 3, env.runs(), "the resumed call runs on the re-derived scoped catalog")
		// One session executes the decision, one streams the follow-up.
		requireAllRevoked(t, 4)
		assert.Len(t, env.auditRecords(audit.EventAgentScopedToolCall), 3)
		assert.Equal(t, 0, env.catalog.unscoped)
	})
}
