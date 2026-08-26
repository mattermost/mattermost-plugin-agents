// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// instrumentedMCPServer is a real MCP server — a go-sdk streamable HTTP server
// speaking the actual protocol — that records the JSON-RPC methods it is asked
// to serve. firstRequestDelay stalls only the first request so one slow
// handshake per server can be distinguished from N sequential handshakes.
type instrumentedMCPServer struct {
	*httptest.Server

	delayOnce sync.Once

	mu       sync.Mutex
	requests int
	methods  map[string]int
}

func newInstrumentedMCPServer(toolPrefix string, toolCount int, firstRequestDelay time.Duration) *instrumentedMCPServer {
	server := gomcp.NewServer(&gomcp.Implementation{Name: toolPrefix, Version: "1.0"}, nil)
	for i := range toolCount {
		addTestMCPTool(server, fmt.Sprintf("%s_%d", toolPrefix, i))
	}

	handler := gomcp.NewStreamableHTTPHandler(
		func(*http.Request) *gomcp.Server { return server },
		&gomcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)

	instrumented := &instrumentedMCPServer{methods: map[string]int{}}
	instrumented.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		instrumented.record(body)

		if firstRequestDelay > 0 && r.Method == http.MethodPost {
			instrumented.delayOnce.Do(func() { time.Sleep(firstRequestDelay) })
		}
		handler.ServeHTTP(w, r)
	}))

	return instrumented
}

