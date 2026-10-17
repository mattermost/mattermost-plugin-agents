// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"errors"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

const (
	unconnectedJiraOrigin   = "https://jira.example.com"
	unconnectedGitHubOrigin = "https://github.example.com"
)

func unconnectedTestAuthErrors() []llm.ToolAuthError {
	return []llm.ToolAuthError{
		{
			ServerName:        "Jira",
			ServerOrigin:      unconnectedJiraOrigin,
			AuthURL:           "https://mm.example.com/plugins/mattermost-ai/mcp/oauth/Jira/start",
			ServerDescription: "Jira issues",
			ToolNames:         []string{"create_issue", "search_issues"},
		},
		{
			ServerName:   "GitHub",
			ServerOrigin: unconnectedGitHubOrigin,
			AuthURL:      "https://mm.example.com/plugins/mattermost-ai/mcp/oauth/GitHub/start",
			ToolNames:    []string{"create_pr"},
		},
	}
}

func unconnectedNames(servers []llm.UnconnectedMCPServer) []string {
	names := make([]string, 0, len(servers))
	for _, server := range servers {
		names = append(names, server.Name)
	}
	return names
}

func TestBuilderAdvertisesUnconnectedMCPServers(t *testing.T) {
	autoEnable := llm.BotConfig{ID: "bot-id", Name: "matty", DisplayName: "Matty", AutoEnableNewMCPTools: true}

	tests := []struct {
		name        string
		botConfig   llm.BotConfig
		opts        func(b *Builder, bot *bots.Bot) []llm.ContextOption
		concrete    bool
		wantServers []string
	}{
		{
			name:      "interactive turn advertises every eligible server",
			botConfig: autoEnable,
			opts: func(b *Builder, _ *bots.Bot) []llm.ContextOption {
				return []llm.ContextOption{b.WithLLMContextInteractive()}
			},
			wantServers: []string{"Jira", "GitHub"},
		},
		{
			name:      "non-interactive flow advertises nothing",
			botConfig: autoEnable,
			opts:      func(*Builder, *bots.Bot) []llm.ContextOption { return nil },
		},
		{
			name: "dynamic tool loading still advertises when no server is connected",
			botConfig: llm.BotConfig{
				ID: "bot-id", Name: "matty", DisplayName: "Matty",
				AutoEnableNewMCPTools: true,
				MCPDynamicToolLoading: true,
			},
			opts: func(b *Builder, _ *bots.Bot) []llm.ContextOption {
				return []llm.ContextOption{b.WithLLMContextInteractive()}
			},
			wantServers: []string{"Jira", "GitHub"},
		},
		{
			name: "agent allowlist excludes servers it does not allow",
			botConfig: llm.BotConfig{
				ID: "bot-id", Name: "matty", DisplayName: "Matty",
				EnabledMCPTools: []llm.EnabledMCPTool{{ServerOrigin: unconnectedJiraOrigin, ToolName: llm.MCPServerToolWildcard}},
			},
			opts: func(b *Builder, _ *bots.Bot) []llm.ContextOption {
				return []llm.ContextOption{b.WithLLMContextInteractive()}
			},
			wantServers: []string{"Jira"},
		},
		{
			name:      "user-disabled origins are not advertised",
			botConfig: autoEnable,
			opts: func(b *Builder, _ *bots.Bot) []llm.ContextOption {
				return []llm.ContextOption{
					b.WithLLMContextInteractive(),
					b.WithLLMContextDisabledMCPServers([]string{unconnectedGitHubOrigin}),
				}
			},
			wantServers: []string{"Jira"},
		},
		{
			name:      "channel tool filter keeps only servers with an allowed configured tool",
			botConfig: autoEnable,
			opts: func(b *Builder, _ *bots.Bot) []llm.ContextOption {
				return []llm.ContextOption{
					b.WithLLMContextInteractive(),
					b.WithLLMContextMCPToolFilter(func(tool llm.Tool) bool { return tool.Name == "create_issue" }),
				}
			},
			wantServers: []string{"Jira"},
		},
		{
			name: "service account agents never advertise",
			botConfig: llm.BotConfig{
				ID: "bot-id", Name: "matty", DisplayName: "Matty",
				AutoEnableNewMCPTools: true,
				UseServiceAccountAuth: true,
			},
			opts: func(b *Builder, _ *bots.Bot) []llm.ContextOption {
				return []llm.ContextOption{b.WithLLMContextInteractive()}
			},
		},
		{
			name:      "bridge concrete catalog never advertises",
			botConfig: autoEnable,
			opts: func(b *Builder, _ *bots.Bot) []llm.ContextOption {
				return []llm.ContextOption{b.WithLLMContextInteractive()}
			},
			concrete: true,
		},
		{
			name: "tools disabled for the agent",
			botConfig: llm.BotConfig{
				ID: "bot-id", Name: "matty", DisplayName: "Matty",
				AutoEnableNewMCPTools: true,
				DisableTools:          true,
			},
			opts: func(b *Builder, _ *bots.Bot) []llm.ContextOption {
				return []llm.ContextOption{b.WithLLMContextInteractive()}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &staticMCPToolProvider{
				errors: &mcp.Errors{ToolAuthErrors: unconnectedTestAuthErrors()},
			}
			builder := newTestBuilder(t, &emptyToolProvider{}, provider)
			bot := newTestBotWithConfig(tt.botConfig)

			opts := tt.opts(builder, bot)
			if tt.concrete {
				opts = append(opts, builder.WithLLMContextConcreteTools(stdcontext.Background(), bot))
			} else {
				opts = append(opts, builder.WithLLMContextTools(stdcontext.Background(), bot))
			}
			c := builder.BuildLLMContextUserRequest(bot, testUser(), testChannel(), opts...)

			if len(tt.wantServers) == 0 {
				assert.Empty(t, c.ToolCatalog.UnconnectedMCPServers)
				if c.Tools != nil {
					assert.Nil(t, c.Tools.GetTool(llm.ConnectMCPServerToolName))
				}
				return
			}

			assert.Equal(t, tt.wantServers, unconnectedNames(c.ToolCatalog.UnconnectedMCPServers))
			connect := c.Tools.GetTool(llm.ConnectMCPServerToolName)
			require.NotNil(t, connect)
			assert.Equal(t, llm.UserInteractionConnect, connect.UserInteraction)
			for _, name := range tt.wantServers {
				assert.Contains(t, connectServerParamDescription(t, connect), "- "+name)
			}
			assert.True(t, c.IsToolCallable(llm.ConnectMCPServerToolName))
		})
	}
}

