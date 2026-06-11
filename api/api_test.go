// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversations"
	"github.com/mattermost/mattermost-plugin-agents/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/metrics"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	prompttemplates "github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/mattermost/mattermost-plugin-agents/public/bridgeclient"
	"github.com/mattermost/mattermost-plugin-agents/search"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Test ID constants (26-char alphanumeric to match Mattermost ID format)
const (
	testBotUserID      = "abcdefghijklmnopqrstuvwxyz"
	testUserID         = "user12345678901234567890ab"
	testChannelID      = "chan12345678901234567890ab"
	testOtherUserID    = "othe12345678901234567890ab"
	testNonexistentBot = "none12345678901234567890ab"
)

type TestEnvironment struct {
	api               *API
	mockAPI           *plugintest.API
	bots              *bots.MMBots
	config            *testConfigImpl
	client            *pluginapi.Client
	conversationStore *mockConversationStore
	agentStore        *mockAgentStore
	mcp               *mockMCPClientManager
}

// testConfigImpl is a minimal implementation of Config for testing
type testConfigImpl struct {
	allowUnsafeLinks                bool
	enableChannelMentionToolCalling bool
	mcpConfig                       mcp.Config
}

func (tc *testConfigImpl) GetDefaultBotName() string {
	return "ai"
}

func (tc *testConfigImpl) MCP() mcp.Config {
	return tc.mcpConfig
}

func (tc *testConfigImpl) AllowUnsafeLinks() bool {
	return tc.allowUnsafeLinks
}

func (tc *testConfigImpl) EmbeddingSearchConfig() embeddings.EmbeddingSearchConfig {
	return embeddings.EmbeddingSearchConfig{}
}

func (tc *testConfigImpl) EnableChannelMentionToolCalling() bool {
	return tc.enableChannelMentionToolCalling
}

func (tc *testConfigImpl) AllowNativeWebSearchInChannels() bool {
	return false
}

type testLLMContextToolProvider struct {
	tools []llm.Tool
}

func (p *testLLMContextToolProvider) GetTools(_ *bots.Bot) []llm.Tool {
	return p.tools
}

type testLLMContextConfigProvider struct{}