// newUnreachableMCPServer records requests and fails every one of them.
func newUnreachableMCPServer() *instrumentedMCPServer {
	instrumented := &instrumentedMCPServer{methods: map[string]int{}}
	instrumented.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		instrumented.record(body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	return instrumented
}

func (s *instrumentedMCPServer) record(body []byte) {
	var methods []string

	var single struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &single); err == nil && single.Method != "" {
		methods = append(methods, single.Method)
	} else {
		var batch []struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &batch); err == nil {
			for _, entry := range batch {
				if entry.Method != "" {
					methods = append(methods, entry.Method)
				}
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	for _, method := range methods {
		s.methods[method]++
	}
}

func (s *instrumentedMCPServer) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// dialCount reports how many times a client completed a handshake and listed
// tools. The harness never lets the shared tools cache hit, so one tools/list
// is exactly one dial.
func (s *instrumentedMCPServer) dialCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.methods[listToolsMethod]
}

// delayedEmbeddedServer is a real in-memory embedded MCP server that can stall
// its first connection.
type delayedEmbeddedServer struct {
	ctx        context.Context
	server     *gomcp.Server
	delay      time.Duration
	delayOnce  sync.Once
	transports atomic.Int64
}

func (d *delayedEmbeddedServer) CreateClientTransport(_ string, _ string, _ *pluginapi.Client) (*gomcp.InMemoryTransport, error) {
	d.transports.Add(1)
	if d.delay > 0 {
		d.delayOnce.Do(func() { time.Sleep(d.delay) })
	}

	serverTransport, clientTransport := gomcp.NewInMemoryTransports()
	go func() {
		_ = d.server.Run(d.ctx, serverTransport)
	}()
	return clientTransport, nil
}

// runtimeHarness wires a ClientManager to real MCP servers of every kind:
// go-sdk streamable HTTP servers for remote origins, PluginHTTP-forwarded
// go-sdk servers for plugin origins, and an in-memory embedded server. Nothing
// is stubbed at the MCP protocol level.
type runtimeHarness struct {
	t *testing.T

	remote    []ServerConfig
	remoteSrv map[string]*instrumentedMCPServer

	plugins   []PluginServerConfig
	pluginSrv map[string]*instrumentedMCPServer

	embedded *delayedEmbeddedServer

	userSessions map[string]string
}

func newRuntimeHarness(t *testing.T, userIDs ...string) *runtimeHarness {
	t.Helper()

	harness := &runtimeHarness{
		t:            t,
		remoteSrv:    map[string]*instrumentedMCPServer{},
		pluginSrv:    map[string]*instrumentedMCPServer{},
		userSessions: map[string]string{},
	}
	for _, userID := range userIDs {
		harness.userSessions[userID] = userID + "-session"
	}
	return harness
}

// addRemote registers a healthy remote MCP server whose first handshake stalls
// for delay.
func (h *runtimeHarness) addRemote(name string, toolCount int, delay time.Duration) ServerConfig {
	h.t.Helper()

	server := newInstrumentedMCPServer(name, toolCount, delay)
	h.t.Cleanup(server.Close)
	h.remoteSrv[name] = server

	cfg := ServerConfig{Name: name, BaseURL: server.URL, Enabled: true}
	h.remote = append(h.remote, cfg)
	return cfg
}

// addUnreachableRemote registers an enabled remote server that fails every
// request.
func (h *runtimeHarness) addUnreachableRemote(name string) ServerConfig {
	h.t.Helper()

	server := newUnreachableMCPServer()
	h.t.Cleanup(server.Close)
	h.remoteSrv[name] = server

	cfg := ServerConfig{Name: name, BaseURL: server.URL, Enabled: true}
	h.remote = append(h.remote, cfg)
	return cfg
}

func (h *runtimeHarness) addDisabledRemote(name string) ServerConfig {
	h.t.Helper()

	cfg := h.addRemote(name, 1, 0)
	h.remote[len(h.remote)-1].Enabled = false
	cfg.Enabled = false
	return cfg
}

func (h *runtimeHarness) addPlugin(pluginID, name string, toolCount int, delay time.Duration) PluginServerConfig {
	h.t.Helper()

	server := newInstrumentedMCPServer(name, toolCount, delay)
	h.t.Cleanup(server.Close)
	h.pluginSrv[pluginID] = server

	cfg := PluginServerConfig{PluginID: pluginID, Name: name, Path: "/mcp", Enabled: true}
	h.plugins = append(h.plugins, cfg)
	return cfg
}

func (h *runtimeHarness) addUnreachablePlugin(pluginID, name string) PluginServerConfig {
	h.t.Helper()

	server := newUnreachableMCPServer()
	h.t.Cleanup(server.Close)
	h.pluginSrv[pluginID] = server

	cfg := PluginServerConfig{PluginID: pluginID, Name: name, Path: "/mcp", Enabled: true}
	h.plugins = append(h.plugins, cfg)
	return cfg
}

func (h *runtimeHarness) withEmbedded(toolName string, delay time.Duration) {
	h.t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	h.t.Cleanup(cancel)

	h.embedded = &delayedEmbeddedServer{
		ctx:    ctx,
		server: newTestMCPServer(0, toolName),
		delay:  delay,
	}
}

// pluginForwarder routes PluginHTTP calls to the right plugin server. The
// PluginHTTPRoundTripper rewrites paths to "/<pluginID><path>".
func (h *runtimeHarness) pluginForwarder() *fakePluginHTTPClient {
	return &fakePluginHTTPClient{
		pluginHTTP: func(req *http.Request) *http.Response {
			for pluginID, server := range h.pluginSrv {
				if strings.HasPrefix(req.URL.Path, "/"+pluginID) {
					recorder := httptest.NewRecorder()
					server.Config.Handler.ServeHTTP(recorder, req)
					return recorder.Result()
				}
			}
			recorder := httptest.NewRecorder()
			recorder.WriteHeader(http.StatusNotFound)
			return recorder.Result()
		},
	}
}

func (h *runtimeHarness) newManager() *ClientManager {
	h.t.Helper()

	sessionByID := map[string]*model.Session{}
	userByID := map[string]*model.User{}
	sessionKeys := map[string]string{}
	for userID, sessionID := range h.userSessions {
		sessionByID[sessionID] = &model.Session{
			Id:        sessionID,
			UserId:    userID,
			Token:     userID + "-token",
			ExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
		}
		userByID[userID] = &model.User{Id: userID, Roles: "system_user"}
		sessionKeys[buildEmbeddedSessionKey(userID)] = sessionID
	}

	// KVGet only answers the embedded-session lookups, so every shared
	// tools-cache read misses and each dial performs a real tools/list.
	pluginAPI := pluginapi.NewClient(&fixedPluginAPI{
		kvGet: func(key string) ([]byte, *model.AppError) {
			if sessionID, ok := sessionKeys[key]; ok {
				return []byte(sessionID), nil
			}
			return nil, nil
		},
		sessionByID: sessionByID,
		userByID:    userByID,
	}, nil)

	var embedded EmbeddedMCPServer
	if h.embedded != nil {
		embedded = h.embedded
	}

	manager := NewClientManager(
		Config{
			Enabled:            true,
			Servers:            h.remote,
			PluginServers:      h.plugins,
			EmbeddedServer:     EmbeddedServerConfig{Enabled: true},
			IdleTimeoutMinutes: 30,
		},
		pluginAPI.Log,
		pluginAPI,
		nil,
		embedded,
		&http.Client{},
		h.pluginForwarder(),
	)
	h.t.Cleanup(manager.Close)

	for _, cfg := range h.plugins {
		manager.RegisterPluginServer(cfg)
	}

	return manager
}

func toolOrigins(tools []llm.Tool) map[string]int {
	origins := map[string]int{}
	for _, tool := range tools {
		origins[tool.ServerOrigin]++
	}
	return origins
}

// A cold request dials remote, embedded, and plugin servers in one batch, so
// its wall-clock cost tracks the slowest server rather than their sum.
func TestGetToolsForUserDialsEveryServerTypeConcurrently(t *testing.T) {
	const dialDelay = 400 * time.Millisecond

	harness := newRuntimeHarness(t, "alice")
	harness.addRemote("remotea", 1, dialDelay)
	harness.addRemote("remoteb", 1, dialDelay)
	harness.addRemote("remotec", 1, dialDelay)
	harness.addPlugin("com.example.one", "pluginone", 1, dialDelay)
	harness.addPlugin("com.example.two", "plugintwo", 1, dialDelay)
	harness.withEmbedded("embedded_tool", dialDelay)

	manager := harness.newManager()

	start := time.Now()
	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	elapsed := time.Since(start)

	require.Nil(t, mcpErrors)
	require.Len(t, tools, 6, "expected one tool from each of the six servers")

	// Six servers each stall their handshake for dialDelay. Sequential dialing
	// costs at least 6x that; concurrent dialing costs about 1x.
	require.Less(t, elapsed, 3*dialDelay,
		"cold connect took %s; six %s servers dialed sequentially would take at least %s",
		elapsed, dialDelay, 6*dialDelay)
}

// Servers the admin, the user, the agent, or the license rules out must not be
// contacted at all, not merely have their tools filtered out afterwards.
func TestGetToolsForUserNeverContactsIneligibleServers(t *testing.T) {
	testCases := []struct {
		name string
		// selection is built from the eligible server's origin.
		selection func(eligible, ineligible ServerConfig) ToolSelection
		// disableIneligible models the admin turning the server off instead.
		disableIneligible bool
	}{
		{
			name: "admin-disabled server",
			selection: func(ServerConfig, ServerConfig) ToolSelection {
				return ToolSelection{}
			},
			disableIneligible: true,
		},
		{
			name: "user-disabled server",
			selection: func(_ ServerConfig, ineligible ServerConfig) ToolSelection {
				return ToolSelection{DeniedOrigins: []string{ineligible.BaseURL}}
			},
		},
		{
			name: "server outside the agent allowlist",
			selection: func(eligible ServerConfig, _ ServerConfig) ToolSelection {
				return ToolSelection{AllowedOrigins: []string{eligible.BaseURL, EmbeddedClientKey}}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			harness := newRuntimeHarness(t, "alice")
			eligible := harness.addRemote("eligible", 1, 0)
			var ineligible ServerConfig
			if tc.disableIneligible {
				ineligible = harness.addDisabledRemote("ineligible")
			} else {
				ineligible = harness.addRemote("ineligible", 1, 0)
			}

			manager := harness.newManager()
			tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", tc.selection(eligible, ineligible))

			require.Nil(t, mcpErrors)
			require.Equal(t, map[string]int{eligible.BaseURL: 1}, toolOrigins(tools))
			require.Positive(t, harness.remoteSrv["eligible"].requestCount())
			require.Zero(t, harness.remoteSrv["ineligible"].requestCount(),
				"an ineligible MCP server must never be contacted")
		})
	}
}

// Without the MCP license, remote and plugin servers are off limits while the
// embedded Mattermost server stays available.
func TestGetToolsForUserSkipsRemoteServersWhenUnlicensed(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	harness.addRemote("remotea", 1, 0)
	harness.addPlugin("com.example.one", "pluginone", 1, 0)
	harness.withEmbedded("embedded_tool", 0)

	manager := harness.newManager()
	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{ExcludeRemoteServers: true})

	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{EmbeddedClientKey: 1}, toolOrigins(tools))
	require.Zero(t, harness.remoteSrv["remotea"].requestCount())
	require.Zero(t, harness.pluginSrv["com.example.one"].requestCount())
}

