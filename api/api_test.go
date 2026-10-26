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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/metrics"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	prompttemplates "github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"
	"github.com/mattermost/mattermost-plugin-agents/v2/search"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
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
	autoReplyStore    *fakeChannelAutoReplyStore
}

// testConfigImpl is a minimal implementation of Config for testing
type testConfigImpl struct {
	allowUnsafeLinks                bool
	enableHTMLArtifacts             bool
	enableChannelMentionToolCalling bool
	mcpConfig                       mcp.Config
	services                        []llm.ServiceConfig
	defaultBotUserID                string
	defaultBotName                  string
}

func (tc *testConfigImpl) GetDefaultBotUserID() string {
	return tc.defaultBotUserID
}

func (tc *testConfigImpl) GetDefaultBotName() string {
	return tc.defaultBotName
}

func (tc *testConfigImpl) MCP() mcp.Config {
	return tc.mcpConfig
}

func (tc *testConfigImpl) AllowUnsafeLinks() bool {
	return tc.allowUnsafeLinks
}

func (tc *testConfigImpl) EnableHTMLArtifacts() bool {
	return tc.enableHTMLArtifacts
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

func (tc *testConfigImpl) GetServices() []llm.ServiceConfig {
	return slices.Clone(tc.services)
}

type testLLMContextToolProvider struct {
	tools []llm.Tool
}

func (p *testLLMContextToolProvider) GetTools(_ *bots.Bot, _ *llm.Context) []llm.Tool {
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

// allowAnyPluginAPILogging permits arbitrary log calls from subsystems used in
// tests (for example MCP discovery). plugintest expands variadic arguments into
// positional ones, so each arity needs its own expectation.
func allowAnyPluginAPILogging(mockAPI *plugintest.API) {
	for arity := 1; arity <= 20; arity++ {
		args := make([]any, arity)
		for i := range args {
			args[i] = mock.Anything
		}
		mockAPI.On("LogDebug", args...).Maybe()
		mockAPI.On("LogInfo", args...).Maybe()
		mockAPI.On("LogWarn", args...).Maybe()
		mockAPI.On("LogError", args...).Maybe()
	}
}

// mockMCPClientManager is a minimal implementation of MCPClientManager for testing
type mockMCPClientManager struct {
	oauthManager                  *mcp.OAuthManager
	tools                         []llm.Tool
	mcpErrors                     *mcp.Errors
	deniedOrigins                 map[string]bool
	config                        mcp.Config
	embeddedServer                mcp.EmbeddedMCPServer
	processOAuthSession           *mcp.OAuthSession
	processOAuthErr               error
	disconnectCalls               []mcpDisconnectCall
	disconnectErr                 error
	oauthNeededCalls              []mcpDisconnectCall
	refreshErr                    error
	refreshCalls                  []string
	getContexts                   []context.Context
	getServiceAccountCalls        []string
	getServiceAccountInvokerCalls []string
	getServiceAccountContexts     []context.Context
	serviceAccountTools           []llm.Tool
	serviceAccountErrors          *mcp.Errors
	refreshContexts               []context.Context
	ensureSessionErr              error
	ensureSessionCreated          bool

	registerCalls   []mcp.PluginServerConfig
	adminPatchCalls []mcp.PluginServerConfig
	unregisterCalls []string
	pluginServers   []mcp.PluginServerConfig
	// accessPluginServers, when non-nil, is returned on CatalogAccess instead
	// of ListPluginServers — used to assert response rendering ignores a live
	// re-sample of the registry.
	accessPluginServers []mcp.PluginServerConfig
	// orphanPluginIDs simulates entries present in pluginServers but with
	// no live source-plugin registration (hydrated from persisted config).
	orphanPluginIDs map[string]bool

	discoverPluginToolsResponse []mcp.ToolInfo
	discoverPluginToolsErr      error
	// discoverPluginToolsFunc overrides the canned response per plugin. Set it
	// to model per-plugin latency or per-plugin failures.
	discoverPluginToolsFunc func(cfg mcp.PluginServerConfig) ([]mcp.ToolInfo, error)
	httpClient              *http.Client

	// Plugin discovery runs concurrently, so its bookkeeping is guarded.
	discoverMu                   sync.Mutex
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

func (m *mockMCPClientManager) ProcessOAuthCallback(ctx context.Context, loggedInUserID, state, code, iss string) (*mcp.OAuthSession, error) {
	return m.processOAuthSession, m.processOAuthErr
}

func (m *mockMCPClientManager) DisconnectUserOAuth(_ context.Context, userID, serverName string) error {
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

func (m *mockMCPClientManager) EnsureMCPSessionID(userID string) (string, bool, error) {
	if m.ensureSessionErr != nil {
		return "", false, m.ensureSessionErr
	}
	return "mock-session-id", m.ensureSessionCreated, nil
}

func (m *mockMCPClientManager) GetHTTPClient() *http.Client {
	return m.httpClient
}

func (m *mockMCPClientManager) GetToolsWithSelection(ctx context.Context, _ mcp.CatalogRequest, _ mcp.ToolSelection) ([]llm.Tool, *mcp.Errors) {
	m.getContexts = append(m.getContexts, ctx)
	return m.tools, m.mcpErrors
}

func (m *mockMCPClientManager) catalogAccess(req mcp.CatalogRequest) mcp.CatalogAccess {
	pluginSnapshot := m.accessPluginServers
	if pluginSnapshot == nil {
		pluginSnapshot = m.ListPluginServers()
	}
	access := mcp.CatalogAccess{
		Tools:         m.tools,
		Errors:        m.mcpErrors,
		DeniedOrigins: m.deniedOrigins,
		PluginServers: pluginSnapshot,
	}
	if req.ServiceAccount {
		access.Tools = m.serviceAccountTools
		access.Errors = m.serviceAccountErrors
	}
	return access
}

func (m *mockMCPClientManager) GetCatalogAccess(ctx context.Context, req mcp.CatalogRequest) mcp.CatalogAccess {
	if req.ServiceAccount {
		m.getServiceAccountCalls = append(m.getServiceAccountCalls, req.RemoteOwnerID)
		m.getServiceAccountInvokerCalls = append(m.getServiceAccountInvokerCalls, req.InvokingUserID)
		m.getServiceAccountContexts = append(m.getServiceAccountContexts, ctx)
		return m.catalogAccess(req)
	}
	m.getContexts = append(m.getContexts, ctx)
	return m.catalogAccess(req)
}

func (m *mockMCPClientManager) GetTools(ctx context.Context, req mcp.CatalogRequest) ([]llm.Tool, *mcp.Errors) {
	access := m.GetCatalogAccess(ctx, req)
	return access.Tools, access.Errors
}

func (m *mockMCPClientManager) RefreshCatalogAccess(ctx context.Context, req mcp.CatalogRequest) (mcp.CatalogAccess, error) {
	m.refreshCalls = append(m.refreshCalls, req.RemoteOwnerID)
	m.refreshContexts = append(m.refreshContexts, ctx)
	if m.refreshErr != nil {
		return mcp.CatalogAccess{}, m.refreshErr
	}
	return m.catalogAccess(req), nil
}

func (m *mockMCPClientManager) RefreshToolsForUser(ctx context.Context, userID string) ([]llm.Tool, *mcp.Errors, error) {
	access, err := m.RefreshCatalogAccess(ctx, mcp.UserCatalogRequest(userID))
	return access.Tools, access.Errors, err
}

func (m *mockMCPClientManager) GetConfig() mcp.Config {
	return m.config
}

func (m *mockMCPClientManager) RegisterPluginServer(cfg mcp.PluginServerConfig) {
	m.registerCalls = append(m.registerCalls, cfg)
	delete(m.orphanPluginIDs, cfg.PluginID)
	m.storePluginServer(cfg)
}

func (m *mockMCPClientManager) storePluginServer(cfg mcp.PluginServerConfig) {
	for i, existing := range m.pluginServers {
		if existing.PluginID == cfg.PluginID {
			m.pluginServers[i] = cfg
			return
		}
	}
	m.pluginServers = append(m.pluginServers, cfg)
}

func (m *mockMCPClientManager) UpdatePluginServerAdminFields(pluginID string, enabled bool, toolConfigs []mcp.ToolConfig) (mcp.PluginServerConfig, bool) {
	// Mirror real ClientManager: patch only admin-owned fields on the live entry.
	for i, existing := range m.pluginServers {
		if existing.PluginID == pluginID {
			existing.Enabled = enabled
			existing.ToolConfigs = toolConfigs
			m.pluginServers[i] = existing
			m.adminPatchCalls = append(m.adminPatchCalls, existing)
			return existing, true
		}
	}
	return mcp.PluginServerConfig{}, false
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
	out := make([]mcp.PluginServerConfig, 0, len(m.pluginServers))
	for _, cfg := range m.pluginServers {
		if m.orphanPluginIDs[cfg.PluginID] {
			continue
		}
		out = append(out, cfg)
	}
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

func (m *mockMCPClientManager) DiscoverPluginServerTools(ctx context.Context, userID string, cfg mcp.PluginServerConfig) ([]mcp.ToolInfo, error) {
	m.discoverMu.Lock()
	m.discoverPluginToolsCallCount++
	discover := m.discoverPluginToolsFunc
	m.discoverMu.Unlock()

	if discover != nil {
		return discover(cfg)
	}
	return m.discoverPluginToolsResponse, m.discoverPluginToolsErr
}

func (m *mockMCPClientManager) pluginDiscoveryCallCount() int {
	m.discoverMu.Lock()
	defer m.discoverMu.Unlock()
	return m.discoverPluginToolsCallCount
}

// fakeChannelAutoReplyStore is a hand-rolled in-memory implementation of
// ChannelAutoReplyStore. Injectable errors exercise the handlers' status-code
// mapping; setErr may wrap autoreply.ErrValidation to exercise the 400 path.
type fakeChannelAutoReplyStore struct {
	settings    map[string]*autoreply.Setting
	setCalls    []autoreply.Setting
	deleteCalls []string
	getErr      error
	setErr      error
	deleteErr   error
}

func newFakeChannelAutoReplyStore() *fakeChannelAutoReplyStore {
	return &fakeChannelAutoReplyStore{settings: make(map[string]*autoreply.Setting)}
}

func (f *fakeChannelAutoReplyStore) Get(channelID string) (*autoreply.Setting, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.settings[channelID], nil
}

func (f *fakeChannelAutoReplyStore) Set(channelID, botID string, mode autoreply.Mode, updatedBy string) (*autoreply.Setting, error) {
	if f.setErr != nil {
		return nil, f.setErr
	}
	setting := autoreply.Setting{
		ChannelID: channelID,
		BotID:     botID,
		Mode:      mode,
		UpdatedBy: updatedBy,
		UpdateAt:  time.Now().UnixMilli(),
	}
	f.setCalls = append(f.setCalls, setting)
	f.settings[channelID] = &setting
	return &setting, nil
}

func (f *fakeChannelAutoReplyStore) Delete(channelID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCalls = append(f.deleteCalls, channelID)
	delete(f.settings, channelID)
	return nil
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

	// countErr, when set, makes CountActiveAgents fail (to exercise best-effort paths).
	countErr error

	// createErr, when set, makes CreateAgent fail.
	createErr error

	// updateErr, when set, makes UpdateAgent fail.
	updateErr error

	// updateErrs, when non-empty, pops errors on consecutive UpdateAgent calls.
	updateErrs []error

	// versions holds each agent's versions, oldest first, recorded with the
	// same rules as the real store.
	versions map[string][]store.AgentVersionDetail
}

func newMockAgentStore() *mockAgentStore {
	return &mockAgentStore{
		agents:   make(map[string]*llm.BotConfig),
		versions: make(map[string][]store.AgentVersionDetail),
	}
}

func mockVersionSnapshot(cfg *llm.BotConfig) llm.BotConfig {
	snap := *cloneBotConfig(cfg)
	snap.BotUserID = ""
	snap.CreatorID = ""
	snap.CreateAt, snap.UpdateAt, snap.DeleteAt = 0, 0, 0
	nilIfEmpty := func(s []string) []string {
		if len(s) == 0 {
			return nil
		}
		return s
	}
	snap.ChannelIDs = nilIfEmpty(snap.ChannelIDs)
	snap.UserIDs = nilIfEmpty(snap.UserIDs)
	snap.TeamIDs = nilIfEmpty(snap.TeamIDs)
	snap.AdminUserIDs = nilIfEmpty(snap.AdminUserIDs)
	snap.EnabledNativeTools = nilIfEmpty(snap.EnabledNativeTools)
	if len(snap.EnabledMCPTools) == 0 {
		snap.EnabledMCPTools = nil
	}
	return snap
}

// recordVersion appends cfg as the next version of its agent. Agents seeded
// directly into agents (no versions yet) first get prev recorded as version 1.
func (m *mockAgentStore) recordVersion(cfg, prev *llm.BotConfig, meta store.AgentVersionMeta) {
	history := m.versions[cfg.ID]
	if len(history) == 0 && prev != nil {
		history = append(history, store.AgentVersionDetail{
			Version:       1,
			Source:        store.AgentVersionSourceInitial,
			CreateAt:      prev.UpdateAt,
			ChangedFields: []string{},
			Config:        mockVersionSnapshot(prev),
		})
	}
	snap := mockVersionSnapshot(cfg)
	changed := []string{}
	if len(history) > 0 {
		changed = audit.ChangedJSONKeys(history[len(history)-1].Config, snap)
	}
	history = append(history, store.AgentVersionDetail{
		Version:             mockLatestVersion(history) + 1,
		CreatedBy:           meta.ActorID,
		CreateAt:            cfg.UpdateAt,
		Source:              meta.Source,
		RestoredFromVersion: meta.RestoredFromVersion,
		ChangedFields:       changed,
		Config:              snap,
	})
	m.versions[cfg.ID] = history
}

func (m *mockAgentStore) ListAgentVersions(agentID string, before, limit int) (store.AgentVersionPage, error) {
	if limit < 1 {
		return store.AgentVersionPage{}, fmt.Errorf("invalid agent version page size %d", limit)
	}
	history := m.versions[agentID]
	page := store.AgentVersionPage{CurrentVersion: mockLatestVersion(history), Versions: []store.AgentVersion{}}
	for _, h := range slices.Backward(history) {
		if before > 0 && h.Version >= before {
			continue
		}
		if len(page.Versions) == limit {
			page.HasMore = true
			break
		}
		page.Versions = append(page.Versions, h.AgentVersion)
	}
	return page, nil
}

func (m *mockAgentStore) GetAgentVersion(agentID string, version int) (*store.AgentVersionDetail, error) {
	for _, detail := range m.versions[agentID] {
		if detail.Version == version {
			detail.Config = *cloneBotConfig(&detail.Config)
			return &detail, nil
		}
	}
	return nil, nil
}

func mockLatestVersion(history []store.AgentVersionDetail) int {
	if len(history) == 0 {
		return 0
	}
	return history[len(history)-1].Version
}

func (m *mockAgentStore) DeleteAgentVersions(agentID string, versions []int) (int64, error) {
	history := m.versions[agentID]
	if slices.Contains(versions, mockLatestVersion(history)) {
		return 0, store.ErrCurrentAgentVersion
	}
	kept := slices.DeleteFunc(slices.Clone(history), func(v store.AgentVersionDetail) bool {
		return slices.Contains(versions, v.Version)
	})
	m.versions[agentID] = kept
	return int64(len(history) - len(kept)), nil
}

func (m *mockAgentStore) DeletePreviousAgentVersions(agentID string) (int64, error) {
	history := m.versions[agentID]
	if len(history) == 0 {
		return 0, nil
	}
	m.versions[agentID] = history[len(history)-1:]
	return int64(len(history) - 1), nil
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

func (m *mockAgentStore) CreateAgent(cfg *llm.BotConfig, meta store.AgentVersionMeta) error {
	if m.createErr != nil {
		return m.createErr
	}
	cfg.ID = "agen" + fmt.Sprintf("%022d", len(m.agents)+1)
	now := time.Now().UnixMilli()
	cfg.CreateAt = now
	cfg.UpdateAt = now
	m.agents[cfg.ID] = cloneBotConfig(cfg)
	m.recordVersion(cfg, nil, meta)
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
	if m.countErr != nil {
		return 0, m.countErr
	}
	count := 0
	for _, cfg := range m.agents {
		if cfg.DeleteAt == 0 {
			count++
		}
	}
	return count, nil
}

func (m *mockAgentStore) UpdateAgent(cfg *llm.BotConfig, meta store.AgentVersionMeta) error {
	if len(m.updateErrs) > 0 {
		err := m.updateErrs[0]
		m.updateErrs = m.updateErrs[1:]
		if err != nil {
			return err
		}
	}
	if m.updateErr != nil {
		return m.updateErr
	}
	existing, ok := m.agents[cfg.ID]
	if !ok || existing.DeleteAt != 0 {
		return fmt.Errorf("agent %q not found or already deleted", cfg.ID)
	}
	cfg.UpdateAt = time.Now().UnixMilli()
	m.agents[cfg.ID] = cloneBotConfig(cfg)
	m.recordVersion(cfg, existing, meta)
	return nil
}

func (m *mockAgentStore) DeleteAgent(id string) error {
	cfg, ok := m.agents[id]
	if !ok || cfg.DeleteAt != 0 {
		return fmt.Errorf("agent %q not found or already deleted", id)
	}
	cfg.DeleteAt = time.Now().UnixMilli()
	delete(m.versions, id)
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

// CaptureAuditRecords replaces the default LogAuditRec mock registered by
// SetupTestEnvironment with one that appends every logged record to the
// returned slice, so tests can assert on emitted audit record contents.
// The returned pointer must only be dereferenced after the request completes.
func (e *TestEnvironment) CaptureAuditRecords() *[]*model.AuditRecord {
	filtered := make([]*mock.Call, 0, len(e.mockAPI.ExpectedCalls))
	for _, call := range e.mockAPI.ExpectedCalls {
		if call.Method != "LogAuditRec" {
			filtered = append(filtered, call)
		}
	}
	e.mockAPI.ExpectedCalls = filtered

	records := &[]*model.AuditRecord{}
	e.mockAPI.On("LogAuditRec", mock.Anything).Run(func(args mock.Arguments) {
		rec, ok := args.Get(0).(*model.AuditRecord)
		if ok {
			*records = append(*records, rec)
		}
	}).Maybe()
	return records
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

// newPassthroughAccessChecker builds an ABAC checker that always reports
// no_policy, so tests exercise pure legacy permission behavior.
func newPassthroughAccessChecker() *accesscontrol.Checker {
	return accesscontrol.New(accesscontrol.PassthroughClient{}, nil, accesscontrol.NoMCPServerIDs, nil)
}

// createTestBots creates a test MMBots instance for testing
func createTestBots(mockAPI *plugintest.API, client *pluginapi.Client) *bots.MMBots {
	licenseChecker := enterprise.NewLicenseChecker(client)
	testBots := bots.New(mockAPI, client, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
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
	e.setBots(bot)
}

// setBots replaces the bot registry with registry, each bot's Mattermost
// account holding the username the registry lists it under.
func (e *TestEnvironment) setBots(registry ...*bots.Bot) {
	for _, bot := range registry {
		mmBot := bot.GetMMBot()
		e.mockAPI.On("GetUser", mmBot.UserId).Return(&model.User{Id: mmBot.UserId, Username: mmBot.Username, IsBot: true}, nil).Maybe()
	}
	e.bots.SetBotsForTesting(registry)
}

// askForEveryTool is a tool policy checker that never lets a tool run
// without approval.
var askForEveryTool = mcp.ToolPolicyFunc(func(string, string) (string, bool) { return mcp.ToolPolicyAsk, false })

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
	autoReplyStore := newFakeChannelAutoReplyStore()

	allowAnyPluginAPILogging(mockAPI)

	// Mock GetConfig and GetLicense for WithLLMContextServerInfo and the
	// context builder's remote-MCP license gate. Default to a licensed
	// server so remote MCP tools are supplied; tests asserting unlicensed
	// behavior override via OverrideLicense.
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterprise}).Maybe()

	// The audit middleware logs a record for every audited route; accept them
	// by default so unrelated tests don't have to care. Tests asserting audit
	// behavior replace this with CaptureAuditRecords.
	mockAPI.On("LogAuditRec", mock.Anything).Maybe()

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
		askForEveryTool,
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
		enterprise.NewLicenseChecker(client),
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
		autoReplyStore,
		newPassthroughAccessChecker(),
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
		autoReplyStore:    autoReplyStore,
	}
}

func TestAIBotRequiredUsesConfiguredDefaultBot(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.config.defaultBotName = "ai"

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
	e.setBots(otherBot, defaultBot)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/post/postid/react", nil)
	ctx.Request = req

	e.api.aiBotRequired(ctx)
	require.False(t, ctx.IsAborted())

	selectedBot := ctx.MustGet(ContextBotKey).(*bots.Bot)
	require.Equal(t, "ai", selectedBot.GetMMBot().Username)
}

func TestAIBotRequiredDoesNotSubstituteAnotherAgent(t *testing.T) {
	const firstID = "firstbotuserid123456789012"
	const namedID = "namedbotuserid123456789012"
	const missingID = "missingbotuserid1234567890"
	first := bots.NewBot(
		llm.BotConfig{Name: "first", DisplayName: "First"},
		llm.ServiceConfig{},
		&model.Bot{UserId: firstID, Username: "first", DisplayName: "First"},
		nil,
	)
	named := bots.NewBot(
		llm.BotConfig{Name: "named", DisplayName: "Named"},
		llm.ServiceConfig{},
		&model.Bot{UserId: namedID, Username: "named", DisplayName: "Named"},
		nil,
	)

	tests := []struct {
		name             string
		query            string
		defaultBotUserID string
		defaultBotName   string
		expected         *bots.Bot
		expectedStatus   int
	}{
		{name: "agent requested by ID is served", query: "?botId=" + namedID, expected: named},
		{name: "requested ID wins over requested username", query: "?botId=" + namedID + "&botUsername=first", expected: named},
		{name: "agent requested by ID is unavailable", query: "?botId=" + missingID + "&botUsername=named", expectedStatus: http.StatusNotFound},
		{name: "requested ID is not a bot user ID", query: "?botId=named", expectedStatus: http.StatusBadRequest},
		{name: "agent requested by username is served", query: "?botUsername=named", expected: named},
		{name: "agent requested by username is unavailable", query: "?botUsername=renamed", expectedStatus: http.StatusNotFound},
		{name: "default agent by ID is served", defaultBotUserID: namedID, expected: named},
		{name: "default agent ID wins over legacy name", defaultBotUserID: namedID, defaultBotName: "first", expected: named},
		{name: "default agent by legacy name is served", defaultBotName: "named", expected: named},
		{name: "default agent ID is unavailable", defaultBotUserID: missingID, defaultBotName: "named", expectedStatus: http.StatusNotFound},
		{name: "default agent legacy name is unavailable", defaultBotName: "renamed", expectedStatus: http.StatusNotFound},
		{name: "requested agent wins over the default", query: "?botId=" + firstID, defaultBotUserID: namedID, expected: first},
		{name: "no agent requested and no default configured", expected: first},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.config.defaultBotUserID = tc.defaultBotUserID
			e.config.defaultBotName = tc.defaultBotName
			e.mockAPI.On("GetUserByUsername", "renamed").
				Return(nil, model.NewAppError("GetUserByUsername", "app.user.missing_account.const", nil, "", http.StatusNotFound)).Maybe()
			e.setBots(first, named)

			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/post/postid/react"+tc.query, nil)

			e.api.aiBotRequired(ctx)

			if tc.expected == nil {
				require.True(t, ctx.IsAborted())
				require.Equal(t, tc.expectedStatus, w.Code)
				_, set := ctx.Get(ContextBotKey)
				require.False(t, set, "no other agent may be selected in place of the requested one")
				return
			}
			require.False(t, ctx.IsAborted())
			require.Same(t, tc.expected, ctx.MustGet(ContextBotKey).(*bots.Bot))
		})
	}
}

// TestBotRequiredRoutesAcceptBotID drives every route behind aiBotRequired
// through the router, so a route that stops honoring botId fails here.
func TestBotRequiredRoutesAcceptBotID(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const missingID = "missingbotuserid1234567890"
	routes := map[string]string{
		"react":                   "/post/postid/react",
		"analyze post":            "/post/postid/analyze",
		"transcribe":              "/post/postid/transcribe/file/fileid",
		"summarize_transcription": "/post/postid/summarize_transcription",
		"postback_summary":        "/post/postid/postback_summary",
		"loop_in_agent":           "/post/postid/loop_in_agent",
		"analyze channel":         "/channel/channelid/analyze",
		"interval":                "/channel/channelid/interval",
		"search run":              "/search/run",
	}
	tests := []struct {
		name           string
		query          string
		expectedStatus int
	}{
		{name: "missing agent", query: "?botId=" + missingID, expectedStatus: http.StatusNotFound},
		{name: "malformed bot user ID", query: "?botId=permtest", expectedStatus: http.StatusBadRequest},
	}

	for routeName, url := range routes {
		for _, tc := range tests {
			t.Run(routeName+" "+tc.name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)
				e.setupTestBot(llm.BotConfig{Name: "permtest"})
				e.mockAPI.On("GetPost", "postid").Return(&model.Post{ChannelId: "channelid"}, nil).Maybe()
				e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{Id: "channelid", Type: model.ChannelTypeOpen, TeamId: "teamid"}, nil).Maybe()
				e.mockAPI.On("HasPermissionToChannel", testUserID, "channelid", model.PermissionReadChannel).Return(true).Maybe()

				request := httptest.NewRequest(http.MethodPost, url+tc.query, nil)
				request.Header.Add("Mattermost-User-ID", testUserID)
				recorder := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, recorder, request)
				require.Equal(t, tc.expectedStatus, recorder.Code)
			})
		}
	}
}