func (p *testLLMContextConfigProvider) GetServiceByID(_ string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

type mcpDisconnectCall struct {
	userID     string
	serverName string
}

// mockMCPClientManager is a minimal implementation of MCPClientManager for testing
type mockMCPClientManager struct {
	oauthManager        *mcp.OAuthManager
	tools               []llm.Tool
	mcpErrors           *mcp.Errors
	config              mcp.Config
	embeddedServer      mcp.EmbeddedMCPServer
	processOAuthSession *mcp.OAuthSession
	processOAuthErr     error
	disconnectCalls     []mcpDisconnectCall
	disconnectErr       error
	oauthNeededCalls    []mcpDisconnectCall
	ensureSessionErr    error

	registerCalls   []mcp.PluginServerConfig
	unregisterCalls []string
	pluginServers   []mcp.PluginServerConfig
	// orphanPluginIDs simulates entries present in pluginServers but with
	// no live source-plugin registration (hydrated from persisted config).
	orphanPluginIDs map[string]bool

	discoverPluginToolsResponse  []mcp.ToolInfo
	discoverPluginToolsErr       error
	discoverPluginToolsCallCount int
}

func newTestMCPClientManager(t *testing.T) *mockMCPClientManager {
	t.Helper()
	mockClient := mmapimocks.NewMockClient(t)
	mockClient.EXPECT().KVGet(mock.Anything, mock.Anything).Return(nil).Maybe()
	return &mockMCPClientManager{
		oauthManager: mcp.NewOAuthManager(mockClient, "", &http.Client{}, nil),
	}
}

func (m *mockMCPClientManager) GetOAuthManager() *mcp.OAuthManager {
	return m.oauthManager
}

func (m *mockMCPClientManager) GetToolsCache() *mcp.ToolsCache {
	return nil
}

func (m *mockMCPClientManager) ProcessOAuthCallback(ctx context.Context, loggedInUserID, state, code string) (*mcp.OAuthSession, error) {
	return m.processOAuthSession, m.processOAuthErr
}

func (m *mockMCPClientManager) DisconnectUserOAuth(userID, serverName string) error {
	m.disconnectCalls = append(m.disconnectCalls, mcpDisconnectCall{
		userID:     userID,
		serverName: serverName,
	})
	return m.disconnectErr
}

func (m *mockMCPClientManager) MarkOAuthNeeded(userID, serverName, authURL string) error {
	m.oauthNeededCalls = append(m.oauthNeededCalls, mcpDisconnectCall{
		userID:     userID,
		serverName: serverName,
	})
	return nil
}

func (m *mockMCPClientManager) GetEmbeddedServer() mcp.EmbeddedMCPServer {
	return m.embeddedServer
}

func (m *mockMCPClientManager) EnsureMCPSessionID(userID string) (string, error) {
	if m.ensureSessionErr != nil {
		return "", m.ensureSessionErr
	}
	return "mock-session-id", nil
}

func (m *mockMCPClientManager) GetHTTPClient() *http.Client {
	return nil
}

func (m *mockMCPClientManager) GetToolsForUser(context.Context, string) ([]llm.Tool, *mcp.Errors) {
	return m.tools, m.mcpErrors
}

func (m *mockMCPClientManager) GetConfig() mcp.Config {
	return m.config
}

func (m *mockMCPClientManager) RegisterPluginServer(cfg mcp.PluginServerConfig) {
	m.registerCalls = append(m.registerCalls, cfg)
	// Mirror real ClientManager: same PluginID replaces existing entry.
	for i, existing := range m.pluginServers {
		if existing.PluginID == cfg.PluginID {
			m.pluginServers[i] = cfg
			return
		}
	}
	m.pluginServers = append(m.pluginServers, cfg)
}

func (m *mockMCPClientManager) UnregisterPluginServer(pluginID string) {
	m.unregisterCalls = append(m.unregisterCalls, pluginID)
	for i, existing := range m.pluginServers {
		if existing.PluginID == pluginID {
			m.pluginServers = append(m.pluginServers[:i], m.pluginServers[i+1:]...)
			return
		}
	}
}

func (m *mockMCPClientManager) ListPluginServers() []mcp.PluginServerConfig {
	out := make([]mcp.PluginServerConfig, len(m.pluginServers))
	copy(out, m.pluginServers)
	return out
}

func (m *mockMCPClientManager) GetPluginServer(pluginID string) (mcp.PluginServerConfig, bool) {
	for _, existing := range m.pluginServers {
		if existing.PluginID == pluginID {
			return existing, true
		}
	}
	return mcp.PluginServerConfig{}, false
}

func (m *mockMCPClientManager) IsPluginRegistered(pluginID string) bool {
	if m.orphanPluginIDs[pluginID] {
		return false
	}
	for _, existing := range m.pluginServers {
		if existing.PluginID == pluginID {
			return true
		}
	}
	return false
}

func (m *mockMCPClientManager) DiscoverPluginServerTools(ctx context.Context, userID string, cfg mcp.PluginServerConfig) ([]mcp.ToolInfo, error) {
	m.discoverPluginToolsCallCount++
	return m.discoverPluginToolsResponse, m.discoverPluginToolsErr
}

type fakeMCPOAuthClusterNotifier struct {
	calls []string
	err   error
}

func (f *fakeMCPOAuthClusterNotifier) PublishMCPOAuthUpdate(userID string) error {
	f.calls = append(f.calls, userID)
	return f.err
}

// mockConversationStore is a simple in-memory implementation of ConversationStore for API-layer tests.
type mockConversationStore struct {
	conversations map[string]*store.Conversation
	turns         map[string][]store.Turn // keyed by conversation ID
	turnsByPost   map[string]*store.Turn  // keyed by post ID
	err           error                   // if set, all methods return this error
}

func newMockConversationStore() *mockConversationStore {
	return &mockConversationStore{
		conversations: make(map[string]*store.Conversation),
		turns:         make(map[string][]store.Turn),
		turnsByPost:   make(map[string]*store.Turn),
	}
}

func (m *mockConversationStore) GetConversation(id string) (*store.Conversation, error) {
	if m.err != nil {
		return nil, m.err
	}
	conv, ok := m.conversations[id]
	if !ok {
		return nil, store.ErrConversationNotFound
	}
	return conv, nil
}

func (m *mockConversationStore) GetTurnsForConversation(conversationID string) ([]store.Turn, error) {
	if m.err != nil {
		return nil, m.err
	}
	turns, ok := m.turns[conversationID]
	if !ok {
		return []store.Turn{}, nil
	}
	return turns, nil
}

func (m *mockConversationStore) GetTurnByPostID(postID string) (*store.Turn, error) {
	if m.err != nil {
		return nil, m.err
	}
	turn, ok := m.turnsByPost[postID]
	if !ok {
		return nil, nil
	}
	return turn, nil
}

func (m *mockConversationStore) UpdateTurnContent(id string, content json.RawMessage) error {
	if m.err != nil {
		return m.err
	}
	for convID, turns := range m.turns {
		for i, turn := range turns {
			if turn.ID == id {
				m.turns[convID][i].Content = content
				return nil
			}
		}
	}
	return nil
}

func (m *mockConversationStore) GetConversationSummariesForUser(_ string, _, _ int) ([]store.ConversationSummary, error) {
	if m.err != nil {
		return nil, m.err
	}
	return []store.ConversationSummary{}, nil
}

// mockAgentStore is a minimal in-memory implementation of AgentStore for testing.
type mockAgentStore struct {
	agents map[string]*llm.BotConfig
}

func newMockAgentStore() *mockAgentStore {
	return &mockAgentStore{agents: make(map[string]*llm.BotConfig)}
}

// cloneBotConfig returns a deep copy so API callers cannot mutate mock store internals via returned pointers.
func cloneBotConfig(src *llm.BotConfig) *llm.BotConfig {
	if src == nil {
		return nil
	}
	dst := *src
	if len(src.ChannelIDs) > 0 {
		dst.ChannelIDs = append([]string(nil), src.ChannelIDs...)
	}
	if len(src.UserIDs) > 0 {
		dst.UserIDs = append([]string(nil), src.UserIDs...)
	}
	if len(src.TeamIDs) > 0 {
		dst.TeamIDs = append([]string(nil), src.TeamIDs...)
	}
	if len(src.AdminUserIDs) > 0 {
		dst.AdminUserIDs = append([]string(nil), src.AdminUserIDs...)
	}
	if len(src.EnabledMCPTools) > 0 {
		dst.EnabledMCPTools = append([]llm.EnabledMCPTool(nil), src.EnabledMCPTools...)
	}
	if len(src.EnabledNativeTools) > 0 {
		dst.EnabledNativeTools = append([]string(nil), src.EnabledNativeTools...)
	}
	return &dst
}

func (m *mockAgentStore) CreateAgent(cfg *llm.BotConfig) error {
	cfg.ID = "agen" + fmt.Sprintf("%022d", len(m.agents)+1)
	now := time.Now().UnixMilli()
	cfg.CreateAt = now
	cfg.UpdateAt = now
	m.agents[cfg.ID] = cloneBotConfig(cfg)
	return nil
}

func (m *mockAgentStore) GetAgent(id string) (*llm.BotConfig, error) {
	cfg, ok := m.agents[id]
	if !ok || cfg.DeleteAt != 0 {
		return nil, nil
	}
	return cloneBotConfig(cfg), nil
}

func (m *mockAgentStore) ListAgents() ([]*llm.BotConfig, error) {
	result := make([]*llm.BotConfig, 0, len(m.agents))
	for _, cfg := range m.agents {
		if cfg.DeleteAt == 0 {
			result = append(result, cloneBotConfig(cfg))
		}
	}
	return result, nil
}

func (m *mockAgentStore) ListAgentsByCreator(creatorID string) ([]*llm.BotConfig, error) {
	result := make([]*llm.BotConfig, 0)
	for _, cfg := range m.agents {
		if cfg.DeleteAt == 0 && cfg.CreatorID == creatorID {
			result = append(result, cloneBotConfig(cfg))
		}
	}
	return result, nil
}

func (m *mockAgentStore) CountActiveAgents() (int, error) {
	count := 0
	for _, cfg := range m.agents {
		if cfg.DeleteAt == 0 {
			count++
		}
	}
	return count, nil
}

func (m *mockAgentStore) UpdateAgent(cfg *llm.BotConfig) error {
	existing, ok := m.agents[cfg.ID]
	if !ok || existing.DeleteAt != 0 {
		return fmt.Errorf("agent %q not found or already deleted", cfg.ID)
	}
	cfg.UpdateAt = time.Now().UnixMilli()
	m.agents[cfg.ID] = cloneBotConfig(cfg)
	return nil
}

func (m *mockAgentStore) DeleteAgent(id string) error {
	cfg, ok := m.agents[id]
	if !ok || cfg.DeleteAt != 0 {
		return fmt.Errorf("agent %q not found or already deleted", id)
	}
	cfg.DeleteAt = time.Now().UnixMilli()
	return nil
}

func (e *TestEnvironment) Cleanup(t *testing.T) {
	if e.mockAPI != nil {
		e.mockAPI.AssertExpectations(t)
	}
}

// OverrideLicense replaces the default GetLicense mock registered by
// SetupTestEnvironment so that tests can control the license value.
// Testify matches the first registered expectation, so simply adding
// a new On("GetLicense") does not override the default.
func (e *TestEnvironment) OverrideLicense(license *model.License) {
	filtered := make([]*mock.Call, 0, len(e.mockAPI.ExpectedCalls))
	for _, call := range e.mockAPI.ExpectedCalls {
		if call.Method != "GetLicense" {
			filtered = append(filtered, call)
		}
	}
	e.mockAPI.ExpectedCalls = filtered
	e.mockAPI.On("GetLicense").Return(license).Maybe()
}

// OverrideConfig replaces the default GetConfig mock registered by
// SetupTestEnvironment so that tests can control the config value.
func (e *TestEnvironment) OverrideConfig(config *model.Config) {
	filtered := make([]*mock.Call, 0, len(e.mockAPI.ExpectedCalls))
	for _, call := range e.mockAPI.ExpectedCalls {
		if call.Method != "GetConfig" {
			filtered = append(filtered, call)
		}
	}
	e.mockAPI.ExpectedCalls = filtered
	e.mockAPI.On("GetConfig").Return(config).Maybe()
}

// CreateBridgeClient creates a bridge client that uses the test API
func (e *TestEnvironment) CreateBridgeClient() *bridgeclient.Client {
	// Create a plugin API wrapper that routes to our test API
	pluginAPI := &testPluginAPI{
		api: e.api,
	}
	return bridgeclient.NewClient(pluginAPI)
}

// testPluginAPI wraps the test API to implement bridgeclient.PluginAPI
type testPluginAPI struct {
	api *API
}

func (t *testPluginAPI) PluginHTTP(req *http.Request) *http.Response {
	// Add inter-plugin authentication header
	req.Header.Set("Mattermost-Plugin-ID", "test-plugin")

	// Strip plugin ID prefix from path (e.g., /mattermost-ai/bridge/... -> /bridge/...)
	// The real PluginHTTP strips the first path component
	path := req.URL.Path
	if idx := strings.Index(path[1:], "/"); idx != -1 {
		req.URL.Path = path[1+idx:]
	}

	recorder := httptest.NewRecorder()
	t.api.ServeHTTP(&plugin.Context{}, recorder, req)
	return recorder.Result()
}

// createTestBots creates a test MMBots instance for testing
func createTestBots(mockAPI *plugintest.API, client *pluginapi.Client) *bots.MMBots {
	licenseChecker := enterprise.NewLicenseChecker(client)
	testBots := bots.New(mockAPI, client, licenseChecker, nil, nil, &http.Client{}, nil)
	return testBots
}

// setupTestBot configures a test bot in the environment
func (e *TestEnvironment) setupTestBot(botConfig llm.BotConfig) {
	// Create a mock bot user
	mmBot := &model.Bot{
		UserId:      testBotUserID,
		Username:    botConfig.Name,
		DisplayName: botConfig.DisplayName,
	}

	// Create the bot instance
	bot := bots.NewBot(botConfig, llm.ServiceConfig{}, mmBot, nil)

	// Set the bot directly for testing
	e.bots.SetBotsForTesting([]*bots.Bot{bot})
}

func SetupTestEnvironment(t *testing.T) *TestEnvironment {
	mockAPI := &plugintest.API{}
	noopMetrics := &metrics.NoopMetrics{}

	client := pluginapi.NewClient(mockAPI, nil)
	llmPrompts, err := llm.NewPrompts(prompttemplates.PromptsFolder)
	require.NoError(t, err)

	contextBuilder := llmcontext.NewLLMContextBuilder(
		client,
		&testLLMContextToolProvider{},
		nil,
		&testLLMContextConfigProvider{},
	)

	// Create test bots instance
	testBots := createTestBots(mockAPI, client)

	cfg := &testConfigImpl{}
	mockConvStore := newMockConversationStore()
	agentStore := newMockAgentStore()
	mcpMgr := newTestMCPClientManager(t)

	// Allow arbitrary log calls from subsystems used in tests (e.g. MCP discovery).
	for i := 1; i <= 20; i++ {
		args := make([]interface{}, i)
		for j := range args {
			args[j] = mock.Anything
		}
		mockAPI.On("LogDebug", args...).Maybe()
		mockAPI.On("LogInfo", args...).Maybe()
		mockAPI.On("LogWarn", args...).Maybe()
		mockAPI.On("LogError", args...).Maybe()
	}

	// Mock GetConfig and GetLicense for WithLLMContextServerInfo used in bridge context building.
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return((*model.License)(nil)).Maybe()

	conversationsService := conversations.New(
		llmPrompts,
		nil,
		nil,
		contextBuilder,
		testBots,
		nil,
		nil,
		nil,
		nil,
		cfg,
	)

	api := New(
		testBots,
		conversationsService,
		nil,
		nil,
		nil,
		client,
		noopMetrics,
		contextBuilder,
		cfg,
		llmPrompts,
		nil,
		nil,
		nil,
		nil,
		nil,
		mcpMgr,
		nil,
		nil,
		nil,
		agentStore,
		nil,
		nil,
		nil,
		nil,
		nil,
		mockConvStore,
		nil,
		nil,
	)

	return &TestEnvironment{
		api:               api,
		mockAPI:           mockAPI,
		bots:              testBots,
		config:            cfg,
		client:            client,
		conversationStore: mockConvStore,
		agentStore:        agentStore,
		mcp:               mcpMgr,
	}
}

func TestAIBotRequiredUsesConfiguredDefaultBot(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	defaultBot := bots.NewBot(
		llm.BotConfig{Name: "ai", DisplayName: "AI"},
		llm.ServiceConfig{},
		&model.Bot{UserId: "defaultbotuserid1234567890", Username: "ai", DisplayName: "AI"},
		nil,
	)
	otherBot := bots.NewBot(
		llm.BotConfig{Name: "second", DisplayName: "Second"},
		llm.ServiceConfig{},
		&model.Bot{UserId: "secondbotuserid123456789", Username: "second", DisplayName: "Second"},
		nil,
	)

	// Put the non-default bot first to verify we prefer config over slice order.
	e.bots.SetBotsForTesting([]*bots.Bot{otherBot, defaultBot})

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/post/postid/react", nil)
	ctx.Request = req

	e.api.aiBotRequired(ctx)
	require.False(t, ctx.IsAborted())

	selectedBot := ctx.MustGet(ContextBotKey).(*bots.Bot)
	require.Equal(t, "ai", selectedBot.GetMMBot().Username)
}

func TestPostRouter(t *testing.T) {
	// This just makes gin not output a whole bunch of debug stuff.
	// maybe pipe this to the test log?
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for urlName, url := range map[string]string{
		"react":                   "/post/postid/react",
		"summarize":               "/post/postid/analyze",
		"transcribe":              "/post/postid/transcribe/file/fileid",
		"summarize_transcription": "/post/postid/summarize_transcription",
		"stop":                    "/post/postid/stop",
		"regenerate":              "/post/postid/regenerate",
		"loop_in_agent":           "/post/postid/loop_in_agent",
	} {
		for name, test := range map[string]struct {
			request        *http.Request
			expectedStatus int
			botconfig      llm.BotConfig
			envSetup       func(e *TestEnvironment)
		}{
			"no permission to channel": {
				request:        httptest.NewRequest(http.MethodPost, url, nil),
				expectedStatus: http.StatusForbidden,
				envSetup: func(e *TestEnvironment) {
					e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
						Id:     "channelid",
						Type:   model.ChannelTypeOpen,
						TeamId: "teamid",
					}, nil)
					e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(false)
				},
			},
			"user not allowed": {
				request:        httptest.NewRequest(http.MethodPost, url, nil),
				expectedStatus: http.StatusForbidden,
				botconfig: llm.BotConfig{
					UserAccessLevel: llm.UserAccessLevelBlock,
					UserIDs:         []string{"userid"},
				},
				envSetup: func(e *TestEnvironment) {
					e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
						Id:     "channelid",
						Type:   model.ChannelTypeOpen,
						TeamId: "teamid",
					}, nil)
					e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				},
			},
		} {
			t.Run(urlName+" "+name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				test.botconfig.Name = "permtest"

				e.setupTestBot(test.botconfig)

				e.mockAPI.On("GetPost", "postid").Return(&model.Post{
					ChannelId: "channelid",
				}, nil)
				e.mockAPI.On("LogError", mock.Anything).Maybe()

				test.envSetup(e)

				test.request.Header.Add("Mattermost-User-ID", "userid")
				recorder := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, recorder, test.request)
				resp := recorder.Result()
				require.Equal(t, test.expectedStatus, resp.StatusCode)
			})
		}
	}
}