func TestUnconnectedMCPServersFiltering(t *testing.T) {
	allTools := &llm.UnconnectedMCPServerScope{AllMCPTools: true}
	bare := []llm.ToolAuthError{{ServerName: "Bare", ServerOrigin: "https://bare.example.com", AuthURL: "https://mm/bare/start"}}

	tests := []struct {
		name          string
		authErrors    []llm.ToolAuthError
		catalog       llm.ToolCatalogContext
		wantServers   []string
		wantToolNames map[string][]string
	}{
		{
			name:          "auto-enable keeps all servers and admin tool names",
			authErrors:    unconnectedTestAuthErrors(),
			catalog:       llm.ToolCatalogContext{UnconnectedMCPServerScope: allTools},
			wantServers:   []string{"Jira", "GitHub"},
			wantToolNames: map[string][]string{"Jira": {"create_issue", "search_issues"}, "GitHub": {"create_pr"}},
		},
		{
			name:       "context that cannot offer ConnectMCPServer advertises nothing",
			authErrors: unconnectedTestAuthErrors(),
		},
		{
			name:       "allowlist narrows advertised tool names",
			authErrors: unconnectedTestAuthErrors(),
			catalog: llm.ToolCatalogContext{UnconnectedMCPServerScope: &llm.UnconnectedMCPServerScope{EnabledMCPTools: []llm.EnabledMCPTool{
				{ServerOrigin: unconnectedJiraOrigin, ToolName: "search_issues"},
			}}},
			wantServers:   []string{"Jira"},
			wantToolNames: map[string][]string{"Jira": {"search_issues"}},
		},
		{
			name:       "allowlist that excludes every configured tool drops the server",
			authErrors: unconnectedTestAuthErrors(),
			catalog: llm.ToolCatalogContext{UnconnectedMCPServerScope: &llm.UnconnectedMCPServerScope{EnabledMCPTools: []llm.EnabledMCPTool{
				{ServerOrigin: unconnectedJiraOrigin, ToolName: "delete_project"},
			}}},
		},
		{
			name:       "allowlisted server without configured tools is advertised",
			authErrors: bare,
			catalog: llm.ToolCatalogContext{UnconnectedMCPServerScope: &llm.UnconnectedMCPServerScope{EnabledMCPTools: []llm.EnabledMCPTool{
				{ServerOrigin: "https://bare.example.com/", ToolName: llm.MCPServerToolWildcard},
			}}},
			wantServers: []string{"Bare"},
		},
		{
			name:        "disabled origins are normalized before matching",
			authErrors:  unconnectedTestAuthErrors(),
			catalog:     llm.ToolCatalogContext{UnconnectedMCPServerScope: allTools, DisabledMCPServerOrigins: []string{" " + unconnectedJiraOrigin + "/ "}},
			wantServers: []string{"GitHub"},
		},
		{
			name:       "context tool filter that rejects every configured tool drops the server",
			authErrors: unconnectedTestAuthErrors(),
			catalog:    llm.ToolCatalogContext{UnconnectedMCPServerScope: allTools, KeepMCPTool: func(llm.Tool) bool { return false }},
		},
		{
			name:       "context tool filter drops servers without admin tool names",
			authErrors: bare,
			catalog:    llm.ToolCatalogContext{UnconnectedMCPServerScope: allTools, KeepMCPTool: func(llm.Tool) bool { return true }},
		},
		{
			name:        "no context filter keeps servers without admin tool names",
			authErrors:  bare,
			catalog:     llm.ToolCatalogContext{UnconnectedMCPServerScope: allTools},
			wantServers: []string{"Bare"},
		},
		{
			name: "duplicate origins and servers without a start URL are skipped",
			authErrors: append(unconnectedTestAuthErrors()[:1],
				llm.ToolAuthError{ServerName: "Jira again", ServerOrigin: unconnectedJiraOrigin, AuthURL: "https://mm/x"},
				llm.ToolAuthError{ServerName: "NoURL", ServerOrigin: "https://nourl.example.com"},
			),
			catalog:     llm.ToolCatalogContext{UnconnectedMCPServerScope: allTools},
			wantServers: []string{"Jira"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unconnectedMCPServers(tt.authErrors, tt.catalog)
			if len(tt.wantServers) == 0 {
				assert.Empty(t, got)
				return
			}
			require.Equal(t, tt.wantServers, unconnectedNames(got))
			for _, server := range got {
				if want, ok := tt.wantToolNames[server.Name]; ok {
					assert.Equal(t, want, server.ToolNames, server.Name)
				}
			}
		})
	}
}

