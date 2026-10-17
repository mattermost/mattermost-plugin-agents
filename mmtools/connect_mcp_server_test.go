// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

func testUnconnectedServers() []llm.UnconnectedMCPServer {
	return []llm.UnconnectedMCPServer{
		{Name: "Jira Cloud", Origin: "jira", Description: "Issues", AuthURL: "https://example.com/jira/start", ToolNames: []string{"create_issue"}},
		{Name: "Notes", Origin: "notes", AuthURL: "https://example.com/notes/start"},
	}
}

func TestConnectMCPServerToolValidation(t *testing.T) {
	tool := NewConnectMCPServerTool(testUnconnectedServers())

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "listed server", input: `{"server":"Jira Cloud"}`},
		{name: "second listed server", input: `{"server":"Notes"}`},
		{name: "unlisted server", input: `{"server":"GitHub"}`, wantErr: true},
		{name: "name must match exactly", input: `{"server":"jira cloud"}`, wantErr: true},
		{name: "empty server", input: `{"server":" "}`, wantErr: true},
		{name: "missing server", input: `{}`, wantErr: true},
		{name: "malformed", input: `{`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.ValidateArguments(json.RawMessage(tt.input))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestConnectMCPServerToolShape(t *testing.T) {
	tool := NewConnectMCPServerTool(testUnconnectedServers())

	assert.False(t, tool.AutoExecute)

	schema, ok := tool.Schema.(*jsonschema.Schema)
	require.True(t, ok)
	server := schema.Properties["server"]
	require.NotNil(t, server)
	assert.Equal(t, []any{"Jira Cloud", "Notes"}, server.Enum)

	// Tool.Description is copied onto persisted tool calls that other channel
	// members can see, so only the model-facing schema may list servers.
	assert.Equal(t, NewConnectMCPServerTool(nil).Description, tool.Description)
	for _, s := range testUnconnectedServers() {
		assert.Contains(t, server.Description, s.PromptLine())
		assert.NotContains(t, tool.Description, s.Name)
		if s.Description != "" {
			assert.NotContains(t, tool.Description, s.Description)
		}
		for _, toolName := range s.ToolNames {
			assert.NotContains(t, tool.Description, toolName)
		}
	}

	_, err := tool.Resolver(context.Background(), &llm.Context{}, nil)
	require.Error(t, err)
}

func TestConnectMCPServerToolCallDescriptionNamesNoServer(t *testing.T) {
	servers := testUnconnectedServers()
	store := llm.NewToolStore()
	store.AddTools([]llm.Tool{NewConnectMCPServerTool(servers)})

	call := llm.ToolCall{ID: "c1", Name: llm.ConnectMCPServerToolName, Arguments: json.RawMessage(`{"server":"Jira Cloud"}`)}
	llm.EnrichToolCall(&call, store, llm.EnrichToolCallOptions{})

	require.NotEmpty(t, call.Description)
	assert.Equal(t, llm.UserInteractionConnect, call.UserInteraction)
	for _, s := range servers {
		assert.NotContains(t, call.Description, s.Name)
		if s.Description != "" {
			assert.NotContains(t, call.Description, s.Description)
		}
		for _, toolName := range s.ToolNames {
			assert.NotContains(t, call.Description, toolName)
		}
	}
}

func TestResolveConnectMCPServerAnswer(t *testing.T) {
	const jiraOrigin = "https://jira.example.com"
	jiraInput := json.RawMessage(`{"server":"Jira Cloud"}`)
	resolver := func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }
	storeWith := func(tools []llm.Tool, unloaded []llm.Tool) *llm.ToolStore {
		store := llm.NewToolStore()
		store.AddTools(tools)
		store.SetUnloadedMCPTools(unloaded)
		return store
	}
	jiraTool := llm.Tool{Name: "jira__create_issue", ServerOrigin: jiraOrigin, Resolver: resolver}
	otherTool := llm.Tool{Name: "github__create_pr", ServerOrigin: "https://github.example.com", Resolver: resolver}
	searchTools := llm.Tool{Name: mcp.SearchToolsName, Resolver: resolver}

	tests := []struct {
		name          string
		input         json.RawMessage
		env           UserInteractionEnv
		wantIsError   bool
		wantSearchUse bool
		wantErr       bool
	}{
		{
			name:  "connected when the server's tools are visible",
			input: jiraInput,
			env: UserInteractionEnv{
				Context:             &llm.Context{Tools: storeWith([]llm.Tool{jiraTool}, nil)},
				ConnectServerOrigin: jiraOrigin + "/",
			},
		},
		{
			name:  "connected in dynamic mode when the server's tools are in the unloaded catalog",
			input: jiraInput,
			env: UserInteractionEnv{
				Context: &llm.Context{
					Tools:       storeWith([]llm.Tool{searchTools}, []llm.Tool{jiraTool}),
					ToolCatalog: llm.ToolCatalogContext{MCPDynamicToolLoading: true},
				},
				ConnectServerOrigin: jiraOrigin,
			},
			wantSearchUse: true,
		},
		{
			name:  "still not connected when the rebuilt context lists it",
			input: jiraInput,
			env: UserInteractionEnv{
				Context: &llm.Context{
					Tools:       storeWith([]llm.Tool{jiraTool}, nil),
					ToolCatalog: llm.ToolCatalogContext{UnconnectedMCPServers: testUnconnectedServers()},
				},
				ConnectServerOrigin: jiraOrigin,
			},
			wantIsError: true,
		},
		{
			name:  "not listed but no tools from its origin is unavailable",
			input: jiraInput,
			env: UserInteractionEnv{
				Context:             &llm.Context{Tools: storeWith([]llm.Tool{otherTool}, nil)},
				ConnectServerOrigin: jiraOrigin,
			},
			wantIsError: true,
		},
		{
			name:        "server without an enabled configured origin is unavailable",
			input:       jiraInput,
			env:         UserInteractionEnv{Context: &llm.Context{Tools: storeWith([]llm.Tool{jiraTool}, nil)}},
			wantIsError: true,
		},
		{
			name:        "no tool store is unavailable",
			input:       jiraInput,
			env:         UserInteractionEnv{Context: &llm.Context{}, ConnectServerOrigin: jiraOrigin},
			wantIsError: true,
		},
		{name: "requires rebuilt context", input: jiraInput, env: UserInteractionEnv{ConnectServerOrigin: jiraOrigin}, wantErr: true},
		{name: "invalid arguments", input: json.RawMessage(`{}`), env: UserInteractionEnv{Context: &llm.Context{}, ConnectServerOrigin: jiraOrigin}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveUserInteractionAnswer(llm.UserInteractionConnect, tt.input, UserInteractionAnswer{}, tt.env)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantIsError, got.IsError)
			assert.Contains(t, got.Content, "Jira Cloud")
			assert.Equal(t, tt.wantSearchUse, strings.Contains(got.Content, mcp.SearchToolsName))
		})
	}
}

func TestUserInteractionSkippedResult(t *testing.T) {
	tests := []struct {
		name  string
		kind  string
		input json.RawMessage
		want  string
	}{
		{name: "connect", kind: llm.UserInteractionConnect, input: json.RawMessage(`{"server":"Jira Cloud"}`), want: "User declined to connect Jira Cloud"},
		{name: "connect with bad arguments", kind: llm.UserInteractionConnect, input: json.RawMessage(`{`), want: "User declined to connect the MCP server"},
		{name: "question", kind: llm.UserInteractionSelect, input: json.RawMessage(`{}`), want: "User skipped the question"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, UserInteractionSkippedResult(tt.kind, tt.input))
		})
	}
}
