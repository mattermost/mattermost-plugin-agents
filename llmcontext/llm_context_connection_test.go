// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// describingMCPToolProvider adds the MCP server description surface the real
// client manager offers on top of a fixed catalog.
type describingMCPToolProvider struct {
	staticMCPToolProvider
	described []llm.UnconnectedMCPServer
	origins   map[string]string
}

func (p *describingMCPToolProvider) DescribeUnconnectedMCPServers(authErrors []llm.ToolAuthError) []llm.UnconnectedMCPServer {
	var out []llm.UnconnectedMCPServer
	for _, server := range p.described {
		for _, authErr := range authErrors {
			if authErr.ServerName == server.Name {
				server.Tools = append([]llm.ToolInfo(nil), server.Tools...)
				out = append(out, server)
				break
			}
		}
	}
	return out
}

func (p *describingMCPToolProvider) MCPServerOriginsByName() map[string]string {
	return p.origins
}

func TestGetToolsStoreOffersMCPConnections(t *testing.T) {
	const jiraOrigin = "https://jira.example.com/mcp"
	jira := llm.UnconnectedMCPServer{
		Name:         "Jira",
		ServerOrigin: jiraOrigin,
		Description:  "Issues and sprints",
		Tools: []llm.ToolInfo{
			{Name: "create_issue", ServerOrigin: jiraOrigin},
			{Name: "search", ServerOrigin: jiraOrigin},
		},
	}
	jiraAuthError := []llm.ToolAuthError{{ServerName: "Jira", ServerOrigin: jiraOrigin, AuthURL: "https://mm.example.com/start"}}
	origins := map[string]string{"Jira": jiraOrigin}

	autoEnable := llm.BotConfig{ID: "bot-id", Name: "matty", DisplayName: "Matty", AutoEnableNewMCPTools: true}

	tests := []struct {
		name        string
		botConfig   llm.BotConfig
		interactive bool
		authErrors  []llm.ToolAuthError
		wantOffered bool
		wantTools   []string
		wantOrigins bool
	}{
		{
			name:        "interactive user with an unconnected server is offered the connection",
			botConfig:   autoEnable,
			interactive: true,
			authErrors:  jiraAuthError,
			wantOffered: true,
			wantTools:   []string{"create_issue", "search"},
			wantOrigins: true,
		},
		{
			name:        "dynamic loading with no connected tools still offers the connection",
			botConfig:   llm.BotConfig{ID: "bot-id", Name: "matty", AutoEnableNewMCPTools: true, MCPDynamicToolLoading: true},
			interactive: true,
			authErrors:  jiraAuthError,
			wantOffered: true,
			wantTools:   []string{"create_issue", "search"},
			wantOrigins: true,
		},
		{
			name:        "unattended request is never offered a connection",
			botConfig:   autoEnable,
			interactive: false,
			authErrors:  jiraAuthError,
		},
		{
			name:        "fully connected user keeps server origins for resolving answered requests",
			botConfig:   autoEnable,
			interactive: true,
			wantOrigins: true,
		},
		{
			name: "agent allowlist narrows the known tools it describes",
			botConfig: llm.BotConfig{
				ID:              "bot-id",
				Name:            "matty",
				EnabledMCPTools: []llm.EnabledMCPTool{{ServerOrigin: jiraOrigin + "/", ToolName: "jira__search"}},
			},
			interactive: true,
			authErrors:  jiraAuthError,
			wantOffered: true,
			wantTools:   []string{"search"},
			wantOrigins: true,
		},
		{
			name: "agent wildcard allowlist keeps every known tool",
			botConfig: llm.BotConfig{
				ID:              "bot-id",
				Name:            "matty",
				EnabledMCPTools: []llm.EnabledMCPTool{{ServerOrigin: jiraOrigin, ToolName: llm.MCPServerToolWildcard}},
			},
			interactive: true,
			authErrors:  jiraAuthError,
			wantOffered: true,
			wantTools:   []string{"create_issue", "search"},
			wantOrigins: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &describingMCPToolProvider{
				errors:    &mcp.Errors{ToolAuthErrors: tt.authErrors},
				described: []llm.UnconnectedMCPServer{jira},
				origins:   origins,
			}
			builder := newTestBuilder(t, &emptyToolProvider{}, provider)
			bot := newTestBotWithConfig(tt.botConfig)

			var opts []llm.ContextOption
			if tt.interactive {
				opts = append(opts, builder.WithLLMContextInteractive())
			}
			context := buildToolsContext(builder, bot, opts...)

			tool := context.Tools.GetTool(mcp.RequestConnectionToolName)
			if !tt.wantOffered {
				assert.Nil(t, tool)
				assert.Empty(t, context.UnconnectedMCPServers)
			} else {
				require.NotNil(t, tool)
				assert.Equal(t, llm.UserInteractionConnect, tool.UserInteraction)
				require.NoError(t, tool.ValidateArguments([]byte(`{"server":"Jira","reason":"Find issues"}`)))

				require.Len(t, context.UnconnectedMCPServers, 1)
				described := context.UnconnectedMCPServers[0]
				assert.Equal(t, "Jira", described.Name)
				assert.Equal(t, "Issues and sprints", described.Description)
				var toolNames []string
				for _, info := range described.Tools {
					toolNames = append(toolNames, info.Name)
				}
				assert.Equal(t, tt.wantTools, toolNames)
			}

			if tt.wantOrigins {
				assert.Equal(t, origins, context.ToolCatalog.MCPServerOrigins)
			} else {
				assert.Nil(t, context.ToolCatalog.MCPServerOrigins, "connection requests cannot be offered here")
			}
		})
	}
}

func TestGetToolsStoreServiceAccountNeverOffersMCPConnections(t *testing.T) {
	const jiraOrigin = "https://jira.example.com/mcp"
	provider := &describingMCPToolProvider{
		errors: &mcp.Errors{ToolAuthErrors: []llm.ToolAuthError{
			{ServerName: "Jira", ServerOrigin: jiraOrigin},
		}},
		described: []llm.UnconnectedMCPServer{{Name: "Jira", ServerOrigin: jiraOrigin}},
		origins:   map[string]string{"Jira": jiraOrigin},
	}
	builder := newLicenseTestBuilder(t, true, &emptyToolProvider{}, provider)
	bot := newTestBotWithMMBot(
		llm.BotConfig{ID: "bot-id", Name: "matty", AutoEnableNewMCPTools: true, UseServiceAccountAuth: true},
		&model.Bot{UserId: "bot-user-id", Username: "matty"},
	)

	context := builder.BuildLLMContextUserRequest(bot, testUser(), testChannel(),
		builder.WithLLMContextInteractive(),
		builder.WithLLMContextTools(stdcontext.Background(), bot),
	)

	assert.Nil(t, context.Tools.GetTool(mcp.RequestConnectionToolName))
	assert.Empty(t, context.UnconnectedMCPServers)
}