// An MCP grant that lapses mid-turn is admitted through the same policy as at
// catalog build; when eligible, ConnectMCPServer is offered for it for the
// rest of the turn.
func TestMCPToolErrorResultOffersReconnectMidTurn(t *testing.T) {
	const confluenceOrigin = "https://confluence.example.com"
	expired := &mcp.ReconnectNeededError{Auth: llm.ToolAuthError{
		ServerName:   "Confluence",
		ServerOrigin: confluenceOrigin,
		AuthURL:      "https://mm.example.com/plugins/mattermost-ai/mcp/oauth/Confluence/start",
		Error:        errors.New("401 unauthorized"),
		ToolNames:    []string{"search"},
	}}
	autoEnable := llm.BotConfig{ID: "bot-id", Name: "matty", DisplayName: "Matty", AutoEnableNewMCPTools: true}

	tests := []struct {
		name        string
		err         error
		authErrors  []llm.ToolAuthError
		botConfig   llm.BotConfig
		opts        func(b *Builder) []llm.ContextOption
		wantServers []string
	}{
		{
			name:        "offers the server when nothing was unconnected",
			err:         expired,
			botConfig:   autoEnable,
			opts:        func(b *Builder) []llm.ContextOption { return []llm.ContextOption{b.WithLLMContextInteractive()} },
			wantServers: []string{"Confluence"},
		},
		{
			name:        "extends an existing list",
			err:         expired,
			authErrors:  unconnectedTestAuthErrors()[:1],
			botConfig:   autoEnable,
			opts:        func(b *Builder) []llm.ContextOption { return []llm.ContextOption{b.WithLLMContextInteractive()} },
			wantServers: []string{"Jira", "Confluence"},
		},
		{
			name:      "non-interactive turn never offers it",
			err:       expired,
			botConfig: autoEnable,
			opts:      func(*Builder) []llm.ContextOption { return nil },
		},
		{
			name:      "channel tool filter that rejects every configured tool",
			err:       expired,
			botConfig: autoEnable,
			opts: func(b *Builder) []llm.ContextOption {
				return []llm.ContextOption{
					b.WithLLMContextInteractive(),
					b.WithLLMContextMCPToolFilter(func(llm.Tool) bool { return false }),
				}
			},
		},
		{
			name: "agent allowlist that excludes the server",
			err:  expired,
			botConfig: llm.BotConfig{
				ID: "bot-id", Name: "matty", DisplayName: "Matty",
				EnabledMCPTools: []llm.EnabledMCPTool{{ServerOrigin: unconnectedJiraOrigin, ToolName: llm.MCPServerToolWildcard}},
			},
			opts: func(b *Builder) []llm.ContextOption { return []llm.ContextOption{b.WithLLMContextInteractive()} },
		},
		{
			name:      "user-disabled origin",
			err:       expired,
			botConfig: autoEnable,
			opts: func(b *Builder) []llm.ContextOption {
				return []llm.ContextOption{
					b.WithLLMContextInteractive(),
					b.WithLLMContextDisabledMCPServers([]string{confluenceOrigin}),
				}
			},
		},
		{
			name:      "ordinary tool failure",
			err:       errors.New("tool exploded"),
			botConfig: autoEnable,
			opts:      func(b *Builder) []llm.ContextOption { return []llm.ContextOption{b.WithLLMContextInteractive()} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &staticMCPToolProvider{
				tools: []llm.Tool{testMCPTool("confluence__search", confluenceOrigin, "search pages")},
			}
			if tt.authErrors != nil {
				provider.errors = &mcp.Errors{ToolAuthErrors: tt.authErrors}
			}
			builder := newTestBuilder(t, &emptyToolProvider{}, provider)
			c := buildToolsContext(builder, newTestBotWithConfig(tt.botConfig), tt.opts(builder)...)
			before := unconnectedNames(c.ToolCatalog.UnconnectedMCPServers)

			result := MCPToolErrorResult(c, tt.err)

			if tt.wantServers == nil {
				assert.Equal(t, tt.err.Error(), result)
				assert.Equal(t, before, unconnectedNames(c.ToolCatalog.UnconnectedMCPServers))
				assert.NotContains(t, unconnectedNames(c.ToolCatalog.UnconnectedMCPServers), "Confluence")
				return
			}

			assert.Contains(t, result, "Confluence")
			assert.Contains(t, result, llm.ConnectMCPServerToolName)
			assert.Equal(t, tt.wantServers, unconnectedNames(c.ToolCatalog.UnconnectedMCPServers))
			require.True(t, c.IsToolCallable(llm.ConnectMCPServerToolName))
			connect := c.Tools.GetTool(llm.ConnectMCPServerToolName)
			assert.Contains(t, connectServerParamDescription(t, connect), "- Confluence")
			require.NoError(t, connect.ValidateArguments([]byte(`{"server":"Confluence"}`)))

			// A second failure from the same server does not list it twice.
			assert.Equal(t, result, MCPToolErrorResult(c, tt.err))
			assert.Equal(t, tt.wantServers, unconnectedNames(c.ToolCatalog.UnconnectedMCPServers))
		})
	}
}

// connectServerParamDescription returns the model-only schema text that lists
// the connect tool's servers.
func connectServerParamDescription(t *testing.T, connect *llm.Tool) string {
	t.Helper()
	schema, ok := connect.Schema.(*jsonschema.Schema)
	require.True(t, ok)
	require.NotNil(t, schema.Properties["server"])
	return schema.Properties["server"].Description
}