func TestAdminRouter(t *testing.T) {
	// This just makes gin not output a whole bunch of debug stuff.
	// maybe pipe this to the test log?
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for urlName, url := range map[string]string{
		"reindex_status":  "/admin/reindex/status",
		"mcp_tools":       "/admin/mcp/tools",
		"mcp_vetted_seed": "/admin/mcp/vetted-tool-seed",
	} {
		for name, test := range map[string]struct {
			request        *http.Request
			expectedStatus int
			envSetup       func(e *TestEnvironment)
		}{
			"only admins": {
				request:        httptest.NewRequest(http.MethodGet, url, nil),
				expectedStatus: http.StatusForbidden,
				envSetup: func(e *TestEnvironment) {
					e.mockAPI.On("HasPermissionTo", "userid", model.PermissionManageSystem).Return(false)
				},
			},
		} {
			t.Run(urlName+" "+name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				e.mockAPI.On("LogError", mock.Anything).Maybe()

				test.envSetup(e)

				test.request.Header.Add("Mattermost-User-ID", "userid")
				recorder := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, recorder, test.request)
				resp := recorder.Result()
				require.Equal(t, test.expectedStatus, resp.StatusCode)
			})
		}
	}
}

func TestEnforceEmptyBody(t *testing.T) {
	// This just makes gin not output a whole bunch of debug stuff.
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name          string
		requestBody   string
		expectedError bool
	}{
		{
			name:          "empty body",
			requestBody:   "",
			expectedError: false,
		},
		{
			name:          "non-empty body",
			requestBody:   "some content",
			expectedError: true,
		},
		{
			name:          "whitespace only",
			requestBody:   "   \n\t",
			expectedError: true,
		},
		{
			name:          "json object",
			requestBody:   `{"key": "value"}`,
			expectedError: true,
		},
		{
			name:          "empty json object",
			requestBody:   `{}`,
			expectedError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Create a test context with the specified request body
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)

			// Create request with the test body
			bodyReader := strings.NewReader(test.requestBody)
			req, err := http.NewRequest("POST", "/test", bodyReader)
			require.NoError(t, err)

			ctx.Request = req

			// Test the enforceEmptyBody function
			err = e.api.enforceEmptyBody(ctx)

			if test.expectedError {
				require.Error(t, err)
				require.Contains(t, err.Error(), "request body must be empty")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestEmptyBodyCheckerInApi tests the API endpoints that use enforceEmptyBody
func TestEmptyBodyCheckerInApi(t *testing.T) {
	// This just makes gin not output a whole bunch of debug stuff.
	// maybe pipe this to the test log?
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for urlName, url := range map[string]string{
		"react":                   "/post/postid/react?botUsername=thebot",
		"transcribe file":         "/post/postid/transcribe/file/fileid?botUsername=thebot",
		"summarize transcription": "/post/postid/summarize_transcription?botUsername=thebot",
		"regen":                   "/post/postid/regenerate",
		"postback summary":        "/post/postid/postback_summary",
		"loop in agent":           "/post/postid/loop_in_agent?botUsername=thebot",
		"cancel":                  "/admin/reindex/cancel",
	} {
		t.Run(urlName, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.mockAPI.On("LogError", "request body must be empty")
			e.mockAPI.On("GetPost", mock.Anything).Return(&model.Post{}, nil).Maybe()
			e.mockAPI.On("GetChannel", mock.Anything).Return(&model.Channel{}, nil).Maybe()
			e.mockAPI.On("HasPermissionToChannel", mock.Anything, mock.Anything, model.PermissionReadChannel).Return(true).Maybe()
			e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(true).Maybe()

			e.bots.SetBotsForTesting([]*bots.Bot{bots.NewBot(llm.BotConfig{Name: "thebot"}, llm.ServiceConfig{}, nil, nil)})

			request := httptest.NewRequest(http.MethodPost, url, strings.NewReader("non-empty body"))
			request.Header.Add("Mattermost-User-ID", "userid")
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)
			resp := recorder.Result()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestChannelRouter(t *testing.T) {
	// This just makes gin not output a whole bunch of debug stuff.
	// maybe pipe this to the test log?
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	for urlName, url := range map[string]string{
		"summarize since": "/channel/channelid/interval",
	} {
		for name, test := range map[string]struct {
			request        *http.Request
			expectedStatus int
			botconfig      llm.BotConfig
			envSetup       func(e *TestEnvironment)
		}{
			"test no permission to channel": {
				request:        httptest.NewRequest(http.MethodPost, url, nil),
				expectedStatus: http.StatusForbidden,
				envSetup: func(e *TestEnvironment) {
					e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
						Id:     "channelid",
						Type:   model.ChannelTypeOpen,
						TeamId: "teamid",
					}, nil)
					e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(false)
				},
			},
			"test user not allowed": {
				request:        httptest.NewRequest(http.MethodPost, url, nil),
				expectedStatus: http.StatusForbidden,
				botconfig: llm.BotConfig{
					UserAccessLevel: llm.UserAccessLevelBlock,
					UserIDs:         []string{"userid"},
				},
				envSetup: func(e *TestEnvironment) {
					e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
						Id:     "channelid",
						Type:   model.ChannelTypeOpen,
						TeamId: "teamid",
					}, nil)
					e.mockAPI.On("HasPermissionToChannel", "userid", "channelid", model.PermissionReadChannel).Return(true)
				},
			},
		} {
			t.Run(urlName+" "+name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				test.botconfig.Name = "permtest"

				e.setupTestBot(test.botconfig)

				e.mockAPI.On("LogError", mock.Anything).Maybe()

				test.envSetup(e)

				test.request.Header.Add("Mattermost-User-ID", "userid")
				recorder := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, recorder, test.request)
				resp := recorder.Result()
				require.Equal(t, test.expectedStatus, resp.StatusCode)
			})
		}
	}
}

