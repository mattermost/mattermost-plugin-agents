// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeToolAuthProvider struct{}

func (fakeToolAuthProvider) ValidateAuth(context.Context) error {
	return nil
}

func (fakeToolAuthProvider) GetAuthenticatedMattermostClient(context.Context) (*model.Client4, error) {
	return model.NewAPIv4Client("https://mm.example.com"), nil
}

func TestCreateMCPToolContextReadsBeforeHookResolver(t *testing.T) {
	expectedResolver := auth.BeforeHookResolver(func(_, _, _ string) (string, error) {
		return "/plugins/com.example.plugin/hooks/before", nil
	})
	ctx := context.WithValue(context.Background(), auth.BeforeHookResolverContextKey, expectedResolver)

	provider := &MattermostToolProvider{
		authProvider: fakeToolAuthProvider{},
		mmServerURL:  "https://mm.example.com",
		accessMode:   AccessModeRemote,
	}

	mcpCtx, err := provider.createMCPToolContext(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, mcpCtx.BeforeHookResolver)

	got, err := mcpCtx.BeforeHookResolver("user-1", "search_posts", "beforeHook:secret")
	require.NoError(t, err)
	require.Equal(t, "/plugins/com.example.plugin/hooks/before", got)
}

// TestTypedWrapperDecodeError verifies the typed wrapper owns argument decoding
// and surfaces the standard "invalid arguments" error for the tool when the
// argument getter fails, before the underlying resolver runs.
func TestTypedWrapperDecodeError(t *testing.T) {
	provider := &MattermostToolProvider{logger: &testLogger{t: t}}

	r := typed("delete_automation", provider.toolDeleteAutomation)
	_, err := r(&MCPToolContext{}, func(any) error { return fmt.Errorf("bad json") })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get arguments for tool delete_automation")
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

func TestToolAvailabilityMiddleware(t *testing.T) {
	t.Run("drops unavailable tool, keeps the rest", func(t *testing.T) {
		availability := map[string]func() bool{"b": alwaysFalse}
		handler := toolAvailabilityMiddleware(availability)(newToolsListHandler("a", "b", "c"))

		// req is unread by the middleware; mcp.Request is an interface, so nil compiles.
		result, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)

		listResult, ok := result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.ElementsMatch(t, []string{"a", "c"}, toolNames(listResult.Tools))
	})

	t.Run("keeps available tool", func(t *testing.T) {
		availability := map[string]func() bool{"a": alwaysTrue}
		handler := toolAvailabilityMiddleware(availability)(newToolsListHandler("a"))

		result, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)

		listResult, ok := result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.Equal(t, []string{"a"}, toolNames(listResult.Tools))
	})

	t.Run("tools not in the availability map are always kept", func(t *testing.T) {
		availability := map[string]func() bool{"b": alwaysFalse}
		handler := toolAvailabilityMiddleware(availability)(newToolsListHandler("a", "c"))

		result, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)

		listResult, ok := result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.ElementsMatch(t, []string{"a", "c"}, toolNames(listResult.Tools))
	})

	t.Run("non-tools/list method is passed through untouched", func(t *testing.T) {
		availability := map[string]func() bool{"b": alwaysFalse}
		handler := toolAvailabilityMiddleware(availability)(newToolsListHandler("a", "b", "c"))

		// A different method: the middleware must NOT filter "b" out.
		result, err := handler(context.Background(), "tools/call", nil)
		require.NoError(t, err)

		listResult, ok := result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.ElementsMatch(t, []string{"a", "b", "c"}, toolNames(listResult.Tools))
	})

	t.Run("shared predicate is evaluated once per call (memoization)", func(t *testing.T) {
		calls := 0
		pred := func() bool {
			calls++
			return true
		}
		availability := map[string]func() bool{"x": pred, "y": pred}
		handler := toolAvailabilityMiddleware(availability)(newToolsListHandler("x", "y"))

		_, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)

		assert.Equal(t, 1, calls, "the shared predicate must be probed once, not once per tool")
	})
}