// TestAIBotRequiredRejectsReleasedUsername covers a registry that still lists
// a renamed agent under the username its bot account released.
func TestAIBotRequiredRejectsReleasedUsername(t *testing.T) {
	const renamedID = "renamedbotuserid123456789"
	notFound := model.NewAppError("GetUserByUsername", "app.user.missing_account.const", nil, "", http.StatusNotFound)

	tests := []struct {
		name           string
		query          string
		defaultBotName string
		holder         *model.User
	}{
		{name: "requested by its released name", query: "?botUsername=old-name"},
		{
			name:   "requested by its released name now held by a user",
			query:  "?botUsername=old-name",
			holder: &model.User{Id: "humanuserid12345678901234", Username: "old-name"},
		},
		{name: "configured default is its released name", defaultBotName: "old-name"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.config.defaultBotName = tc.defaultBotName
			if tc.holder != nil {
				e.mockAPI.On("GetUserByUsername", "old-name").Return(tc.holder, nil)
			} else {
				e.mockAPI.On("GetUserByUsername", "old-name").Return(nil, notFound)
			}
			e.mockAPI.On("GetUser", renamedID).Return(&model.User{Id: renamedID, Username: "new-name", IsBot: true}, nil)
			e.bots.SetBotsForTesting([]*bots.Bot{bots.NewBot(
				llm.BotConfig{Name: "old-name", DisplayName: "Renamed"},
				llm.ServiceConfig{},
				&model.Bot{UserId: renamedID, Username: "old-name", DisplayName: "Renamed"},
				nil,
			)})

			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/post/postid/react"+tc.query, nil)

			e.api.aiBotRequired(ctx)

			require.True(t, ctx.IsAborted())
			require.Equal(t, http.StatusNotFound, w.Code)
			_, set := ctx.Get(ContextBotKey)
			require.False(t, set, "a released username must not select the agent that released it")
		})
	}
}

