// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestTypedWrapperDecodeError verifies the typed wrapper owns argument decoding
// and surfaces the standard "invalid arguments" error for the tool when the
// argument getter fails, before the underlying resolver runs.
func TestTypedWrapperDecodeError(t *testing.T) {
	provider := &MattermostToolProvider{logger: &testLogger{t: t}}

	r := typed("delete_post", provider.toolDeletePost)
	_, err := r(&MCPToolContext{}, func(any) error { return fmt.Errorf("bad json") })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get arguments for tool delete_post")
}

// TestSchemaArgs is a test struct for schema conversion testing
type TestSchemaArgs struct {
	Username string `json:"username" jsonschema:"The username for the test"`
	Count    int    `json:"count" jsonschema:"Number of items to process"`
	Enabled  bool   `json:"enabled" jsonschema:"Whether the feature is enabled"`
}

// TestAccessArgs is a test struct for access validation testing
type TestAccessArgs struct {
	Message         string   `json:"message" jsonschema:"The message content"`
	Attachments     []string `json:"attachments,omitempty" access:"local" jsonschema:"Optional list of file attachments"`
	RemoteOnlyField string   `json:"remote_only_field,omitempty" access:"remote" jsonschema:"Field only available in remote mode"`
}

// TestRegisterDynamicTool_WithSchema tests that tools are properly registered with schemas
func TestRegisterDynamicTool_WithSchema(t *testing.T) {
	// Create a mock server
	mockServer := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	// Create a provider
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	// Create a test tool with schema
	testTool := MCPTool{
		Name:        "test_tool_with_schema",
		Description: "A test tool for schema validation",
		Schema:      llm.NewJSONSchemaFromStruct[TestSchemaArgs](),
		Resolver:    nil, // Not needed for this test
	}

	// Register the tool - should succeed without errors
	provider.registerDynamicTool(mockServer, testTool)

	// Verify the schema was properly assigned (type safety guarantees it's valid)
	require.NotNil(t, testTool.Schema, "Schema should not be nil")
	assert.Equal(t, "object", testTool.Schema.Type, "Schema should be an object type")
	assert.NotNil(t, testTool.Schema.Properties, "Schema should have properties")

	t.Log("Tool with schema registered successfully")
}

// TestRegisterDynamicTool_WithoutSchema tests that tools work without schemas
func TestRegisterDynamicTool_WithoutSchema(t *testing.T) {
	// Create a mock server
	mockServer := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	// Create a provider
	provider := &MattermostToolProvider{
		logger: &testLogger{t: t},
	}

	// Create a test tool without schema
	testTool := MCPTool{
		Name:        "test_tool_no_schema",
		Description: "A test tool without schema",
		Schema:      nil,
		Resolver:    nil, // Not needed for this test
	}

	// Register the tool
	provider.registerDynamicTool(mockServer, testTool)

	// Verify the tool was registered
	t.Log("Tool without schema registered successfully")
}

func TestValidateAccessRestrictions_ValidFields(t *testing.T) {
	testCases := []struct {
		name          string
		jsonData      string
		accessMode    string
		expectError   bool
		errorContains string
	}{
		{
			name:        "local access mode with local-only field should succeed",
			jsonData:    `{"message": "hello", "attachments": ["file1.txt"]}`,
			accessMode:  "local",
			expectError: false,
		},
		{
			name:        "remote access mode with remote-only field should succeed",
			jsonData:    `{"message": "hello", "remote_only_field": "value"}`,
			accessMode:  "remote",
			expectError: false,
		},
		{
			name:        "remote access mode without restricted fields should succeed",
			jsonData:    `{"message": "hello"}`,
			accessMode:  "remote",
			expectError: false,
		},
		{
			name:          "remote access mode with local-only field should fail",
			jsonData:      `{"message": "hello", "attachments": ["file1.txt"]}`,
			accessMode:    "remote",
			expectError:   true,
			errorContains: "field 'attachments' is not available in remote access mode",
		},
		{
			name:          "local access mode with remote-only field should fail",
			jsonData:      `{"message": "hello", "remote_only_field": "value"}`,
			accessMode:    "local",
			expectError:   true,
			errorContains: "field 'remote_only_field' is not available in local access mode",
		},
		{
			name:          "remote access mode with multiple restricted fields should fail on first",
			jsonData:      `{"message": "hello", "attachments": ["file1.txt"], "remote_only_field": "value"}`,
			accessMode:    "remote",
			expectError:   true,
			errorContains: "field 'attachments' is not available in remote access mode",
		},
	}

	var target TestAccessArgs

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAccessRestrictions([]byte(tc.jsonData), &target, tc.accessMode)

			if tc.expectError {
				require.Error(t, err, "Expected validation to fail")
				assert.Contains(t, err.Error(), tc.errorContains, "Error message should contain expected text")
			} else {
				require.NoError(t, err, "Expected validation to succeed")
			}
		})
	}
}

