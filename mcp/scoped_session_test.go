// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var errServerRefused = errors.New("server refused to create scoped session")

type fakeScopedMinter struct {
	mu        sync.Mutex
	createErr error
	// rejected channels make the server refuse any scope listing them.
	rejected  map[string]bool
	attempts  int
	wrongUser bool
	created   []mintedScopedSession
	revoked   []string
}

type mintedScopedSession struct {
	userID    string
	scope     model.SessionScope
	expiresAt int64
	session   *model.Session
}

func (f *fakeScopedMinter) CreateScopedSession(userID string, scope *model.SessionScope, expiresAt int64) (*model.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.createErr != nil {
		return nil, f.createErr
	}
	if slices.ContainsFunc(scope.Resources.ChannelIDs, func(id string) bool { return f.rejected[id] }) {
		return nil, errServerRefused
	}
	session := &model.Session{Id: model.NewId(), UserId: userID, Token: model.NewId(), ExpiresAt: expiresAt}
	if f.wrongUser {
		session.UserId = model.NewId()
	}
	f.created = append(f.created, mintedScopedSession{userID: userID, scope: *scope, expiresAt: expiresAt, session: session})
	return session, nil
}

func (f *fakeScopedMinter) RevokeSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, sessionID)
	return nil
}

func (f *fakeScopedMinter) revokedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revoked...)
}

func addReadOnlyTestMCPTool(server *gomcp.Server, toolName string) {
	server.AddTool(&gomcp.Tool{
		Name:        toolName,
		InputSchema: map[string]any{"type": "object"},
		Annotations: &gomcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(context.Context, *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
		return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: toolName + " ok"}}}, nil
	})
}