func TestHandleGetAIBots(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name                     string
		searchService            *search.Search
		expectedSearchEnabled    bool
		expectedAllowUnsafeLinks bool
		expectedStatus           int
		envSetup                 func(e *TestEnvironment)
	}{
		{
			name: "search enabled - non-nil service with non-nil embedding search",
			searchService: func() *search.Search {
				me := mocks.NewMockEmbeddingSearch(t)
				return search.New(func() embeddings.EmbeddingSearch { return me }, nil, nil, nil, nil, nil)
			}(),
			expectedSearchEnabled:    true,
			expectedAllowUnsafeLinks: false,
			expectedStatus:           http.StatusOK,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
			},
		},
		{
			name:                     "search disabled - non-nil service with nil embedding search",
			searchService:            search.New(nil, nil, nil, nil, nil, nil),
			expectedSearchEnabled:    false,
			expectedAllowUnsafeLinks: false,
			expectedStatus:           http.StatusOK,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
			},
		},
		{
			name:                     "no search service - nil service",
			searchService:            nil,
			expectedSearchEnabled:    false,
			expectedAllowUnsafeLinks: false,
			expectedStatus:           http.StatusOK,
			envSetup: func(e *TestEnvironment) {
				e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
			},
		},
		{
			name:                     "unsafe links enabled via config",
			searchService:            nil,
			expectedSearchEnabled:    false,
			expectedAllowUnsafeLinks: true,
			expectedStatus:           http.StatusOK,
			envSetup: func(e *TestEnvironment) {
				e.config.allowUnsafeLinks = true
				e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Override the search service for this test
			e.api.searchService = test.searchService

			// Setup a test bot
			e.setupTestBot(llm.BotConfig{
				Name:        "test-bot",
				DisplayName: "Test Bot",
			})

			// Setup mock expectations
			test.envSetup(e)
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create request
			request := httptest.NewRequest(http.MethodGet, "/ai_bots", nil)
			request.Header.Add("Mattermost-User-ID", "userid")

			// Execute request
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			// Verify status code
			resp := recorder.Result()
			require.Equal(t, test.expectedStatus, resp.StatusCode)

			// Verify response body
			if test.expectedStatus == http.StatusOK {
				var response AIBotsResponse
				err := json.NewDecoder(resp.Body).Decode(&response)
				require.NoError(t, err)
				require.Equal(t, test.expectedSearchEnabled, response.SearchEnabled, "SearchEnabled field should match expected value")
				require.Equal(t, test.expectedAllowUnsafeLinks, response.AllowUnsafeLinks, "AllowUnsafeLinks field should match expected value")
				require.NotEmpty(t, response.Bots, "Should return at least one bot")
			}
		})
	}
}