func TestValidateAccessRestrictions_NonStructTarget(t *testing.T) {
	// Test with a non-struct target (should succeed without validation)
	var target string
	jsonData := `"hello world"`

	err := validateAccessRestrictions([]byte(jsonData), &target, "remote")
	require.NoError(t, err, "Non-struct targets should not be validated")
}

func TestValidateAccessRestrictions_SliceTarget(t *testing.T) {
	// Test with a slice target (should succeed without validation)
	var target []string
	jsonData := `["item1", "item2"]`

	err := validateAccessRestrictions([]byte(jsonData), &target, "remote")
	require.NoError(t, err, "Non-struct targets should not be validated")
}

func TestValidateAccessRestrictions_InvalidJSON(t *testing.T) {
	var target TestAccessArgs
	invalidJSON := `{"message": "hello", "attachments"`

	err := validateAccessRestrictions([]byte(invalidJSON), &target, "local")
	require.NoError(t, err, "Invalid JSON that can't be parsed as object should be allowed (not parsed as struct)")
}

func TestValidateAccessRestrictions_AttackScenario(t *testing.T) {
	// This test simulates a realistic attack scenario:
	// Someone creates a remote HTTP request to a post creation tool and tries to
	// include attachments, which should only be available in local access mode

	// Simulate a malicious HTTP request trying to send attachments via remote access
	maliciousRemoteRequest := `{
		"channel_id": "channel123",
		"message": "This is a test post",
		"attachments": ["/etc/passwd"]
	}`

	// Use the actual CreatePostArgs-like structure from our codebase
	type CreatePostArgsSimulated struct {
		ChannelID   string   `json:"channel_id"`
		Message     string   `json:"message"`
		Attachments []string `json:"attachments,omitempty" access:"local"`
	}

	var target CreatePostArgsSimulated

	// Validate that remote access mode rejects local-only attachment fields
	err := validateAccessRestrictions([]byte(maliciousRemoteRequest), &target, "remote")
	require.Error(t, err, "Remote access mode should reject local-only attachments field")
	assert.Contains(t, err.Error(), "field 'attachments' is not available in remote access mode")

	// Validate that local access mode allows the same request
	err = validateAccessRestrictions([]byte(maliciousRemoteRequest), &target, "local")
	require.NoError(t, err, "Local access mode should allow attachments field")

	// Validate that a clean remote request without restricted fields works
	cleanRemoteRequest := `{
		"channel_id": "channel123", 
		"message": "This is a clean test post"
	}`

	err = validateAccessRestrictions([]byte(cleanRemoteRequest), &target, "remote")
	require.NoError(t, err, "Remote access mode should allow requests without restricted fields")
}

// toolNames extracts the names from a slice of *mcp.Tool.
func toolNames(tools []*mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// newToolsListHandler returns a fake "next" MethodHandler that responds to any
// method with a ListToolsResult carrying tools named by the given names.
func newToolsListHandler(names ...string) mcp.MethodHandler {
	return func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		tools := make([]*mcp.Tool, 0, len(names))
		for _, name := range names {
			tools = append(tools, &mcp.Tool{Name: name})
		}
		return &mcp.ListToolsResult{Tools: tools}, nil
	}
}

func alwaysTrue() bool  { return true }
func alwaysFalse() bool { return false }

