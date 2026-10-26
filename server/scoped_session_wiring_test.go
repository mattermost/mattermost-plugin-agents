// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type wiringMinter struct {
	mu      sync.Mutex
	session *model.Session
	revoked []string
}

func (m *wiringMinter) CreateScopedSession(userID string, _ *model.SessionScope, expiresAt int64) (*model.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.session = &model.Session{Id: model.NewId(), UserId: userID, Token: model.NewId(), ExpiresAt: expiresAt}
	return m.session, nil
}

func (m *wiringMinter) RevokeSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked = append(m.revoked, sessionID)
	return nil
}

// heldFileReads lets a test hold a read_file call at the file service, after
// the embedded server has authenticated it, and observe what the real
// service then returns.
type heldFileReads struct {
	inner   *files.Service
	hold    chan struct{}
	entered chan struct{}
	results chan error
}

func (h *heldFileReads) GetContent(ctx context.Context, userID, fileID string, offset, limit int) (files.Content, error) {
	if h.hold != nil {
		h.entered <- struct{}{}
		<-h.hold
	}
	content, err := h.inner.GetContent(ctx, userID, fileID, offset, limit)
	h.results <- err
	return content, err
}

// TestScopedRunReadsThroughEmbeddedServer runs a scoped run's read_file call
// through the production embedded MCP server: the file service must check the
// run's scope instead of the bot's memberships, and refuse a read still in
// flight when the run ends.
func TestScopedRunReadsThroughEmbeddedServer(t *testing.T) {
	botID, requesterID := model.NewId(), model.NewId()
	trigger := &model.Channel{Id: model.NewId(), TeamId: model.NewId(), Type: model.ChannelTypePrivate}
	outside := &model.Channel{Id: model.NewId(), TeamId: model.NewId(), Type: model.ChannelTypeOpen}
	closedTeam := &model.Team{Id: outside.TeamId}
	triggerTeam := &model.Team{Id: trigger.TeamId}
	inScopeFile := &model.FileInfo{Id: model.NewId(), ChannelId: trigger.Id, Name: "notes.txt", Content: "evidence"}
	outsideFile := &model.FileInfo{Id: model.NewId(), ChannelId: outside.Id, Name: "secret.txt", Content: "secret"}

	mmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/users/me" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		assert.NoError(t, json.NewEncoder(w).Encode(&model.User{Id: botID, Username: "scoped-bot"}))
	}))
	t.Cleanup(mmServer.Close)

	minter := &wiringMinter{}
	api := &plugintest.API{}
	for i := 1; i <= 12; i++ {
		args := make([]any, i)
		for j := range args {
			args[j] = mock.Anything
		}
		api.On("LogDebug", args...).Return().Maybe()
		api.On("LogInfo", args...).Return().Maybe()
		api.On("LogWarn", args...).Return().Maybe()
		api.On("LogError", args...).Return().Maybe()
	}
	api.On("GetConfig").Return(&model.Config{ServiceSettings: model.ServiceSettings{
		SiteURL:       model.NewPointer(mmServer.URL),
		ListenAddress: model.NewPointer(strings.TrimPrefix(mmServer.URL, "http://")),
	}}).Maybe()
	api.On("KVGet", mock.Anything).Return(nil, nil).Maybe()
	api.On("GetSession", mock.Anything).Return(func(id string) (*model.Session, *model.AppError) {
		minter.mu.Lock()
		defer minter.mu.Unlock()
		if minter.session != nil && minter.session.Id == id {
			return minter.session, nil
		}
		return nil, model.NewAppError("GetSession", "not_found", nil, "", http.StatusNotFound)
	}).Maybe()
	pluginAPI := pluginapi.NewClient(api, nil)

	mm := mmapimocks.NewMockClient(t)
	mm.EXPECT().HasPermissionToFileAction(mock.Anything, mock.Anything, model.AccessControlPolicyActionDownloadFileAttachment).Return(true).Maybe()
	mm.EXPECT().GetFileInfo(inScopeFile.Id).Return(inScopeFile, nil).Maybe()
	mm.EXPECT().GetFileInfo(outsideFile.Id).Return(outsideFile, nil).Maybe()
	mm.EXPECT().GetChannel(trigger.Id).Return(trigger, nil).Maybe()
	mm.EXPECT().GetChannel(outside.Id).Return(outside, nil).Maybe()
	mm.EXPECT().GetTeam(trigger.TeamId).Return(triggerTeam, nil).Maybe()
	mm.EXPECT().GetTeam(outside.TeamId).Return(closedTeam, nil).Maybe()
	mm.EXPECT().GetConfig().Return(&model.Config{ComplianceSettings: model.ComplianceSettings{Enable: model.NewPointer(false)}}).Maybe()
	// The bot is in neither channel: only the scope can make a file readable.
	mm.EXPECT().HasPermissionToChannel(botID, mock.Anything, model.PermissionReadChannel).Return(false).Maybe()
	mm.EXPECT().RolesGrantPermission([]string{model.SystemUserRoleId}, model.PermissionJoinPublicTeams.Id).Return(true).Maybe()

	reads := &heldFileReads{inner: files.New(mm), entered: make(chan struct{}, 1), results: make(chan error, 4)}
	embedded, err := NewEmbeddedMCPServer(pluginAPI, pluginAPI.Log, nil, reads, func() bool { return false })
	require.NoError(t, err)

	manager := mcp.NewClientManager(mcp.Config{
		IdleTimeoutMinutes: 30,
		EmbeddedServer: mcp.EmbeddedServerConfig{
			Enabled:     true,
			ToolConfigs: []mcp.ToolConfig{{Name: "read_file", Policy: mcp.ToolPolicyAsk, Enabled: true}},
		},
	}, pluginAPI.Log, pluginAPI, nil, embedded, http.DefaultClient, nil, mcp.RemoteMCPAlwaysAllowed)
	t.Cleanup(manager.Close)
	manager.SetScopedSessions(minter)

	run, err := manager.MintScopedSession(t.Context(), mcp.ScopedSessionRequest{BotUserID: botID, RequesterID: requesterID, ChannelID: trigger.Id})
	require.NoError(t, err)
	tools, mcpErrors := manager.GetTools(t.Context(), mcp.ScopedCatalogRequest(run, requesterID))
	require.Nil(t, mcpErrors)
	var readFile llm.Tool
	for _, tool := range tools {
		if tool.Name == "mattermost__read_file" {
			readFile = tool
		}
	}
	require.NotNil(t, readFile.Resolver, "the scoped catalog must include read_file")

	read := func(fileID string) (string, error) {
		return readFile.Resolver(t.Context(), &llm.Context{BotUserID: botID}, func(args any) error {
			raw, marshalErr := json.Marshal(map[string]any{"file_id": fileID})
			if marshalErr != nil {
				return marshalErr
			}
			return json.Unmarshal(raw, args)
		})
	}

	result, err := read(inScopeFile.Id)
	require.NoError(t, err)
	assert.Contains(t, result, "evidence", "the triggering channel is readable through the scope, without membership")
	require.NoError(t, <-reads.results)

	result, err = read(outsideFile.Id)
	require.NoError(t, err)
	assert.Contains(t, result, "do not have permission")
	require.ErrorIs(t, <-reads.results, files.ErrForbidden)

	reads.hold = make(chan struct{})
	go func() { _, _ = read(inScopeFile.Id) }()
	select {
	case <-reads.entered:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "read_file never reached the file service")
	}
	// Close waits for in-flight tool calls, so release the held read once the
	// run has ended.
	closeErr := make(chan error, 1)
	go func() { closeErr <- run.Close() }()
	require.Eventually(t, run.Closed, 10*time.Second, time.Millisecond)
	close(reads.hold)
	select {
	case err := <-reads.results:
		require.ErrorIs(t, err, scopedsession.ErrRunEnded, "a read in flight when the run ends is refused")
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the held read never finished")
	}
	require.NoError(t, <-closeErr)
	assert.Equal(t, []string{run.Session().Id}, minter.revoked)
}