// One user client is filled in incrementally: switching between agents with
// different allowlists adds the newly eligible server without re-dialing the
// one that was already connected.
func TestGetToolsForUserPopulatesOneUserClientIncrementally(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	first := harness.addRemote("first", 1, 0)
	second := harness.addRemote("second", 1, 0)

	manager := harness.newManager()
	ctx := context.Background()

	tools, mcpErrors := manager.GetToolsForUser(ctx, "alice", ToolSelection{AllowedOrigins: []string{first.BaseURL}})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{first.BaseURL: 1}, toolOrigins(tools))
	require.Equal(t, 1, harness.remoteSrv["first"].dialCount())
	require.Zero(t, harness.remoteSrv["second"].requestCount())

	tools, mcpErrors = manager.GetToolsForUser(ctx, "alice", ToolSelection{AllowedOrigins: []string{second.BaseURL}})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{second.BaseURL: 1}, toolOrigins(tools),
		"a server that is no longer eligible must not contribute tools even though its session is cached")
	require.Equal(t, 1, harness.remoteSrv["first"].dialCount(), "the first server must not be re-dialed")
	require.Equal(t, 1, harness.remoteSrv["second"].dialCount())

	tools, mcpErrors = manager.GetToolsForUser(ctx, "alice", ToolSelection{AllowedOrigins: []string{first.BaseURL, second.BaseURL}})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{first.BaseURL: 1, second.BaseURL: 1}, toolOrigins(tools))
	require.Equal(t, 1, harness.remoteSrv["first"].dialCount(), "both sessions must be reused")
	require.Equal(t, 1, harness.remoteSrv["second"].dialCount())
}