func newToolsCallHandler(called *bool, text string) mcp.MethodHandler {
	return func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		if called != nil {
			*called = true
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil
	}
}

func callToolRequest(name string) mcp.Request {
	return &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: &mcp.CallToolParamsRaw{Name: name},
	}
}

func TestStateChangingToolsMiddleware(t *testing.T) {
	stateChanging := map[string]struct{}{"create_post": {}}

	t.Run("tools/list omits state-changing tools when not allowed", func(t *testing.T) {
		handler := stateChangingToolsMiddleware(stateChanging, alwaysFalse)(newToolsListHandler("read_post", "create_post", "get_me"))

		result, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)
		listResult, ok := result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.ElementsMatch(t, []string{"read_post", "get_me"}, toolNames(listResult.Tools))
	})

	t.Run("tools/list includes state-changing tools when allowed", func(t *testing.T) {
		handler := stateChangingToolsMiddleware(stateChanging, alwaysTrue)(newToolsListHandler("read_post", "create_post", "get_me"))

		result, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)
		listResult, ok := result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.ElementsMatch(t, []string{"read_post", "create_post", "get_me"}, toolNames(listResult.Tools))
	})

	t.Run("nil predicate fails closed on tools/list", func(t *testing.T) {
		handler := stateChangingToolsMiddleware(stateChanging, nil)(newToolsListHandler("read_post", "create_post"))

		result, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)
		listResult, ok := result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.Equal(t, []string{"read_post"}, toolNames(listResult.Tools))
	})

	t.Run("tools/call of state-changing tool when not allowed returns license error result", func(t *testing.T) {
		called := false
		handler := stateChangingToolsMiddleware(stateChanging, alwaysFalse)(newToolsCallHandler(&called, "resolver ran"))

		result, err := handler(context.Background(), "tools/call", callToolRequest("create_post"))
		require.NoError(t, err, "must be an MCP tool error result, not a transport error")
		assert.False(t, called, "resolver must not run")

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		require.True(t, callResult.IsError)
		require.NotEmpty(t, callResult.Content)
		text, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Contains(t, text.Text, "Enterprise")
		assert.Equal(t, stateChangingToolsUnavailableMessage(), text.Text)
	})

	t.Run("tools/call of read-only tool proceeds when state-changing tools are not allowed", func(t *testing.T) {
		called := false
		handler := stateChangingToolsMiddleware(stateChanging, alwaysFalse)(newToolsCallHandler(&called, "read-only ok"))

		result, err := handler(context.Background(), "tools/call", callToolRequest("read_post"))
		require.NoError(t, err)
		assert.True(t, called)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		require.False(t, callResult.IsError)
		text, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Equal(t, "read-only ok", text.Text)
	})

	t.Run("tools/call of state-changing tool proceeds when allowed", func(t *testing.T) {
		called := false
		handler := stateChangingToolsMiddleware(stateChanging, alwaysTrue)(newToolsCallHandler(&called, "create_post ran"))

		result, err := handler(context.Background(), "tools/call", callToolRequest("create_post"))
		require.NoError(t, err)
		assert.True(t, called)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		text, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Equal(t, "create_post ran", text.Text)
	})

	t.Run("nil predicate fails closed on tools/call", func(t *testing.T) {
		called := false
		handler := stateChangingToolsMiddleware(stateChanging, nil)(newToolsCallHandler(&called, "should not run"))

		result, err := handler(context.Background(), "tools/call", callToolRequest("create_post"))
		require.NoError(t, err)
		assert.False(t, called)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		require.True(t, callResult.IsError)
		text, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Equal(t, stateChangingToolsUnavailableMessage(), text.Text)
	})

	t.Run("allow predicate is evaluated per request", func(t *testing.T) {
		allow := false
		pred := func() bool { return allow }
		handler := stateChangingToolsMiddleware(stateChanging, pred)(newToolsListHandler("read_post", "create_post"))

		result, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)
		listResult, ok := result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.Equal(t, []string{"read_post"}, toolNames(listResult.Tools))

		allow = true
		result, err = handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)
		listResult, ok = result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.ElementsMatch(t, []string{"read_post", "create_post"}, toolNames(listResult.Tools))
	})
}