func TestScopedCatalog(t *testing.T) {
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)

	botUserID := model.NewId()
	requesterID := model.NewId()
	channelID := model.NewId()
	extraChannelID := model.NewId()

	embeddedMCP := newTestMCPServer(0, "create_post")
	addReadOnlyTestMCPTool(embeddedMCP, "read_channel")
	embeddedServer := &recordingEmbeddedMCPServer{ctx: runCtx, server: embeddedMCP}

	saHTTP := startStreamableMCPServer(t, newTestMCPServer(0, "sa_tool"))
	pluginTarget := newFakePluginMCPServer(t, 1)
	t.Cleanup(pluginTarget.Close)

	// The requester has a regular embedded session; the scoped catalog must
	// never use it, and must never create one for the bot.
	pluginAPI := newTestPluginAPIForEmbeddedUsers(map[string]string{requesterID: "requester-session"})
	m := NewClientManager(Config{
		IdleTimeoutMinutes: 30,
		Servers: []ServerConfig{
			{Name: "sa-server", BaseURL: saHTTP.URL, Enabled: true, ServiceAccountHeaders: testServiceAccountHeaders()},
			{Name: "user-oauth-server", BaseURL: deadServerURL(t), Enabled: true},
		},
		EmbeddedServer: EmbeddedServerConfig{
			Enabled: true,
			ToolConfigs: []ToolConfig{
				{Name: "read_channel", Policy: ToolPolicyAsk, Enabled: true},
				{Name: "create_post", Policy: ToolPolicyAsk, Enabled: true},
			},
		},
	}, pluginAPI.Log, pluginAPI, newTestOAuthManager(), embeddedServer, saHTTP.Client(), newPluginHTTPForwarder(t, pluginTarget), RemoteMCPAlwaysAllowed)
	t.Cleanup(m.Close)
	m.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.mcp", Name: "Example", Path: "/mcp", Enabled: true})

	minter := &fakeScopedMinter{}
	m.SetScopedSessions(minter)

	run, err := m.MintScopedSession(context.Background(), ScopedSessionRequest{
		BotUserID:       botUserID,
		RequesterID:     requesterID,
		ChannelID:       channelID,
		ExtraChannelIDs: []string{extraChannelID, channelID, "not-an-id"},
		EnabledTools:    []llm.EnabledMCPTool{{ServerOrigin: EmbeddedClientKey, ToolName: "read_channel"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []llm.EnabledMCPTool{{ServerOrigin: EmbeddedClientKey, ToolName: "read_channel"}}, run.EnabledTools(),
		"the run records the allowlist it was started with")

	require.Len(t, minter.created, 1)
	minted := minter.created[0]
	assert.Equal(t, botUserID, minted.userID)
	assert.Equal(t, model.ScopeReadPermissions, minted.scope.Permissions)
	assert.Equal(t, []string{channelID, extraChannelID}, minted.scope.Resources.ChannelIDs)
	assert.True(t, minted.scope.Resources.OpenTeamPublicChannels)
	assert.Equal(t, requesterID, minted.scope.OnBehalfOfUserID)
	assert.WithinDuration(t, time.Now().Add(scopedsession.RunTTL), time.UnixMilli(minted.expiresAt), time.Minute)

	tools, mcpErrors := m.GetTools(context.Background(), ScopedCatalogRequest(run, requesterID))
	require.Nil(t, mcpErrors)
	requireToolNames(t, tools, "mattermost__read_channel", "sa_server__sa_tool")
	assert.Equal(t, []string{botUserID}, embeddedServer.recordedUserIDs(), "the embedded server must connect as the bot")
	assert.Equal(t, []string{run.Session().Id}, embeddedServer.recordedSessionIDs(), "the embedded server must use the scoped session")

	scope, err := scopedsession.ScopeFromContext(embeddedServer.lastConnCtx())
	require.NoError(t, err)
	assert.Equal(t, run.Scope(), scope, "the scoped connection carries the run's scope")
	scopedConnCtx := embeddedServer.lastConnCtx()

	// A regular catalog for the same requester afterwards is unaffected.
	userTools, _ := m.GetTools(context.Background(), ServiceAccountCatalogRequest(botUserID, requesterID))
	requireToolNames(t, userTools, "mattermost__read_channel", "mattermost__create_post", "sa_server__sa_tool", "example__test_tool_0")
	requesterScope, err := scopedsession.ScopeFromContext(embeddedServer.lastConnCtx())
	require.NoError(t, err)
	assert.Nil(t, requesterScope, "the requester's own connection carries no scope")

	require.NoError(t, run.Close())
	require.NoError(t, run.Close())
	assert.Equal(t, []string{run.Session().Id}, minter.revokedIDs(), "closing a run revokes its session exactly once")
	_, err = scopedsession.ScopeFromContext(scopedConnCtx)
	require.ErrorIs(t, err, scopedsession.ErrRunEnded, "reads still in flight on the scoped connection are refused")
}

func TestMintScopedSessionFailsClosed(t *testing.T) {
	valid := ScopedSessionRequest{BotUserID: model.NewId(), RequesterID: model.NewId(), ChannelID: model.NewId()}
	tests := []struct {
		name        string
		minter      *fakeScopedMinter
		req         ScopedSessionRequest
		wantErr     error
		wantRevokes int
	}{
		{name: "no minter configured", req: valid, wantErr: ErrScopedSessionsUnavailable},
		{name: "server cannot mint", minter: &fakeScopedMinter{createErr: errServerRefused}, req: valid, wantErr: errServerRefused},
		{name: "session for another user is revoked", minter: &fakeScopedMinter{wrongUser: true}, req: valid, wantRevokes: 1},
		{name: "invalid channel", minter: &fakeScopedMinter{}, req: ScopedSessionRequest{BotUserID: valid.BotUserID, RequesterID: valid.RequesterID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginAPI := newTestPluginAPIForEmbeddedUsers(nil)
			m := NewClientManager(Config{IdleTimeoutMinutes: 30}, pluginAPI.Log, pluginAPI, nil, nil, http.DefaultClient, nil, RemoteMCPAlwaysAllowed)
			t.Cleanup(m.Close)
			if tt.minter != nil {
				m.SetScopedSessions(tt.minter)
			}

			run, err := m.MintScopedSession(context.Background(), tt.req)
			require.Error(t, err)
			assert.Nil(t, run)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
			if tt.minter != nil {
				assert.Len(t, tt.minter.revokedIDs(), tt.wantRevokes)
			}
		})
	}
}

func TestMintScopedSessionWithDeletedExtraChannels(t *testing.T) {
	trigger, kept, deleted, unreadable := model.NewId(), model.NewId(), model.NewId(), model.NewId()
	notFound := model.NewAppError("GetChannel", "app.channel.get.existing.app_error", nil, "", http.StatusNotFound)

	tests := []struct {
		name         string
		rejected     map[string]bool
		channelErrs  map[string]*model.AppError
		wantErr      bool
		wantChannels []string
		wantAttempts int
	}{
		{
			name:         "a deleted extra channel is left out instead of failing the run",
			rejected:     map[string]bool{deleted: true},
			channelErrs:  map[string]*model.AppError{deleted: notFound},
			wantChannels: []string{trigger, kept, unreadable},
			wantAttempts: 2,
		},
		{
			name:         "a failed channel lookup fails the run",
			rejected:     map[string]bool{deleted: true},
			channelErrs:  map[string]*model.AppError{deleted: notFound, unreadable: model.NewAppError("GetChannel", "app.channel.get.find.app_error", nil, "", http.StatusInternalServerError)},
			wantErr:      true,
			wantAttempts: 1,
		},
		{
			name:         "a refusal with every channel present is not retried",
			rejected:     map[string]bool{trigger: true},
			wantErr:      true,
			wantAttempts: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pluginAPI := pluginapi.NewClient(&fixedPluginAPI{channelErrs: tt.channelErrs}, nil)
			m := NewClientManager(Config{IdleTimeoutMinutes: 30}, pluginAPI.Log, pluginAPI, nil, nil, http.DefaultClient, nil, RemoteMCPAlwaysAllowed)
			t.Cleanup(m.Close)
			minter := &fakeScopedMinter{rejected: tt.rejected}
			m.SetScopedSessions(minter)

			run, err := m.MintScopedSession(context.Background(), ScopedSessionRequest{
				BotUserID:       model.NewId(),
				RequesterID:     model.NewId(),
				ChannelID:       trigger,
				ExtraChannelIDs: []string{kept, deleted, unreadable},
			})
			assert.Equal(t, tt.wantAttempts, minter.attempts)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, run)
				return
			}
			require.NoError(t, err)
			wantExtras := tt.wantChannels[1:]
			assert.Equal(t, tt.wantChannels, run.Scope().Resources.ChannelIDs)
			assert.Equal(t, wantExtras, run.ExtraChannelIDs())
		})
	}
}

