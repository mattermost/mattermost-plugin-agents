// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcphelper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/public/bridgeclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoIn is a sample tool input struct used by AddTool integration tests. The
// `jsonschema:"..."` tag exercises the schema-inference path driven by
// google/jsonschema-go.
type echoIn struct {
	Message string `json:"message" jsonschema:"the message to echo back"`
}

type echoOut struct {
	Echoed string `json:"echoed"`
}

// newTestServerWithAuthInjection wires s.ServeHTTP behind an httptest.Server
// whose handler injects the expected Mattermost-Plugin-ID header. Tests that
// want to verify the security gate REJECTS requests skip the injection and
// call s.ServeHTTP directly with httptest.NewRecorder.
//
// extraHeaders are applied to every incoming request before the server sees
// them (useful for e.g. X-Mattermost-UserID).
func newTestServerWithAuthInjection(t *testing.T, s *Server, extraHeaders http.Header) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Mattermost-Plugin-ID", bridgeclient.AiPluginID)
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

// connectClient spins up an MCP client over the given httptest.Server URL.
func connectClient(ctx context.Context, t *testing.T, endpoint string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "mcphelper-test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = session.Close()
	})
	return session
}

// registerEchoTool adds a simple echo tool to s using the provided name
// (caller controls whether the name already carries the namespace prefix,
// which is how idempotency tests exercise the no-double-prefix branch).
func registerEchoTool(s *Server, toolName string) {
	AddTool[echoIn, echoOut](s, &mcp.Tool{
		Name:        toolName,
		Description: "Echoes the input message back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, echoOut, error) {
		return &mcp.CallToolResult{}, echoOut{Echoed: in.Message}, nil
	})
}

// TestAddTool_PrependsNamespace verifies a vanilla tool-name gets the
// sanitized "<PluginID>__" prefix inserted. PluginIDs containing '.' have the
// dots replaced with '_' so the resulting tool name is compliant with
// Bifrost's regex (^[a-zA-Z0-9_-]{1,128}$).
func TestAddTool_PrependsNamespace(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, PluginMCPServer{
		PluginID: "com.example.demo", // dots get replaced for tool-name prefix
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

// TestAddTool_NoDoublePrefix verifies AddTool is idempotent if the caller
// happens to pass an already-prefixed tool name. The check uses the SANITIZED
// prefix (com_example_demo__), so a caller pre-prefixing with the sanitized
// form gets the name through unchanged.
func TestAddTool_NoDoublePrefix(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, PluginMCPServer{
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

// TestAddTool_SanitizesInvalidPluginID verifies that a plugin ID containing
// runes rejected by the MCP validator is sanitized ONLY on the tool-name
// prefix; every other use of s.config.PluginID keeps the raw value.
func TestAddTool_SanitizesInvalidPluginID(t *testing.T) {
	ctx := context.Background()

	rawPluginID := "com mattermost/@evil"
	s := NewServer(nil, PluginMCPServer{
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

	// The raw PluginID in s.config stays unchanged — other subsystems
	// (network routing, registry keys, wire JSON) depend on the raw value.
	assert.Equal(t, rawPluginID, s.config.PluginID)
}

// TestAddTool_NoDoublePrefix_Sanitized verifies the idempotency check uses
// the SANITIZED prefix, so a caller who pre-prefixes with the sanitized form
// gets the name through unchanged.
func TestAddTool_NoDoublePrefix_Sanitized(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, PluginMCPServer{
		PluginID: "has space", // sanitized prefix = "has_space__"
		Name:     "Test",
		Path:     "/mcp",
	})
	registerEchoTool(s, "echo")
	// Second tool registered with the already-sanitized prefix — must NOT be
	// doubled.
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

// TestAddTool_SchemaGenerated confirms we delegate schema inference to the
// go-sdk's generic AddTool (which calls jsonschema-go under the hood). If we
// had short-circuited the inference path the property description wouldn't
// make it to the wire.
func TestAddTool_SchemaGenerated(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, PluginMCPServer{
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

	// InputSchema unmarshals to map[string]any from the wire.
	schema, ok := got.Tools[0].InputSchema.(map[string]any)
	require.True(t, ok, "InputSchema should be a map[string]any on the wire, got %T", got.Tools[0].InputSchema)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema should have a properties map")
	message, ok := props["message"].(map[string]any)
	require.True(t, ok, "properties should contain 'message'")
	assert.Equal(t, "the message to echo back", message["description"],
		"jsonschema tag should be honored via delegated schema inference")
}

// TestNewServer_DefaultVersion confirms an empty PluginMCPServer.Version
// defaults to "0.0.1" on the MCP initialize roundtrip.
func TestNewServer_DefaultVersion(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, PluginMCPServer{
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

// TestNewServer_ExplicitVersion confirms a non-empty Version is forwarded as-is.
func TestNewServer_ExplicitVersion(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, PluginMCPServer{
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

// TestServeHTTP_MissingPluginIDHeader_403 verifies the security gate rejects
// requests that do not carry Mattermost-Plugin-ID.
func TestServeHTTP_MissingPluginIDHeader_403(t *testing.T) {
	s := NewServer(nil, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	s.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.True(t, strings.HasPrefix(rec.Body.String(), "forbidden"),
		"body should start with 'forbidden'; got %q", rec.Body.String())
}

// TestServeHTTP_WrongPluginIDHeader_403 verifies that any plugin-ID other than
// the agents plugin is rejected.
func TestServeHTTP_WrongPluginIDHeader_403(t *testing.T) {
	s := NewServer(nil, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("Mattermost-Plugin-ID", "com.evil.plugin")
	s.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestServeHTTP_CorrectPluginID_Delegates verifies the happy path: security
// gate passes and the request is served by the streamable MCP handler.
func TestServeHTTP_CorrectPluginID_Delegates(t *testing.T) {
	ctx := context.Background()

	s := NewServer(nil, PluginMCPServer{
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

// TestServeHTTP_InjectsUserID confirms ServeHTTP copies the X-Mattermost-UserID
// header into the request context so tool handlers can see it via GetUserID.
func TestServeHTTP_InjectsUserID(t *testing.T) {
	ctx := context.Background()

	var capturedUserID string
	var mu sync.Mutex

	s := NewServer(nil, PluginMCPServer{
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

// TestServeHTTP_HandlerLazyInit exercises the sync.Mutex guarding lazy
// streamable-handler init. Run under -race to catch data races on
// s.handler/s.handlerBuiltOK.
func TestServeHTTP_HandlerLazyInit(t *testing.T) {
	s := NewServer(nil, PluginMCPServer{PluginID: "x", Name: "X", Path: "/mcp"})

	var wg sync.WaitGroup
	const N = 10
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(""))
			req.Header.Set("Mattermost-Plugin-ID", bridgeclient.AiPluginID)
			s.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()

	// After concurrent first-requests, the lazy init MUST have run exactly once.
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.True(t, s.handlerBuiltOK, "handler should have been built")
	assert.NotNil(t, s.handler, "handler should be non-nil after lazy init")
}
