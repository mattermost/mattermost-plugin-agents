// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver"
	"github.com/mattermost/mattermost/server/public/model"
	plugintest "github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	mmcontainer "github.com/mattermost/testcontainers-mattermost-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// EmbeddedTestSuite provides infrastructure for testing the embedded MCP server
type EmbeddedTestSuite struct {
	t              *testing.T
	container      *mmcontainer.MattermostContainer
	serverURL      string
	adminClient    *model.Client4
	embeddedServer *mcpserver.MattermostInMemoryMCPServer
	logger         *testLogger
}

// SetupEmbeddedTestSuite initializes a test environment with a real Mattermost container
// and an embedded MCP server
func SetupEmbeddedTestSuite(t *testing.T) *EmbeddedTestSuite {
	ctx := context.Background()

	// Start Mattermost container
	container, err := mmcontainer.RunContainer(ctx,
		mmcontainer.WithLicense(""),
	)
	require.NoError(t, err, "Failed to start Mattermost container")

	// Enable open server to allow test user signups
	err = container.SetConfig(ctx, "TeamSettings.EnableOpenServer", "true")
	require.NoError(t, err, "Failed to enable open server")

	// Enable user creation
	err = container.SetConfig(ctx, "TeamSettings.EnableUserCreation", "true")
	require.NoError(t, err, "Failed to enable user creation")

	// Get connection details
	serverURL, err := container.URL(ctx)
	require.NoError(t, err, "Failed to get server URL")

	// Get admin client
	adminClient, err := container.GetAdminClient(ctx)
	require.NoError(t, err, "Failed to get admin client")

	suite := &EmbeddedTestSuite{
		t:           t,
		container:   container,
		serverURL:   serverURL,
		adminClient: adminClient,
	}

	return suite
}

// SetupEmbeddedServer creates and configures an embedded MCP server for the test suite
func (s *EmbeddedTestSuite) SetupEmbeddedServer() {
	// Create logger
	s.logger = &testLogger{t: s.t}

	// Create embedded server configuration
	config := mcpserver.InMemoryConfig{
		BaseConfig: mcpserver.BaseConfig{
			MMServerURL:         s.serverURL,
			MMInternalServerURL: s.serverURL,
			DevMode:             false,
		},
	}

	// Create embedded server
	server, err := mcpserver.NewInMemoryServer(config, s.logger)
	require.NoError(s.t, err, "Failed to create embedded MCP server")

	s.embeddedServer = server
}

// TearDown cleans up the test suite
func (s *EmbeddedTestSuite) TearDown() {
	if s.container != nil {
		ctx := context.Background()
		if err := s.container.Terminate(ctx); err != nil {
			s.t.Logf("Failed to terminate container: %v", err)
		}
	}
}

// CreateUserAndSession creates a new test user and returns the user and an active session
func (s *EmbeddedTestSuite) CreateUserAndSession(t *testing.T) (*model.User, *model.Session) {
	ctx := context.Background()

	// Create a unique user
	username := fmt.Sprintf("testuser_%d", model.GetMillis())
	email := fmt.Sprintf("%s@test.com", username)

	user := &model.User{
		Username: username,
		Email:    email,
		Password: "TestPassword123!",
	}

	createdUser, _, err := s.adminClient.CreateUser(ctx, user)
	require.NoError(t, err, "Failed to create test user")

	// Get or create a default team for the user
	teams, _, err := s.adminClient.GetAllTeams(ctx, "", 0, 1)
	require.NoError(t, err, "Failed to get teams")

	var teamID string
	if len(teams) > 0 {
		// Use existing team
		teamID = teams[0].Id
	} else {
		// Create a new team
		team := &model.Team{
			Name:        fmt.Sprintf("testteam_%d", model.GetMillis()),
			DisplayName: "Test Team",
			Type:        model.TeamOpen,
		}
		var createdTeam *model.Team
		createdTeam, _, err = s.adminClient.CreateTeam(ctx, team)
		require.NoError(t, err, "Failed to create team")
		teamID = createdTeam.Id
	}

	// Add user to team
	_, _, err = s.adminClient.AddTeamMember(ctx, teamID, createdUser.Id)
	require.NoError(t, err, "Failed to add user to team")

	// Create a new client for the user to login
	userClient := model.NewAPIv4Client(s.serverURL)

	// Log in to create a session
	sessionUser, _, err := userClient.Login(ctx, user.Email, user.Password)
	require.NoError(t, err, "Failed to login")

	// Get the session token (stored in client after login)
	token := userClient.AuthToken

	// Use admin client to get all sessions for this user
	sessions, _, err := userClient.GetSessions(ctx, sessionUser.Id, "")
	require.NoError(t, err, "Failed to get sessions")
	require.NotEmpty(t, sessions, "Should have at least one session after login")

	// The most recently created session should be the one we just created from login
	// Sessions are typically returned with most recent first, or we find by creation time
	var activeSession *model.Session
	var mostRecentTime int64
	for i := range sessions {
		if sessions[i].CreateAt > mostRecentTime {
			mostRecentTime = sessions[i].CreateAt
			activeSession = sessions[i]
		}
	}
	require.NotNil(t, activeSession, "Should find active session")

	// Update the token in the session object since GetSessions might not return it
	activeSession.Token = token

	return createdUser, activeSession
}