// Concurrent cold requests for one user must converge on a single session per
// server rather than each opening their own.
func TestGetToolsForUserConcurrentColdRequestsDialEachServerOnce(t *testing.T) {
	const callers = 16

	harness := newRuntimeHarness(t, "alice")
	harness.addRemote("remotea", 1, 150*time.Millisecond)
	harness.addRemote("remoteb", 1, 150*time.Millisecond)
	harness.addPlugin("com.example.one", "pluginone", 1, 150*time.Millisecond)
	harness.withEmbedded("embedded_tool", 150*time.Millisecond)

	manager := harness.newManager()

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([][]llm.Tool, callers)
	for caller := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tools, _ := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
			results[caller] = tools
		}()
	}
	close(start)
	wg.Wait()

	for caller, tools := range results {
		require.Len(t, tools, 4, "caller %d should see every server's tools", caller)
	}
	require.Equal(t, 1, harness.remoteSrv["remotea"].dialCount())
	require.Equal(t, 1, harness.remoteSrv["remoteb"].dialCount())
	require.Equal(t, 1, harness.pluginSrv["com.example.one"].dialCount())
	require.Equal(t, int64(1), harness.embedded.transports.Load())
}

// A broken server must not cost the user the tools from healthy ones.
func TestGetToolsForUserReturnsPartialSuccess(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	healthy := harness.addRemote("healthy", 2, 0)
	harness.addUnreachableRemote("broken")
	harness.addUnreachablePlugin("com.example.broken", "brokenplugin")
	harness.withEmbedded("embedded_tool", 0)

	manager := harness.newManager()
	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})

	require.Equal(t, map[string]int{healthy.BaseURL: 2, EmbeddedClientKey: 1}, toolOrigins(tools))
	require.NotNil(t, mcpErrors)
	require.Len(t, mcpErrors.Errors, 2, "both the broken remote and the broken plugin should report an error")
	require.Empty(t, mcpErrors.ToolAuthErrors, "transport failures are not OAuth failures")
}

