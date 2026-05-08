// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package pluginmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type echoIn struct {
	Message string `json:"message" jsonschema:"the message to echo back"`
}

type echoOut struct {
	Echoed string `json:"echoed"`
}

// newTestServerWithAuthInjection wraps s.ServeHTTP with an httptest.Server
// that injects Mattermost-Plugin-ID + extraHeaders. Tests verifying the
// security gate skip this helper and call s.ServeHTTP directly.
func newTestServerWithAuthInjection(t *testing.T, s *Server, extraHeaders http.Header) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Mattermost-Plugin-ID", agentsPluginID)
		for k, vs := range extraHeaders {
			for _, v := range vs {
				r.Header.Add(k, v)
			}
		}
		s.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func connectClient(ctx context.Context, t *testing.T, endpoint string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "pluginmcp-test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = session.Close()
	})
	return session
}

func registerEchoTool(s *Server, toolName string) {
	AddTool[echoIn, echoOut](s, &mcp.Tool{
		Name:        toolName,
		Description: "Echoes the input message back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, echoOut, error) {
		return &mcp.CallToolResult{}, echoOut{Echoed: in.Message}, nil
	})
}

func TestAddTool_PrependsNamespace(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, Config{
		PluginID: "com.example.demo",
		Name:     "Demo",
		Path:     "/mcp",
	})
	registerEchoTool(s, "echo")

	ts := newTestServerWithAuthInjection(t, s, nil)
	session := connectClient(ctx, t, ts.URL)

	got, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, got.Tools, 1)
	assert.Equal(t, "com_example_demo__echo", got.Tools[0].Name)
}

func TestAddTool_NoDoublePrefix(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, Config{
		PluginID: "com.example.demo",
		Name:     "Demo",
		Path:     "/mcp",
	})
	registerEchoTool(s, "com_example_demo__echo")

	ts := newTestServerWithAuthInjection(t, s, nil)
	session := connectClient(ctx, t, ts.URL)

	got, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, got.Tools, 1)
	assert.Equal(t, "com_example_demo__echo", got.Tools[0].Name,
		"no doubled prefix should be emitted when the caller already prefixed")
}

// TestAddTool_SanitizesInvalidPluginID confirms only the tool-name prefix is
// sanitized; the raw PluginID stays in s.config for routing/registry use.
func TestAddTool_SanitizesInvalidPluginID(t *testing.T) {
	ctx := context.Background()

	rawPluginID := "com mattermost/@evil"
	s := NewServer(nil, Config{
		PluginID: rawPluginID,
		Name:     "Evil",
		Path:     "/mcp",
	})
	registerEchoTool(s, "echo")

	ts := newTestServerWithAuthInjection(t, s, nil)
	session := connectClient(ctx, t, ts.URL)

	got, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, got.Tools, 1)
	assert.Equal(t, "com_mattermost__evil__echo", got.Tools[0].Name,
		"sanitizer should replace invalid runes with '_'")

	assert.Equal(t, rawPluginID, s.config.PluginID)
}

func TestAddTool_NoDoublePrefix_Sanitized(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, Config{
		PluginID: "has space",
		Name:     "Test",
		Path:     "/mcp",
	})
	registerEchoTool(s, "echo")
	registerEchoTool(s, "has_space__already")

	ts := newTestServerWithAuthInjection(t, s, nil)
	session := connectClient(ctx, t, ts.URL)

	got, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, got.Tools, 2)

	names := make([]string, len(got.Tools))
	for i, to := range got.Tools {
		names[i] = to.Name
	}
	assert.Contains(t, names, "has_space__echo")
	assert.Contains(t, names, "has_space__already")
}