// CreateChannel creates a test channel for the given team and adds the user as a member
func (s *EmbeddedTestSuite) CreateChannel(t *testing.T, teamID, userID string) *model.Channel {
	ctx := context.Background()

	channelName := fmt.Sprintf("testchannel_%d", model.GetMillis())
	channel := &model.Channel{
		TeamId:      teamID,
		Name:        channelName,
		DisplayName: "Test Channel",
		Type:        model.ChannelTypeOpen,
		CreatorId:   userID,
	}

	createdChannel, _, err := s.adminClient.CreateChannel(ctx, channel)
	require.NoError(t, err, "Failed to create test channel")

	// Add the user as a member of the channel
	_, _, err = s.adminClient.AddChannelMember(ctx, createdChannel.Id, userID)
	require.NoError(t, err, "Failed to add user to channel")

	return createdChannel
}

// CreatePost creates a test post in the given channel
func (s *EmbeddedTestSuite) CreatePost(t *testing.T, channelID, userID, message string) *model.Post {
	ctx := context.Background()

	post := &model.Post{
		ChannelId: channelID,
		UserId:    userID,
		Message:   message,
	}

	createdPost, _, err := s.adminClient.CreatePost(ctx, post)
	require.NoError(t, err, "Failed to create test post")

	return createdPost
}

// testLogger is a simple logger implementation for tests that implements pluginapi.LogService
type testLogger struct {
	t *testing.T
}

func (l *testLogger) Debug(message string, keyValuePairs ...any) {
	l.t.Logf("[DEBUG] %s %v", message, keyValuePairs)
}

func (l *testLogger) Info(message string, keyValuePairs ...any) {
	l.t.Logf("[INFO] %s %v", message, keyValuePairs)
}

func (l *testLogger) Warn(message string, keyValuePairs ...any) {
	l.t.Logf("[WARN] %s %v", message, keyValuePairs)
}

func (l *testLogger) Error(message string, keyValuePairs ...any) {
	l.t.Logf("[ERROR] %s %v", message, keyValuePairs)
}

func (l *testLogger) Flush() error {
	// No-op for test logger
	return nil
}

// mockPluginAPI wraps plugintest.API and adds session handling for testing
type mockPluginAPI struct {
	*plugintest.API
	sessions map[string]*model.Session
	mu       sync.RWMutex
}

func newMockPluginAPI() *mockPluginAPI {
	mockAPI := &plugintest.API{}
	// Setup mock expectations for logging methods
	// Log methods accept variadic arguments, so we need to match various argument counts
	// Using mock.Anything for up to 20 arguments (should cover most log calls)
	anyArgs := make([]interface{}, 20)
	for i := range anyArgs {
		anyArgs[i] = mock.Anything
	}
	mockAPI.On("LogDebug", anyArgs...).Maybe()
	mockAPI.On("LogInfo", anyArgs...).Maybe()
	mockAPI.On("LogWarn", anyArgs...).Maybe()
	mockAPI.On("LogError", anyArgs...).Maybe()

	// Mock KV operations for session storage
	mockAPI.On("KVGet", mock.AnythingOfType("string")).Return(([]byte)(nil), (*model.AppError)(nil)).Maybe()
	mockAPI.On("KVSet", mock.AnythingOfType("string"), mock.Anything).Return(true, (*model.AppError)(nil)).Maybe()
	mockAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, (*model.AppError)(nil)).Maybe()
	mockAPI.On("KVDelete", mock.AnythingOfType("string")).Return((*model.AppError)(nil)).Maybe()

	// Mock User.Get for createEmbeddedSession
	mockAPI.On("GetUser", mock.AnythingOfType("string")).Return(&model.User{
		Id:    "test-user-id",
		Roles: "system_user",
	}, (*model.AppError)(nil)).Maybe()

	m := &mockPluginAPI{
		API:      mockAPI,
		sessions: make(map[string]*model.Session),
	}

	// Mock Session.Create for createEmbeddedSession
	mockAPI.On("CreateSession", mock.AnythingOfType("*model.Session")).Return(func(session *model.Session) *model.Session {
		// Generate a session ID if not set
		if session.Id == "" {
			session.Id = model.NewId()
		}
		if session.Token == "" {
			session.Token = model.NewId()
		}
		// Add to mock's session storage so GetSession can find it
		m.addSession(session)
		return session
	}, (*model.AppError)(nil)).Maybe()

	// Mock Session.Get for embedded session validation - delegate to mockPluginAPI.GetSession
	mockAPI.On("GetSession", mock.AnythingOfType("string")).Return(func(sessionID string) *model.Session {
		session, _ := m.GetSession(sessionID)
		return session
	}, func(sessionID string) *model.AppError {
		_, err := m.GetSession(sessionID)
		return err
	}).Maybe()

	// Mock Session.ExtendExpiry for session renewal
	mockAPI.On("ExtendSessionExpiry", mock.AnythingOfType("string"), mock.AnythingOfType("int64")).Return((*model.AppError)(nil)).Maybe()

	// Mock Configuration.GetConfig for sessionLengthDuration
	defaultConfig := &model.Config{}
	defaultConfig.SetDefaults()
	mockAPI.On("GetConfig").Return(defaultConfig).Maybe()

	return m
}