// TestDefaultAgentFlagFollowsLiveAccount covers a registry that still lists
// the default agent under its previous username.
func TestDefaultAgentFlagFollowsLiveAccount(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	const defaultID = "defaultbotuserid1234567890"
	const otherID = "otherbotuserid12345678901a"

	tests := []struct {
		name             string
		defaultBotUserID string
		defaultBotName   string
		expectDefault    string
	}{
		{name: "default setting holds the agent's bot user ID", defaultBotUserID: defaultID, expectDefault: defaultID},
		{name: "default bot user ID wins over legacy name", defaultBotUserID: defaultID, defaultBotName: "other", expectDefault: defaultID},
		{name: "default bot user ID names no agent", defaultBotUserID: "missingbotuserid1234567890"},
		{name: "legacy default name follows the rename", defaultBotName: "new-name", expectDefault: defaultID},
		{name: "legacy default name is the released username", defaultBotName: "old-name"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			e.config.defaultBotUserID = tc.defaultBotUserID
			e.config.defaultBotName = tc.defaultBotName

			e.mockAPI.On("GetUser", defaultID).Return(&model.User{Id: defaultID, Username: "new-name", IsBot: true}, nil).Maybe()
			e.mockAPI.On("GetUser", otherID).Return(&model.User{Id: otherID, Username: "other", IsBot: true}, nil).Maybe()
			e.mockAPI.On("GetUserByUsername", "new-name").Return(&model.User{Id: defaultID, Username: "new-name", IsBot: true}, nil).Maybe()
			e.mockAPI.On("GetUserByUsername", "old-name").
				Return(nil, model.NewAppError("GetUserByUsername", "app.user.missing_account.const", nil, "", http.StatusNotFound)).Maybe()
			e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
			e.bots.SetBotsForTesting([]*bots.Bot{
				bots.NewBot(llm.BotConfig{Name: "other", DisplayName: "Other Agent"}, llm.ServiceConfig{},
					&model.Bot{UserId: otherID, Username: "other", DisplayName: "Other Agent"}, nil),
				bots.NewBot(llm.BotConfig{Name: "old-name", DisplayName: "Default Agent"}, llm.ServiceConfig{},
					&model.Bot{UserId: defaultID, Username: "old-name", DisplayName: "Default Agent"}, nil),
			})

			request := httptest.NewRequest(http.MethodGet, "/ai_bots", nil)
			request.Header.Add("Mattermost-User-ID", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)
			require.Equal(t, http.StatusOK, recorder.Code)
			var response AIBotsResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
			require.Len(t, response.Bots, 2)
			flagged := ""
			for _, bot := range response.Bots {
				if bot.IsDefault {
					require.Empty(t, flagged, "only one agent may be the default")
					flagged = bot.ID
				}
			}
			require.Equal(t, tc.expectDefault, flagged, "/ai_bots")
			if tc.expectDefault != "" {
				require.Equal(t, tc.expectDefault, response.Bots[0].ID, "the default agent must be listed first")
			}

			agents, err := e.CreateBridgeClient().GetAgents("")
			require.NoError(t, err)
			require.Len(t, agents, 2)
			flagged = ""
			for _, agent := range agents {
				if agent.IsDefault {
					require.Empty(t, flagged, "only one agent may be the default")
					flagged = agent.ID
				}
			}
			require.Equal(t, tc.expectDefault, flagged, "bridge agents")
		})
	}
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
					e.mockAPI.On("HasPermissionToChannel", testUserID, "channelid", model.PermissionReadChannel).Return(false)
				},
			},
			"user not allowed": {
				request:        httptest.NewRequest(http.MethodPost, url, nil),
				expectedStatus: http.StatusForbidden,
				botconfig: llm.BotConfig{
					UserAccessLevel: llm.UserAccessLevelBlock,
					UserIDs:         []string{testUserID},
				},
				envSetup: func(e *TestEnvironment) {
					e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
						Id:     "channelid",
						Type:   model.ChannelTypeOpen,
						TeamId: "teamid",
					}, nil)
					e.mockAPI.On("HasPermissionToChannel", testUserID, "channelid", model.PermissionReadChannel).Return(true)
				},
			},
		} {
			// The agent-post routes check access to the post's agent in the
			// service; TestPostResumeRoutesUseThePostsAgent covers them.
			if name == "user not allowed" && (urlName == "stop" || urlName == "regenerate") {
				continue
			}
			t.Run(urlName+" "+name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				test.botconfig.Name = "permtest"

				e.setupTestBot(test.botconfig)

				// The agent-post routes resolve the agent from the post's
				// author; the others ignore it.
				e.mockAPI.On("GetPost", "postid").Return(&model.Post{
					UserId:    testBotUserID,
					ChannelId: "channelid",
				}, nil)
				e.mockAPI.On("LogError", mock.Anything).Maybe()

				test.envSetup(e)

				test.request.Header.Add("Mattermost-User-ID", testUserID)
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
		"reindex_status":            "/admin/reindex/status",
		"mcp_tools":                 "/admin/mcp/tools",
		"mcp_vetted_seed":           "/admin/mcp/vetted-tool-seed",
		"mcp_suggested_description": "/admin/mcp/server-id/suggested-description",
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
					e.mockAPI.On("HasPermissionTo", testUserID, model.PermissionManageSystem).Return(false)
				},
			},
		} {
			t.Run(urlName+" "+name, func(t *testing.T) {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)

				e.mockAPI.On("LogError", mock.Anything).Maybe()

				test.envSetup(e)

				test.request.Header.Add("Mattermost-User-ID", testUserID)
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

			e.mockAPI.On("LogDebug", "Request rejected", "method", http.MethodPost, "path", mock.Anything, "status", http.StatusBadRequest, "error", "request body must be empty")
			e.mockAPI.On("GetPost", mock.Anything).Return(&model.Post{UserId: testBotUserID}, nil).Maybe()
			e.mockAPI.On("GetChannel", mock.Anything).Return(&model.Channel{}, nil).Maybe()
			e.mockAPI.On("HasPermissionToChannel", mock.Anything, mock.Anything, model.PermissionReadChannel).Return(true).Maybe()
			e.mockAPI.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(true).Maybe()

			e.setBots(bots.NewBot(llm.BotConfig{Name: "thebot"}, llm.ServiceConfig{}, &model.Bot{UserId: testBotUserID, Username: "thebot"}, nil))

			request := httptest.NewRequest(http.MethodPost, url, strings.NewReader("non-empty body"))
			request.Header.Add("Mattermost-User-ID", testUserID)
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
					e.mockAPI.On("HasPermissionToChannel", testUserID, "channelid", model.PermissionReadChannel).Return(false)
				},
			},
			"test user not allowed": {
				request:        httptest.NewRequest(http.MethodPost, url, nil),
				expectedStatus: http.StatusForbidden,
				botconfig: llm.BotConfig{
					UserAccessLevel: llm.UserAccessLevelBlock,
					UserIDs:         []string{testUserID},
				},
				envSetup: func(e *TestEnvironment) {
					e.mockAPI.On("GetChannel", "channelid").Return(&model.Channel{
						Id:     "channelid",
						Type:   model.ChannelTypeOpen,
						TeamId: "teamid",
					}, nil)
					e.mockAPI.On("HasPermissionToChannel", testUserID, "channelid", model.PermissionReadChannel).Return(true)
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

				test.request.Header.Add("Mattermost-User-ID", testUserID)
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
		name                          string
		searchService                 *search.Search
		useServiceAccountAuth         bool
		expectedUseServiceAccountAuth bool
		expectedSearchEnabled         bool
		expectedAllowUnsafeLinks      bool
		expectedHTMLArtifacts         bool
		expectedStatus                int
		envSetup                      func(e *TestEnvironment)
	}{
		{
			name: "search enabled - non-nil service with non-nil embedding search",
			searchService: func() *search.Search {
				me := mocks.NewMockEmbeddingSearch(t)
				return search.New(func() embeddings.EmbeddingSearch { return me }, nil, enterprisetest.CheckerAt(enterprise.LevelEnterprise))
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
			searchService:            search.New(nil, nil, nil),
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
			// The webapp reads useServiceAccountAuth to hide per-user MCP connect prompts.
			name:                          "unsafe links enabled via config",
			searchService:                 nil,
			useServiceAccountAuth:         true,
			expectedUseServiceAccountAuth: true,
			expectedSearchEnabled:         false,
			expectedAllowUnsafeLinks:      true,
			expectedStatus:                http.StatusOK,
			envSetup: func(e *TestEnvironment) {
				e.config.allowUnsafeLinks = true
				e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
			},
		},
		{
			name:                  "html artifacts enabled via config",
			expectedHTMLArtifacts: true,
			expectedStatus:        http.StatusOK,
			envSetup: func(e *TestEnvironment) {
				e.config.enableHTMLArtifacts = true
				e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
			},
		},
		{
			// Unlicensed servers run service account agents in per-user mode, so the
			// response must report the effective mode instead of the raw agent flag.
			name:                          "service account agent reports user mode when unlicensed",
			searchService:                 nil,
			useServiceAccountAuth:         true,
			expectedUseServiceAccountAuth: false,
			expectedSearchEnabled:         false,
			expectedAllowUnsafeLinks:      false,
			expectedStatus:                http.StatusOK,
			envSetup: func(e *TestEnvironment) {
				e.OverrideLicense(nil)
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
				Name:                  "test-bot",
				DisplayName:           "Test Bot",
				UseServiceAccountAuth: test.useServiceAccountAuth,
			})

			// Setup mock expectations
			test.envSetup(e)
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			// Create request
			request := httptest.NewRequest(http.MethodGet, "/ai_bots", nil)
			request.Header.Add("Mattermost-User-ID", testUserID)

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
				require.Equal(t, test.expectedHTMLArtifacts, response.HTMLArtifactsEnabled, "HTMLArtifactsEnabled field should match expected value")
				require.NotEmpty(t, response.Bots, "Should return at least one bot")
				require.Equal(t, test.expectedUseServiceAccountAuth, response.Bots[0].UseServiceAccountAuth,
					"UseServiceAccountAuth field should report the effective service account mode")
			}
		})
	}
}

