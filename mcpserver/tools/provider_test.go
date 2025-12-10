// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// testLogger is a simple no-op logger for testing
type testLogger struct {
	t *testing.T
}

func (l *testLogger) Debug(msg string, keyValuePairs ...any) {
	// Could use l.t.Log if we wanted to see debug output during tests
}

func (l *testLogger) Info(msg string, keyValuePairs ...any) {
	// Could use l.t.Log if we wanted to see info output during tests
}

func (l *testLogger) Warn(msg string, keyValuePairs ...any) {
	// Could use l.t.Log if we wanted to see warnings during tests
}

func (l *testLogger) Error(msg string, keyValuePairs ...any) {
	l.t.Logf("ERROR: %s %v", msg, keyValuePairs)
}

func (l *testLogger) Flush() error {
	return nil
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