// A remote failure is remembered until the user client is invalidated, so a
// dead server is not re-dialed on every request. A plugin failure is retried,
// because a source plugin can come back without any cache invalidation.
func TestGetToolsForUserRemoteFailuresAreStickyAndPluginFailuresRetry(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	harness.addUnreachableRemote("broken")
	harness.addUnreachablePlugin("com.example.broken", "brokenplugin")

	manager := harness.newManager()
	ctx := context.Background()

	_, mcpErrors := manager.GetToolsForUser(ctx, "alice", ToolSelection{})
	require.NotNil(t, mcpErrors)
	require.Len(t, mcpErrors.Errors, 2)

	remoteRequests := harness.remoteSrv["broken"].requestCount()
	pluginRequests := harness.pluginSrv["com.example.broken"].requestCount()
	require.Positive(t, remoteRequests)
	require.Positive(t, pluginRequests)

	_, mcpErrors = manager.GetToolsForUser(ctx, "alice", ToolSelection{})
	require.NotNil(t, mcpErrors)
	require.Len(t, mcpErrors.Errors, 2, "both failures must still be reported")

	require.Equal(t, remoteRequests, harness.remoteSrv["broken"].requestCount(),
		"a remembered remote failure must not be re-dialed")
	require.Greater(t, harness.pluginSrv["com.example.broken"].requestCount(), pluginRequests,
		"a plugin failure must be retried on the next request")
}

// Refreshing drops the remembered state so every eligible server is
// rediscovered, including one that previously failed.
func TestRefreshToolsForUserRediscoversEligibleServers(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	healthy := harness.addRemote("healthy", 1, 0)

	manager := harness.newManager()
	ctx := context.Background()

	tools, mcpErrors := manager.GetToolsForUser(ctx, "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{healthy.BaseURL: 1}, toolOrigins(tools))
	require.Equal(t, 1, harness.remoteSrv["healthy"].dialCount())

	tools, mcpErrors, err := manager.RefreshToolsForUser(ctx, "alice", ToolSelection{})
	require.NoError(t, err)
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{healthy.BaseURL: 1}, toolOrigins(tools))
	require.Equal(t, 2, harness.remoteSrv["healthy"].dialCount(), "refresh must force a rediscovery")
}

// Remote sessions are cached across requests, so their dials must survive the
// request being canceled. Embedded and plugin dials are per-request and abort.
func TestGetToolsForUserCancellationFollowsTransportPolicy(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	remote := harness.addRemote("remotea", 1, 0)
	harness.addPlugin("com.example.one", "pluginone", 1, 0)
	harness.withEmbedded("embedded_tool", 0)

	manager := harness.newManager()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tools, mcpErrors := manager.GetToolsForUser(ctx, "alice", ToolSelection{})

	require.Equal(t, map[string]int{remote.BaseURL: 1}, toolOrigins(tools),
		"a canceled request must still warm the shared remote session, and must abort the per-request embedded and plugin dials")
	require.NotNil(t, mcpErrors)
	require.Len(t, mcpErrors.Errors, 1,
		"only the plugin dial should report an error; the embedded server degrades silently")
	require.Equal(t, int64(1), harness.embedded.transports.Load(),
		"the embedded dial should be attempted and then abort, not be skipped")
}

// Duplicate names and duplicate endpoints make every member of the group
// ambiguous, so none of them is contacted until an admin fixes the config.
// Activation still succeeds and the healthy server keeps working.
func TestGetToolsForUserExcludesConflictingServerConfigurations(t *testing.T) {
	testCases := []struct {
		name    string
		mangle  func(harness *runtimeHarness)
		healthy string
	}{
		{
			name: "duplicate names",
			mangle: func(harness *runtimeHarness) {
				harness.remote[1].Name = harness.remote[2].Name
			},
			healthy: "healthy",
		},
		{
			name: "canonically equivalent URLs",
			mangle: func(harness *runtimeHarness) {
				harness.remote[2].BaseURL = harness.remote[1].BaseURL + "/"
			},
			healthy: "healthy",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			harness := newRuntimeHarness(t, "alice")
			healthy := harness.addRemote("healthy", 1, 0)
			harness.addRemote("dupone", 1, 0)
			harness.addRemote("duptwo", 1, 0)
			tc.mangle(harness)

			manager := harness.newManager()
			tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})

			require.Nil(t, mcpErrors)
			require.Equal(t, map[string]int{healthy.BaseURL: 1}, toolOrigins(tools))
			require.Positive(t, harness.remoteSrv[tc.healthy].requestCount())
			require.Zero(t, harness.remoteSrv["dupone"].requestCount())
			require.Zero(t, harness.remoteSrv["duptwo"].requestCount())
		})
	}
}