func TestHandleGetAIBotsDefaultBotAfterFilteredBot(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.config.defaultBotName = "ai"

	filteredBot := bots.NewBot(
		llm.BotConfig{
			Name:            "hidden",
			DisplayName:     "Hidden Agent",
			UserAccessLevel: llm.UserAccessLevelBlock,
			UserIDs:         []string{testUserID},
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
	e.setBots(filteredBot, defaultBot)

	e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
	e.mockAPI.On("LogError", mock.Anything).Maybe()

	request := httptest.NewRequest(http.MethodGet, "/ai_bots", nil)
	request.Header.Add("Mattermost-User-ID", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)

	resp := recorder.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response AIBotsResponse
	err := json.NewDecoder(resp.Body).Decode(&response)
	require.NoError(t, err)
	require.Len(t, response.Bots, 1)
	require.Equal(t, "ai", response.Bots[0].Username)
	require.True(t, response.Bots[0].IsDefault, "default bot should be flagged with isDefault")
}

func TestHandleGetAIBotsIsDefaultFlag(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.config.defaultBotName = "ai"

	defaultBot := bots.NewBot(
		llm.BotConfig{Name: "ai", DisplayName: "Default Agent"},
		llm.ServiceConfig{},
		&model.Bot{UserId: "defaultbotuserid1234567890", Username: "ai", DisplayName: "Default Agent"},
		nil,
	)
	otherBot := bots.NewBot(
		llm.BotConfig{Name: "other", DisplayName: "Other Agent"},
		llm.ServiceConfig{},
		&model.Bot{UserId: "otherbotuserid12345678901a", Username: "other", DisplayName: "Other Agent"},
		nil,
	)
	e.setBots(otherBot, defaultBot)

	e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{})
	e.mockAPI.On("LogError", mock.Anything).Maybe()

	request := httptest.NewRequest(http.MethodGet, "/ai_bots", nil)
	request.Header.Add("Mattermost-User-ID", testUserID)

	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)

	resp := recorder.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var response AIBotsResponse
	err := json.NewDecoder(resp.Body).Decode(&response)
	require.NoError(t, err)
	require.Len(t, response.Bots, 2)

	byUsername := make(map[string]AIBotInfo, len(response.Bots))
	for _, b := range response.Bots {
		byUsername[b.Username] = b
	}
	require.True(t, byUsername["ai"].IsDefault, "configured default bot should have isDefault=true")
	require.False(t, byUsername["other"].IsDefault, "non-default bot should have isDefault=false")
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