func (m *mockPluginAPI) addSession(session *model.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.Id] = session
}

func (m *mockPluginAPI) GetSession(sessionID string) (*model.Session, *model.AppError) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, exists := m.sessions[sessionID]
	if !exists {
		return nil, model.NewAppError("GetSession", "api.context.session_expired.app_error", nil, "", 401)
	}
	return session, nil
}

// CreateClient creates a Client using the actual EmbeddedServerClient.CreateClient() method
func (s *EmbeddedTestSuite) CreateClient(t *testing.T, user *model.User, session *model.Session) *Client {
	ctx := context.Background()

	// Create mock plugin API and add the session
	mockPluginAPI := newMockPluginAPI()
	mockPluginAPI.addSession(session)

	// Create a real pluginapi.Client to get a proper LogService
	// The first parameter is the plugin API, second is the driver (nil for tests)
	pluginAPIClient := pluginapi.NewClient(mockPluginAPI, nil)

	// Create a wrapper that implements EmbeddedMCPServer interface
	wrapper := &embeddedServerWrapper{
		server: s.embeddedServer,
		api:    mockPluginAPI,
	}

	// Create embedded server client
	embeddedClient := NewEmbeddedServerClient(wrapper, pluginAPIClient.Log, pluginAPIClient)

	// Create client
	client, err := embeddedClient.CreateClient(ctx, user.Id, session.Id)
	require.NoError(t, err, "Should create client successfully")
	require.NotNil(t, client, "Client should not be nil")

	return client
}

// embeddedServerWrapper wraps the MattermostInMemoryMCPServer to work with mock plugin API
type embeddedServerWrapper struct {
	server *mcpserver.MattermostInMemoryMCPServer
	api    *mockPluginAPI
}

// CreateClientTransport implements EmbeddedMCPServer interface
// Note: The signature must match the interface (takes *pluginapi.Client), but in tests
// we ignore the passed pluginAPI parameter and use our mock instead
func (w *embeddedServerWrapper) CreateClientTransport(userID, sessionID string, pluginAPI *pluginapi.Client) (*mcp.InMemoryTransport, error) {
	// Create token resolver using our mock (ignore the passed pluginAPI in tests)
	tokenResolver := func(sid string) (string, error) {
		session, err := w.api.GetSession(sid)
		if err != nil {
			return "", err
		}
		if session == nil {
			return "", fmt.Errorf("session not found")
		}
		return session.Token, nil
	}

	// Call the underlying server's CreateConnectionForUser
	return w.server.CreateConnectionForUser(userID, sessionID, tokenResolver)
}

// CreateClientManager creates a ClientManager for testing
func (s *EmbeddedTestSuite) CreateClientManager(t *testing.T, session *model.Session) *ClientManager {
	// Create mock plugin API and add the session
	mockPluginAPI := newMockPluginAPI()
	mockPluginAPI.addSession(session)

	// Mock GetUser to return the actual user from the session
	mockPluginAPI.API.On("GetUser", session.UserId).Unset()
	mockPluginAPI.API.On("GetUser", session.UserId).Return(&model.User{
		Id:    session.UserId,
		Roles: "system_user",
	}, (*model.AppError)(nil))

	// Pre-populate KV with the session ID so ensureEmbeddedSessionID finds it
	// This simulates the session already being stored for this user
	// Use Unset first to remove the generic mock, then add specific one
	embeddedSessionKey := fmt.Sprintf("mcp_embedded_session_id_%s", session.UserId)
	mockPluginAPI.API.On("KVGet", embeddedSessionKey).Unset()
	mockPluginAPI.API.On("KVGet", embeddedSessionKey).Return([]byte(session.Id), (*model.AppError)(nil))

	// Create a real pluginapi.Client to get a proper LogService
	pluginAPIClient := pluginapi.NewClient(mockPluginAPI, nil)

	// Create wrapper
	wrapper := &embeddedServerWrapper{
		server: s.embeddedServer,
		api:    mockPluginAPI,
	}

	// Create config for testing (no remote servers, just embedded)
	config := Config{
		EmbeddedServer: EmbeddedServerConfig{
			Enabled: true,
		},
		Servers:            []ServerConfig{}, // No remote servers for testing
		IdleTimeoutMinutes: 30,
	}

	// Create ClientManager
	manager := NewClientManager(config, pluginAPIClient.Log, pluginAPIClient, nil, wrapper)
	require.NotNil(t, manager, "ClientManager should not be nil")

	return manager
}