// Repeated cold connects must not accumulate goroutines: each user client is
// closed on invalidation and every dial either commits or closes its session.
func TestGetToolsForUserDoesNotLeakGoroutinesAcrossColdConnects(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	harness.addRemote("remotea", 1, 0)
	harness.addPlugin("com.example.one", "pluginone", 1, 0)
	harness.withEmbedded("embedded_tool", 0)

	manager := harness.newManager()
	ctx := context.Background()

	// Warm up first so one-off transport goroutines are not counted as growth.
	for range 3 {
		manager.GetToolsForUser(ctx, "alice", ToolSelection{})
		manager.InvalidateUserClients("alice")
	}

	time.Sleep(200 * time.Millisecond)
	before := runtime.NumGoroutine()

	for range 10 {
		tools, _ := manager.GetToolsForUser(ctx, "alice", ToolSelection{})
		require.Len(t, tools, 3)
		manager.InvalidateUserClients("alice")
	}

	time.Sleep(300 * time.Millisecond)
	after := runtime.NumGoroutine()
	require.LessOrEqual(t, after, before+4,
		"goroutine count grew across cold connects: before=%d after=%d", before, after)
}

// ensureConnections is the only place connect failures are classified, so its
// policy is asserted directly against each failure shape.
func TestEnsureConnectionsClassifiesFailures(t *testing.T) {
	authURL := "https://mattermost.example.com/plugins/mattermost-ai/mcp/oauth/Jira/start"

	testCases := []struct {
		name                string
		task                func(uc *UserClients) connectTask
		expectAuthErrors    int
		expectGenericErrors int
		expectAuthURL       string
	}{
		{
			name: "oauth failures become tool auth errors",
			task: func(*UserClients) connectTask {
				return connectTask{
					origin:     "https://jira.example.com",
					serverID:   "Jira",
					serverName: "Jira",
					dial: func() (*Client, error) {
						return nil, &OAuthNeededError{authURL: authURL}
					},
				}
			},
			expectAuthErrors: 1,
			expectAuthURL:    authURL,
		},
		{
			name: "transport failures become generic errors",
			task: func(*UserClients) connectTask {
				return connectTask{
					origin:     "https://jira.example.com",
					serverID:   "Jira",
					serverName: "Jira",
					dial: func() (*Client, error) {
						return nil, fmt.Errorf("connection refused")
					},
				}
			},
			expectGenericErrors: 1,
		},
		{
			name: "silent failures are not surfaced",
			task: func(*UserClients) connectTask {
				return connectTask{
					origin:     EmbeddedClientKey,
					serverID:   EmbeddedClientKey,
					serverName: EmbeddedServerName,
					silent:     true,
					dial: func() (*Client, error) {
						return nil, fmt.Errorf("embedded server unavailable")
					},
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			uc := NewUserClients("alice", newTestLogService(), nil, nil, nil)

			mcpErrors := uc.ensureConnections(context.Background(), []connectTask{tc.task(uc)})

			if tc.expectAuthErrors == 0 && tc.expectGenericErrors == 0 {
				require.Nil(t, mcpErrors)
				return
			}

			require.NotNil(t, mcpErrors)
			require.Len(t, mcpErrors.ToolAuthErrors, tc.expectAuthErrors)
			require.Len(t, mcpErrors.Errors, tc.expectGenericErrors)
			if tc.expectAuthURL != "" {
				require.Equal(t, tc.expectAuthURL, mcpErrors.ToolAuthErrors[0].AuthURL)
				require.Equal(t, "Jira", mcpErrors.ToolAuthErrors[0].ServerName)
				require.Equal(t, "https://jira.example.com", mcpErrors.ToolAuthErrors[0].ServerOrigin)
			}
		})
	}
}

// Errors are merged in task order so the response does not depend on which
// server happened to answer first.
func TestEnsureConnectionsMergesErrorsInTaskOrder(t *testing.T) {
	uc := NewUserClients("alice", newTestLogService(), nil, nil, nil)

	tasks := make([]connectTask, 0, 8)
	for i := range 8 {
		origin := fmt.Sprintf("https://server-%d.example.com", i)
		// Later tasks finish first.
		delay := time.Duration(8-i) * 10 * time.Millisecond
		tasks = append(tasks, connectTask{
			origin:     origin,
			serverID:   origin,
			serverName: origin,
			dial: func() (*Client, error) {
				time.Sleep(delay)
				return nil, fmt.Errorf("failed %s", origin)
			},
		})
	}

	mcpErrors := uc.ensureConnections(context.Background(), tasks)

	require.NotNil(t, mcpErrors)
	require.Len(t, mcpErrors.Errors, len(tasks))
	for i, err := range mcpErrors.Errors {
		require.EqualError(t, err, fmt.Sprintf("failed https://server-%d.example.com", i))
	}
}

func TestEnsureConnectionsBoundsConcurrency(t *testing.T) {
	uc := NewUserClients("alice", newTestLogService(), nil, nil, nil)
	const taskCount = maxConcurrentConnections*2 + 3

	var active atomic.Int64
	var peak atomic.Int64
	tasks := make([]connectTask, taskCount)
	for i := range tasks {
		tasks[i] = connectTask{
			origin:   fmt.Sprintf("test://server-%d", i),
			serverID: fmt.Sprintf("server-%d", i),
			dial: func() (*Client, error) {
				current := active.Add(1)
				defer active.Add(-1)
				for {
					previous := peak.Load()
					if current <= previous || peak.CompareAndSwap(previous, current) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				return nil, nil
			},
		}
	}

	require.Nil(t, uc.ensureConnections(context.Background(), tasks))
	require.LessOrEqual(t, peak.Load(), int64(maxConcurrentConnections))
	require.Greater(t, peak.Load(), int64(1))
}

// A dial that finishes after the user client is closed must not leave an
// unreachable session behind.
func TestEnsureConnectionsClosesSessionsCommittedAfterClose(t *testing.T) {
	server := newInstrumentedMCPServer("late", 1, 0)
	t.Cleanup(server.Close)

	uc := NewUserClients("alice", newTestLogService(), nil, &http.Client{}, nil)

	release := make(chan struct{})
	var connected sync.WaitGroup
	connected.Add(1)

	go func() {
		uc.ensureConnections(context.Background(), []connectTask{{
			origin:     server.URL,
			serverID:   "late",
			serverName: "late",
			dial: func() (*Client, error) {
				<-release
				return NewClient(context.Background(), "alice", ServerConfig{Name: "late", BaseURL: server.URL, Enabled: true},
					newTestLogService(), nil, &http.Client{}, nil, false)
			},
		}})
		connected.Done()
	}()

	uc.Close()
	close(release)
	connected.Wait()

	require.Empty(t, uc.snapshotClients(), "a session committed after Close must be discarded")
}

func TestToolSelectionAllows(t *testing.T) {
	const remote = "https://mcp.example.com/mcp"
	const plugin = "plugin://com.example.mcp"

	testCases := []struct {
		name      string
		selection ToolSelection
		origin    string
		allowed   bool
	}{
		{name: "zero value allows every origin", origin: remote, allowed: true},
		{name: "zero value allows the embedded server", origin: EmbeddedClientKey, allowed: true},
		{name: "empty origin is never allowed", origin: "", allowed: false},
		{
			name:      "allowlist admits a listed origin",
			selection: ToolSelection{AllowedOrigins: []string{remote}},
			origin:    remote,
			allowed:   true,
		},
		{
			name:      "allowlist rejects an unlisted origin",
			selection: ToolSelection{AllowedOrigins: []string{remote}},
			origin:    plugin,
			allowed:   false,
		},
		{
			name:      "empty allowlist selects nothing",
			selection: ToolSelection{AllowedOrigins: []string{}},
			origin:    remote,
			allowed:   false,
		},
		{
			name:      "allowlist tolerates trailing slashes",
			selection: ToolSelection{AllowedOrigins: []string{remote + "/"}},
			origin:    remote,
			allowed:   true,
		},
		{
			name:      "denylist wins over the allowlist",
			selection: ToolSelection{AllowedOrigins: []string{remote}, DeniedOrigins: []string{remote}},
			origin:    remote,
			allowed:   false,
		},
		{
			name:      "excluding remote servers rejects a plugin origin",
			selection: ToolSelection{ExcludeRemoteServers: true},
			origin:    plugin,
			allowed:   false,
		},
		{
			name:      "excluding remote servers keeps the embedded server",
			selection: ToolSelection{ExcludeRemoteServers: true},
			origin:    EmbeddedClientKey,
			allowed:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.allowed, tc.selection.Allows(tc.origin))
		})
	}
}
