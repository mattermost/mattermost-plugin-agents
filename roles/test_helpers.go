// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package roles

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/grounding"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/roles/testutils"
)

// MockMCPClientManager is a shared test mock for llmcontext.MCPToolProvider interface
type MockMCPClientManager struct{}

func (m *MockMCPClientManager) GetToolsForUser(userID string) ([]llm.Tool, *mcp.Errors) {
	return []llm.Tool{}, nil
}

// MockConfigProvider is a shared test mock for llmcontext.ConfigProvider interface
type MockConfigProvider struct{}

func (m *MockConfigProvider) GetEnableLLMTrace() bool {
	return false
}

func (m *MockConfigProvider) GetServiceByID(id string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

// TestLogger provides a unified logging interface for tests that mirrors pluginAPI's logging
// Shared across all bot evaluation tests (PM, Dev, etc.)
type TestLogger struct {
	t           *testing.T
	debugMode   bool
	warnMode    bool
	modelPrefix string
}

// NewTestLogger creates a new test logger
func NewTestLogger(t *testing.T, debugMode, warnMode bool, modelPrefix string) *TestLogger {
	if debugMode {
		warnMode = true
	}

	return &TestLogger{
		t:           t,
		debugMode:   debugMode,
		warnMode:    warnMode,
		modelPrefix: modelPrefix,
	}
}

func (l *TestLogger) Warn(msg string, keyValuePairs ...interface{}) {
	if !l.warnMode {
		return
	}
	l.logWithFields("WARN", msg, keyValuePairs...)
}

func (l *TestLogger) Debug(msg string, keyValuePairs ...interface{}) {
	if !l.debugMode {
		return
	}
	l.logWithFields("DEBUG", msg, keyValuePairs...)
}

func (l *TestLogger) Info(msg string, keyValuePairs ...interface{}) {
	l.logWithFields("INFO", msg, keyValuePairs...)
}

func (l *TestLogger) Error(msg string, keyValuePairs ...interface{}) {
	l.logWithFields("ERROR", msg, keyValuePairs...)
}

func (l *TestLogger) logWithFields(level, msg string, keyValuePairs ...interface{}) {
	var fieldStrs []string
	for i := 0; i < len(keyValuePairs)-1; i += 2 {
		if key, ok := keyValuePairs[i].(string); ok {
			value := keyValuePairs[i+1]
			// Truncate long string values for readability
			if strVal, ok := value.(string); ok && len(strVal) > 100 {
				value = testutils.TruncateWithKeywords(strVal, 100, true)
			}
			fieldStrs = append(fieldStrs, fmt.Sprintf("%s=%v", key, value))
		}
	}

	prefix := ""
	if l.modelPrefix != "" {
		prefix = fmt.Sprintf("[%s] ", l.modelPrefix)
	}

	if len(fieldStrs) > 0 {
		l.t.Logf("%s%s: %s | %s", prefix, level, msg, strings.Join(fieldStrs, ", "))
	} else {
		l.t.Logf("%s%s: %s", prefix, level, msg)
	}
}

// CreateMockLoggerBridge creates a bridge function that forwards pluginAPI.LogDebug calls to TestLogger
func CreateMockLoggerBridge(logger *TestLogger) func(msg string, keyValuePairs ...interface{}) {
	return func(msg string, keyValuePairs ...interface{}) {
		logger.Debug(msg, keyValuePairs...)
	}
}

// CreateAPIClients creates API clients from environment variables for grounding validation
// Returns nil if no credentials are configured
//
// Environment variables:
// - MM_AI_GITHUB_TOKEN: GitHub personal access token for issue verification
// - MM_AI_JIRA_TOKEN: Jira API token (base64 encoded email:token) for ticket verification
// - MM_AI_JIRA_URL: Jira instance URL (e.g., https://mattermost.atlassian.net)
func CreateAPIClients() *grounding.APIClients {
	githubToken := os.Getenv("MM_AI_GITHUB_TOKEN")
	jiraToken := os.Getenv("MM_AI_JIRA_TOKEN")
	jiraURL := os.Getenv("MM_AI_JIRA_URL")

	// If no credentials configured, return nil (no API validation)
	if githubToken == "" && jiraToken == "" {
		return nil
	}

	clients := &grounding.APIClients{}

	// Add GitHub client if token provided
	if githubToken != "" {
		clients.GitHub = evals.NewGitHubAdapter(githubToken)
	}

	// Add Jira client if credentials provided
	if jiraToken != "" && jiraURL != "" {
		// Jira token should be base64-encoded "email:apitoken"
		clients.Jira = evals.NewJiraAdapter(jiraURL, jiraToken)
	}

	// Only return clients if at least one is configured
	if clients.GitHub != nil || clients.Jira != nil || clients.Confluence != nil {
		return clients
	}

	return nil
}

// StreamResult captures both the final response and tool results from processing a stream
type StreamResult struct {
	Response    string
	ToolResults []string
}

// FormatToolResultForGrounding formats a tool result for grounding validation
func FormatToolResultForGrounding(toolName, result string) string {
	return fmt.Sprintf("Tool: %s\nResult: %s", toolName, result)
}