// TestAddTool_SchemaGenerated confirms schema inference is delegated to the
// go-sdk; the jsonschema tag must reach the wire.
func TestAddTool_SchemaGenerated(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, Config{
		PluginID: "com.example.demo",
		Name:     "Demo",
		Path:     "/mcp",
	})
	registerEchoTool(s, "echo")

	ts := newTestServerWithAuthInjection(t, s, nil)
	session := connectClient(ctx, t, ts.URL)

	got, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, got.Tools, 1)

	schema, ok := got.Tools[0].InputSchema.(map[string]any)
	require.True(t, ok, "InputSchema should be a map[string]any on the wire, got %T", got.Tools[0].InputSchema)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema should have a properties map")
	message, ok := props["message"].(map[string]any)
	require.True(t, ok, "properties should contain 'message'")
	assert.Equal(t, "the message to echo back", message["description"],
		"jsonschema tag should be honored via delegated schema inference")
}

func TestNewServer_DefaultVersion(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, Config{
		PluginID: "x",
		Name:     "X",
		Path:     "/mcp",
	})
	ts := newTestServerWithAuthInjection(t, s, nil)
	session := connectClient(ctx, t, ts.URL)

	info := session.InitializeResult()
	require.NotNil(t, info)
	require.NotNil(t, info.ServerInfo)
	assert.Equal(t, "0.0.1", info.ServerInfo.Version)
}

func TestNewServer_ExplicitVersion(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, Config{
		PluginID: "x",
		Name:     "X",
		Path:     "/mcp",
		Version:  "1.2.3",
	})
	ts := newTestServerWithAuthInjection(t, s, nil)
	session := connectClient(ctx, t, ts.URL)

	info := session.InitializeResult()
	require.NotNil(t, info)
	require.NotNil(t, info.ServerInfo)
	assert.Equal(t, "1.2.3", info.ServerInfo.Version)
}

func TestServeHTTP_MissingPluginIDHeader_403(t *testing.T) {
	s := NewServer(nil, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	s.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.True(t, strings.HasPrefix(rec.Body.String(), "forbidden"),
		"body should start with 'forbidden'; got %q", rec.Body.String())
}

func TestServeHTTP_WrongPluginIDHeader_403(t *testing.T) {
	s := NewServer(nil, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("Mattermost-Plugin-ID", "com.evil.plugin")
	s.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestServeHTTP_CorrectPluginID_Delegates(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, Config{
		PluginID: "com.example.demo",
		Name:     "Demo",
		Path:     "/mcp",
	})
	registerEchoTool(s, "echo")

	ts := newTestServerWithAuthInjection(t, s, nil)
	session := connectClient(ctx, t, ts.URL)

	got, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, got.Tools, 1)
	assert.Equal(t, "com_example_demo__echo", got.Tools[0].Name)
}

func TestServeHTTP_InjectsUserID(t *testing.T) {
	ctx := context.Background()

	var capturedUserID string
	var mu sync.Mutex

	s := NewServer(nil, Config{
		PluginID: "com.example.demo",
		Name:     "Demo",
		Path:     "/mcp",
	})
	AddTool[echoIn, echoOut](s, &mcp.Tool{
		Name:        "echo",
		Description: "captures user id",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, echoOut, error) {
		mu.Lock()
		capturedUserID = GetUserID(ctx)
		mu.Unlock()
		return &mcp.CallToolResult{}, echoOut{Echoed: in.Message}, nil
	})

	headers := http.Header{}
	headers.Set("X-Mattermost-UserID", "uxyz")
	ts := newTestServerWithAuthInjection(t, s, headers)
	session := connectClient(ctx, t, ts.URL)

	_, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "com_example_demo__echo",
		Arguments: map[string]any{"message": "hi"},
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "uxyz", capturedUserID)
}

// TestServeHTTP_HandlerLazyInit exercises s.mu under -race.
func TestServeHTTP_HandlerLazyInit(t *testing.T) {
	s := NewServer(nil, Config{PluginID: "x", Name: "X", Path: "/mcp"})

	var wg sync.WaitGroup
	const N = 10
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(""))
			req.Header.Set("Mattermost-Plugin-ID", agentsPluginID)
			s.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	assert.True(t, s.handlerBuiltOK, "handler should have been built")
	assert.NotNil(t, s.handler, "handler should be non-nil after lazy init")
}