func TestHandleGetAIBotsDefaultBotAfterFilteredBot(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	filteredBot := bots.NewBot(
		llm.BotConfig{
			Name:            "hidden",
			DisplayName:     "Hidden Agent",
			UserAccessLevel: llm.UserAccessLevelBlock,
			UserIDs:         []string{"userid"},
		},
		llm.ServiceConfig{},
		&model.Bot{UserId: "hiddenbotuserid1234567890", Username: "hidden", DisplayName: "Hidden Agent"},
		nil,
	)
	defaultBot := bots.NewBot(
		llm.BotConfig{
			Name:        "ai",
			DisplayName: "Default Agent",
		},
		llm.ServiceConfig{},
		&model.Bot{UserId: "defaultbotuserid1234567890", Username: "ai", DisplayName: "Default Agent"},
		nil,
	)
	e.bots.SetBotsForTesting([]*bots.Bot{filteredBot, defaultBot})

	e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
	e.mockAPI.On("LogError", mock.Anything).Maybe()

	request := httptest.NewRequest(http.MethodGet, "/ai_bots", nil)
	request.Header.Add("Mattermost-User-ID", "userid")

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)

	resp := recorder.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response AIBotsResponse
	err := json.NewDecoder(resp.Body).Decode(&response)
	require.NoError(t, err)
	require.Len(t, response.Bots, 1)
	require.Equal(t, "ai", response.Bots[0].Username)
}