func TestScopedRunsRevokedWithTheirAgent(t *testing.T) {
	pluginAPI := newTestPluginAPIForEmbeddedUsers(nil)
	m := NewClientManager(Config{IdleTimeoutMinutes: 30}, pluginAPI.Log, pluginAPI, nil, nil, http.DefaultClient, nil, RemoteMCPAlwaysAllowed)
	minter := &fakeScopedMinter{}
	m.SetScopedSessions(minter)

	mint := func(botUserID string) *ScopedRun {
		run, err := m.MintScopedSession(context.Background(), ScopedSessionRequest{BotUserID: botUserID, RequesterID: model.NewId(), ChannelID: model.NewId()})
		require.NoError(t, err)
		return run
	}
	botA, botB := model.NewId(), model.NewId()
	runA1, runA2, runB := mint(botA), mint(botA), mint(botB)

	m.RevokeScopedSessionsWhere(func(run *ScopedRun) bool { return run.BotUserID() == botA })
	assert.True(t, runA1.Closed())
	assert.False(t, runB.Closed())
	assert.ElementsMatch(t, []string{runA1.Session().Id, runA2.Session().Id}, minter.revokedIDs())

	m.Close()
	assert.ElementsMatch(t, []string{runA1.Session().Id, runA2.Session().Id, runB.Session().Id}, minter.revokedIDs(), "stopping the plugin revokes every live run")

	_, err := m.MintScopedSession(context.Background(), ScopedSessionRequest{BotUserID: botA, RequesterID: model.NewId(), ChannelID: model.NewId()})
	require.True(t, errors.Is(err, ErrScopedSessionsUnavailable))
}

func TestScopedCatalogAllows(t *testing.T) {
	tests := []struct {
		name    string
		tool    llm.Tool
		allowed bool
	}{
		{name: "read-only embedded tool", tool: llm.Tool{ServerOrigin: EmbeddedClientKey, ReadOnly: true}, allowed: true},
		{name: "state-changing embedded tool", tool: llm.Tool{ServerOrigin: EmbeddedClientKey}},
		{name: "plugin MCP tool", tool: llm.Tool{ServerOrigin: "plugin://com.example.mcp", ReadOnly: true}},
		{name: "remote tool", tool: llm.Tool{ServerOrigin: "https://mcp.example.com"}, allowed: true},
		{name: "built-in tool", tool: llm.Tool{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.allowed, ScopedCatalogAllows(tt.tool))
		})
	}
}