func TestToolCallDMAllowedWhenChannelToolCallingDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	// These are the 4 tool-related endpoints that have the EnableChannelMentionToolCalling guard.
	// They should all pass the isDM check when the post is in a DM with the bot,
	// even when EnableChannelMentionToolCalling is false.
	tests := []struct {
		name     string
		endpoint string
		method   string
		body     string
	}{
		{
			name:     "tool_call in DM is allowed",
			endpoint: "/post/postid/tool_call",
			method:   http.MethodPost,
			body:     `{"accepted_tool_ids": ["tool-1"]}`,
		},
		{
			name:     "tool_result in DM is allowed",
			endpoint: "/post/postid/tool_result",
			method:   http.MethodPost,
			body:     `{"accepted_tool_ids": ["tool-1"]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			// Disable channel tool calling — the fix ensures DMs still work.
			e.config.enableChannelMentionToolCalling = false

			botUserID := testBotUserID
			userID := testUserID

			e.setupTestBot(llm.BotConfig{Name: "permtest", DisplayName: "Permission Bot"})

			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(&model.License{SkuShortName: "advanced"})

			post := &model.Post{
				Id:        "postid",
				UserId:    botUserID,
				ChannelId: "channelid",
			}
			post.AddProp("conversation_id", "conv-test-dm-toolcall")

			// Seed mock conversation store so isConversationOwner returns true for the DM owner
			e.conversationStore.conversations["conv-test-dm-toolcall"] = &store.Conversation{
				ID:     "conv-test-dm-toolcall",
				UserID: userID,
				BotID:  botUserID,
			}

			// DM channel name contains both user IDs
			dmChannelName := botUserID + "__" + userID

			e.mockAPI.On("GetPost", "postid").Return(post, nil)
			e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
				Id:   "channelid",
				Name: dmChannelName,
				Type: model.ChannelTypeDirect,
			}, nil)
			e.mockAPI.On("HasPermissionToChannel", userID, "channelid", model.PermissionReadChannel).Return(true)
			e.mockAPI.On("LogError", mock.Anything, mock.Anything, mock.Anything).Maybe()
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			var body io.Reader
			if test.body != "" {
				body = strings.NewReader(test.body)
			}
			request := httptest.NewRequest(test.method, test.endpoint, body)
			request.Header.Add("Mattermost-User-ID", userID)

			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)
			resp := recorder.Result()

			// Should NOT be 403 — the DM check should pass.
			// The request may fail later (e.g., 400 or 500 due to missing KV data),
			// but it must not be blocked by the config guard.
			require.NotEqual(t, http.StatusForbidden, resp.StatusCode,
				"DM tool call should not be blocked by EnableChannelMentionToolCalling config")
		})
	}
}
