// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	stdcontext "context"
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrictToolStoreInitialVisibility(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "find Jira issues"),
			testMCPTool("github__search", "https://github.example.com", "search GitHub"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	require.ElementsMatch(t, []string{"builtin", mcp.SearchToolsName, mcp.LoadToolName}, toolNames(context.Tools))
	require.Nil(t, context.Tools.GetTool("jira__get_issue"))
	require.Nil(t, context.Tools.GetTool("github__search"))
}

func TestStrictPreloadsExplicitMCPTools(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
			testMCPTool("mattermost__get_channel_info", mcp.EmbeddedClientKey, "get channel metadata"),
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextPreloadedMCPTools([]llm.EnabledMCPTool{
		{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "read_channel"},
		{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "get_channel_info"},
	}))

	require.ElementsMatch(t, []string{"builtin", mcp.SearchToolsName, mcp.LoadToolName, "read_channel", "get_channel_info"}, toolNames(context.Tools))
	require.Nil(t, context.Tools.GetTool("mattermost__read_channel"))
	require.Nil(t, context.Tools.GetTool("mattermost__get_channel_info"))
	require.Nil(t, context.Tools.GetTool("jira__get_issue"))
	require.Contains(t, searchToolNames(t, context.Tools, "jira"), "jira__get_issue")
	require.False(t, context.Tools.IsUnloadedMCPTool("read_channel"))
	require.False(t, context.Tools.IsUnloadedMCPTool("get_channel_info"))
	require.False(t, context.Tools.IsUnloadedMCPTool("mattermost__read_channel"))
	require.False(t, context.Tools.IsUnloadedMCPTool("mattermost__get_channel_info"))
	require.True(t, context.Tools.IsUnloadedMCPTool("jira__get_issue"))
}

func TestFlagOffAddsPreloadAliases(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
			testMCPTool("mattermost__get_channel_info", mcp.EmbeddedClientKey, "get channel metadata"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: false,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextPreloadedMCPTools([]llm.EnabledMCPTool{
		{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "read_channel"},
		{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "get_channel_info"},
	}))

	require.ElementsMatch(t, []string{"builtin", "mattermost__read_channel", "mattermost__get_channel_info", "read_channel", "get_channel_info"}, toolNames(context.Tools))
	require.NotNil(t, context.Tools.GetTool("mattermost__read_channel"))
	require.NotNil(t, context.Tools.GetTool("mattermost__get_channel_info"))
	require.NotNil(t, context.Tools.GetTool("read_channel"))
	require.NotNil(t, context.Tools.GetTool("get_channel_info"))
	require.Nil(t, context.Tools.GetTool(mcp.SearchToolsName))
	require.Nil(t, context.Tools.GetTool(mcp.LoadToolName))
}

func TestPreloadsNormalizeServerOrigin(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com/", "fetch Jira issue details"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: false,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextPreloadedMCPTools([]llm.EnabledMCPTool{
		{ServerOrigin: " https://jira.example.com ", ToolName: "get_issue"},
	}))

	require.NotNil(t, context.Tools.GetTool("get_issue"))
}

func TestAllowlistNormalizesServerOrigin(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com/", "fetch Jira issue details"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: false,
		MCPDynamicToolLoading: false,
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: " https://jira.example.com ", ToolName: "get_issue"},
		},
	})

	context := buildToolsContext(builder, bot)

	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
}

func TestPreloadsDoNotResurrectFilteredMCPTools(t *testing.T) {
	preloads := []llm.EnabledMCPTool{
		{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "read_channel"},
	}

	tests := []struct {
		name     string
		tools    []llm.Tool
		botCfg   llm.BotConfig
		opts     func(*Builder) []llm.ContextOption
		wantGone string
	}{
		{
			name: "provider omits tool",
			tools: []llm.Tool{
				testMCPTool("mattermost__get_channel_info", mcp.EmbeddedClientKey, "get channel metadata"),
			},
			botCfg: llm.BotConfig{
				ID:                    "bot-id",
				Name:                  "matty",
				DisplayName:           "Matty",
				AutoEnableNewMCPTools: true,
				MCPDynamicToolLoading: true,
			},
			wantGone: "read_channel",
		},
		{
			name: "disabled embedded server",
			tools: []llm.Tool{
				testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
			},
			botCfg: llm.BotConfig{
				ID:                    "bot-id",
				Name:                  "matty",
				DisplayName:           "Matty",
				AutoEnableNewMCPTools: true,
				MCPDynamicToolLoading: true,
			},
			opts: func(builder *Builder) []llm.ContextOption {
				return []llm.ContextOption{builder.WithLLMContextDisabledMCPServers([]string{mcp.EmbeddedClientKey})}
			},
			wantGone: "read_channel",
		},
		{
			name: "predicate filters tool",
			tools: []llm.Tool{
				testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
			},
			botCfg: llm.BotConfig{
				ID:                    "bot-id",
				Name:                  "matty",
				DisplayName:           "Matty",
				AutoEnableNewMCPTools: true,
				MCPDynamicToolLoading: true,
			},
			opts: func(builder *Builder) []llm.ContextOption {
				return []llm.ContextOption{builder.WithLLMContextMCPToolFilter(func(tool llm.Tool) bool {
					return llm.BareMCPToolName(tool.Name) != "read_channel"
				})}
			},
			wantGone: "read_channel",
		},
		{
			name: "bot allowlist excludes tool",
			tools: []llm.Tool{
				testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
			},
			botCfg: llm.BotConfig{
				ID:                    "bot-id",
				Name:                  "matty",
				DisplayName:           "Matty",
				AutoEnableNewMCPTools: false,
				EnabledMCPTools: []llm.EnabledMCPTool{
					{ServerOrigin: mcp.EmbeddedClientKey, ToolName: "get_channel_info"},
				},
				MCPDynamicToolLoading: true,
			},
			wantGone: "read_channel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newTestBuilder(t, &emptyToolProvider{}, &staticMCPToolProvider{tools: tt.tools})
			bot := newTestBotWithConfig(tt.botCfg)
			opts := []llm.ContextOption{builder.WithLLMContextPreloadedMCPTools(preloads)}
			if tt.opts != nil {
				opts = append(opts, tt.opts(builder)...)
			}

			context := buildToolsContext(builder, bot, opts...)

			require.Nil(t, context.Tools.GetTool(tt.wantGone))
		})
	}
}

func TestStrictToolStoreSearchUsesFilteredRegistry(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
			testMCPTool("github__search", "https://github.example.com", "search GitHub code"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	require.Contains(t, searchToolNames(t, context.Tools, "jira"), "jira__get_issue")
	require.Nil(t, context.Tools.GetTool("jira__get_issue"))
}

func TestStrictRegistryUsesAdminRetrievalOverride(t *testing.T) {
	const origin = "https://jira.example.com"
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{
			tools: []llm.Tool{
				testMCPTool("jira__get_issue", origin, "fetch upstream issue details"),
			},
			overrides: map[string]mcp.ToolRetrievalOverride{
				mcp.ToolRetrievalOverrideKey(origin, "get_issue"): {
					Summary: "Find PagerDuty incidents linked to Jira tickets",
				},
			},
		},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)
	result := searchTools(t, context.Tools, "pagerduty")

	require.Len(t, result.Tools, 1)
	require.Equal(t, "jira__get_issue", result.Tools[0].Name)
	require.Equal(t, "Find PagerDuty incidents linked to Jira tickets", result.Tools[0].Summary)
	require.Nil(t, context.Tools.GetTool("jira__get_issue"))
}

func TestStrictToolStoreLoadMaterializesTool(t *testing.T) {
	originalTool := testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details")
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{originalTool}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})
	context := buildToolsContext(builder, bot)

	loadTool := mustTool(t, context.Tools, mcp.LoadToolName)
	resultJSON, err := loadTool.Resolver(stdcontext.Background(), context, contextToolArgs(`{"name":"jira__get_issue"}`))
	require.NoError(t, err)

	var result mcp.LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.True(t, result.Loaded)
	require.Equal(t, "jira__get_issue", result.Name)

	loadedTool := mustTool(t, context.Tools, "jira__get_issue")
	require.Equal(t, originalTool.Schema, loadedTool.Schema)
	require.Equal(t, originalTool.ServerOrigin, loadedTool.ServerOrigin)
	resolved, err := loadedTool.Resolver(stdcontext.Background(), context, contextToolArgs(`{}`))
	require.NoError(t, err)
	require.Equal(t, "mcp:jira__get_issue", resolved)
}

func TestLoadToolUsesOriginalDescriptionWithRetrievalOverride(t *testing.T) {
	const origin = "https://jira.example.com"
	originalTool := testMCPTool("jira__get_issue", origin, "original upstream description")
	originalTool.Schema = map[string]any{"source": "upstream-schema"}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{
			tools: []llm.Tool{originalTool},
			overrides: map[string]mcp.ToolRetrievalOverride{
				mcp.ToolRetrievalOverrideKey(origin, "jira__get_issue"): {
					Summary: "override search-only summary",
				},
			},
		},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})
	context := buildToolsContext(builder, bot)

	searchResult := searchTools(t, context.Tools, "search-only")
	require.Len(t, searchResult.Tools, 1)
	require.Equal(t, "override search-only summary", searchResult.Tools[0].Summary)

	loadTool := mustTool(t, context.Tools, mcp.LoadToolName)
	resultJSON, err := loadTool.Resolver(stdcontext.Background(), context, contextToolArgs(`{"name":"jira__get_issue"}`))
	require.NoError(t, err)

	var result mcp.LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.True(t, result.Loaded)
	require.Equal(t, originalTool.Schema, result.Schema)

	loadedTool := mustTool(t, context.Tools, "jira__get_issue")
	require.Equal(t, "original upstream description", loadedTool.Description)
	require.Equal(t, originalTool.Schema, loadedTool.Schema)
}

func TestFlagOffIgnoresRetrievalOverrides(t *testing.T) {
	const origin = "https://jira.example.com"
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{
			tools: []llm.Tool{
				testMCPTool("jira__get_issue", origin, "original upstream description"),
			},
			overrides: map[string]mcp.ToolRetrievalOverride{
				mcp.ToolRetrievalOverrideKey(origin, "get_issue"): {
					Summary: "override search-only summary",
				},
			},
		},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: false,
	})

	context := buildToolsContext(builder, bot)

	require.ElementsMatch(t, []string{"builtin", "jira__get_issue"}, toolNames(context.Tools))
	require.Nil(t, context.Tools.GetTool(mcp.SearchToolsName))
	require.Equal(t, "original upstream description", mustTool(t, context.Tools, "jira__get_issue").Description)
}

func TestStrictMarksOnlyUnloadedMCPTools(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
			testMCPTool("github__search", "https://github.example.com", "search GitHub code"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)
	context.Tools.LoadMCPTools([]string{"jira__get_issue"})

	require.NotNil(t, context.Tools.GetTool("builtin"))
	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
	require.NotNil(t, context.Tools.GetTool(mcp.SearchToolsName))
	require.NotNil(t, context.Tools.GetTool(mcp.LoadToolName))
	assert.False(t, context.Tools.IsUnloadedMCPTool("builtin"))
	assert.False(t, context.Tools.IsUnloadedMCPTool("jira__get_issue"))
	assert.False(t, context.Tools.IsUnloadedMCPTool(mcp.SearchToolsName))
	assert.False(t, context.Tools.IsUnloadedMCPTool(mcp.LoadToolName))
	assert.True(t, context.Tools.IsUnloadedMCPTool("github__search"))
	info, ok := context.Tools.GetUnloadedMCPToolInfo("github__search")
	require.True(t, ok)
	assert.Equal(t, "search GitHub code", info.Description)
}

func TestFlagOffDoesNotMarkUnloadedMCPTools(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: false,
	})

	context := buildToolsContext(builder, bot)

	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
	assert.False(t, context.Tools.IsUnloadedMCPTool("jira__get_issue"))
}

func TestFlagOffFullSchemaParity(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
			testMCPTool("github__search", "https://github.example.com", "search GitHub code"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: false,
	})

	context := buildToolsContext(builder, bot)

	require.ElementsMatch(t, []string{"builtin", "jira__get_issue", "github__search"}, toolNames(context.Tools))
	require.Nil(t, context.Tools.GetTool(mcp.SearchToolsName))
	require.Nil(t, context.Tools.GetTool(mcp.LoadToolName))
}

func TestContextSetsMCPDynamicToolLoadingCatalogFlag(t *testing.T) {
	builder := newTestBuilder(t, &emptyToolProvider{}, nil)

	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "enabled", enabled: true},
		{name: "disabled", enabled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bot := newTestBotWithConfig(llm.BotConfig{
				ID:                    "bot-id",
				Name:                  "matty",
				DisplayName:           "Matty",
				MCPDynamicToolLoading: tt.enabled,
			})

			context := builder.BuildLLMContextUserRequest(bot, testUser(), testChannel())

			require.Equal(t, tt.enabled, context.ToolCatalog.MCPDynamicToolLoading)
		})
	}
}

func TestFlagOffEmitsTelemetry(t *testing.T) {
	telemetry := &fakeMCPDynamicTelemetry{}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	builder.SetMCPDynamicToolTelemetry(telemetry)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: false,
	})

	context := buildToolsContext(builder, bot)

	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
	require.Equal(t, []contextTelemetryEvent{{botName: "matty", event: "flag_off", result: "disabled"}}, telemetry.events)
}

func TestStrictModeDoesNotEmitFlagOffTelemetry(t *testing.T) {
	telemetry := &fakeMCPDynamicTelemetry{}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	builder.SetMCPDynamicToolTelemetry(telemetry)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	require.NotNil(t, context.Tools.GetTool(mcp.SearchToolsName))
	require.Empty(t, telemetry.events)
}

func TestStrictRegistryAfterBotAllowlist(t *testing.T) {
	jiraOrigin := "https://jira.example.com"
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", jiraOrigin, "fetch Jira issue details"),
			testMCPTool("github__search", "https://github.example.com", "search GitHub code"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: false,
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: jiraOrigin, ToolName: "get_issue"},
		},
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	require.ElementsMatch(t, []string{"builtin", mcp.SearchToolsName, mcp.LoadToolName}, toolNames(context.Tools))
	require.Empty(t, searchToolNames(t, context.Tools, "github"))
	require.Contains(t, searchToolNames(t, context.Tools, "jira"), "jira__get_issue")
}

func TestStrictRegistryAfterDisabledServerOrigins(t *testing.T) {
	githubOrigin := "https://github.example.com"
	disabledOrigin := "  " + githubOrigin + "/  "
	mcpProvider := &staticMCPToolProvider{tools: []llm.Tool{
		testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		testMCPTool("github__search", githubOrigin, "search GitHub code"),
	}}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		mcpProvider,
	)
	strictBot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})
	flagOffBot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: false,
	})

	strictContext := buildToolsContext(builder, strictBot, builder.WithLLMContextDisabledMCPServers([]string{disabledOrigin}))
	require.Empty(t, searchToolNames(t, strictContext.Tools, "github"))
	require.Contains(t, searchToolNames(t, strictContext.Tools, "jira"), "jira__get_issue")

	flagOffContext := buildToolsContext(builder, flagOffBot, builder.WithLLMContextDisabledMCPServers([]string{disabledOrigin}))
	require.ElementsMatch(t, []string{"builtin", "jira__get_issue"}, toolNames(flagOffContext.Tools))
}

func TestStrictRegistryAfterMCPToolPredicate(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__safe_tool", "https://jira.example.com", "safe auto-run Jira tool"),
			testMCPTool("jira__ask_tool", "https://jira.example.com", "dangerous ask-first Jira tool"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextMCPToolFilter(func(tool llm.Tool) bool {
		return tool.Name == "jira__safe_tool"
	}))

	require.Contains(t, searchToolNames(t, context.Tools, "safe"), "jira__safe_tool")
	require.Empty(t, searchToolNames(t, context.Tools, "ask"))

	loadTool := mustTool(t, context.Tools, mcp.LoadToolName)
	resultJSON, err := loadTool.Resolver(stdcontext.Background(), context, contextToolArgs(`{"name":"jira__ask_tool"}`))
	require.NoError(t, err)
	var result mcp.LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.False(t, result.Loaded)
	require.Equal(t, "tool not found", result.Error)
}

func TestStrictModeEmptyMCPProviderStillAddsMetaTools(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		nil,
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	require.ElementsMatch(t, []string{"builtin", mcp.SearchToolsName, mcp.LoadToolName}, toolNames(context.Tools))
	require.Empty(t, searchToolNames(t, context.Tools, "jira"))
}

func TestDisableToolsStillReturnsNoTools(t *testing.T) {
	mcpProvider := &countingMCPToolProvider{}
	builder := newTestBuilder(t, &staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}}, mcpProvider)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		DisableTools:          true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	require.Empty(t, context.Tools.GetTools())
	require.Equal(t, 0, mcpProvider.calls)
}

func TestStrictModePreservesAuthErrors(t *testing.T) {
	origin := "https://mcp.atlassian.com"
	builder := newTestBuilder(t,
		&emptyToolProvider{},
		&staticMCPToolProvider{
			errors: &mcp.Errors{
				ToolAuthErrors: []llm.ToolAuthError{
					{
						ServerName:   "Atlassian",
						ServerOrigin: origin,
						AuthURL:      "https://auth.example.com",
					},
				},
			},
		},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: false,
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: origin, ToolName: llm.MCPServerToolWildcard},
		},
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	require.ElementsMatch(t, []string{mcp.SearchToolsName, mcp.LoadToolName}, toolNames(context.Tools))
	authErrors := context.Tools.GetAuthErrors()
	require.Len(t, authErrors, 1)
	require.Equal(t, origin, authErrors[0].ServerOrigin)
	require.Equal(t, "https://auth.example.com", authErrors[0].AuthURL)
}

func TestLoadMCPToolsAddsDerivedNamesToVisibleStore(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
			testMCPTool("github__search", "https://github.example.com", "search GitHub code"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	require.Nil(t, context.Tools.GetTool("jira__get_issue"))
	require.Nil(t, context.Tools.GetTool("github__search"))

	context.Tools.LoadMCPTools([]string{"jira__get_issue"})

	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
	require.Nil(t, context.Tools.GetTool("github__search"))
	assert.False(t, context.Tools.IsUnloadedMCPTool("jira__get_issue"))
	assert.True(t, context.Tools.IsUnloadedMCPTool("github__search"))
}

func TestLoadMCPToolsSkipsUnknownNames(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	require.NotPanics(t, func() {
		context.Tools.LoadMCPTools([]string{"github__nope", "jira__get_issue"})
	})

	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
	require.Nil(t, context.Tools.GetTool("github__nope"))
	assert.False(t, context.Tools.IsUnloadedMCPTool("github__nope"))
}

func TestLoadMCPToolsSkipsBareNames(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	context.Tools.LoadMCPTools([]string{"get_issue"})

	require.Nil(t, context.Tools.GetTool("jira__get_issue"))
	require.Nil(t, context.Tools.GetTool("get_issue"))
}

func TestLoadMCPToolsSkipsAllowlistFilteredNames(t *testing.T) {
	jiraOrigin := "https://jira.example.com"
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", jiraOrigin, "fetch Jira issue details"),
			testMCPTool("github__search", "https://github.example.com", "search GitHub code"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: false,
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: jiraOrigin, ToolName: "get_issue"},
		},
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)

	context.Tools.LoadMCPTools([]string{"github__search"})

	require.Nil(t, context.Tools.GetTool("github__search"))
	require.Nil(t, context.Tools.GetTool("jira__get_issue"))
}

func TestLoadMCPToolsSkipsUserDisabledOriginNames(t *testing.T) {
	githubOrigin := "https://github.example.com"
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
			testMCPTool("github__search", githubOrigin, "search GitHub code"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextDisabledMCPServers([]string{githubOrigin}))

	context.Tools.LoadMCPTools([]string{"github__search", "jira__get_issue"})

	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
	require.Nil(t, context.Tools.GetTool("github__search"))
}

func TestLoadMCPToolsSkipsPredicateFilteredNames(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__safe_tool", "https://jira.example.com", "safe auto-run Jira tool"),
			testMCPTool("jira__ask_tool", "https://jira.example.com", "dangerous ask-first Jira tool"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextMCPToolFilter(func(tool llm.Tool) bool {
		return tool.Name == "jira__safe_tool"
	}))

	context.Tools.LoadMCPTools([]string{"jira__safe_tool", "jira__ask_tool"})

	require.NotNil(t, context.Tools.GetTool("jira__safe_tool"))
	require.Nil(t, context.Tools.GetTool("jira__ask_tool"))
}

func TestLoadMCPToolsNoopWhenFlagOff(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: false,
	})

	context := buildToolsContext(builder, bot)

	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
	before := toolNames(context.Tools)

	require.NotPanics(t, func() {
		context.Tools.LoadMCPTools([]string{"jira__get_issue"})
	})

	require.ElementsMatch(t, before, toolNames(context.Tools))
	require.Nil(t, context.Tools.GetTool(mcp.SearchToolsName))
	require.Nil(t, context.Tools.GetTool(mcp.LoadToolName))
	assert.False(t, context.Tools.IsUnloadedMCPTool("jira__get_issue"))
}

func TestLoadMCPToolsIgnoresEmptyAndNilNames(t *testing.T) {
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot)
	before := toolNames(context.Tools)

	context.Tools.LoadMCPTools(nil)
	require.ElementsMatch(t, before, toolNames(context.Tools))

	context.Tools.LoadMCPTools([]string{})
	require.ElementsMatch(t, before, toolNames(context.Tools))
}

func TestLoadMCPToolsLeavesRetainedHistoryAloneWhenFiltered(t *testing.T) {
	githubOrigin := "https://github.example.com"
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
			testMCPTool("github__search", githubOrigin, "search GitHub code"),
		}},
	)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextDisabledMCPServers([]string{githubOrigin}))

	require.NotPanics(t, func() {
		context.Tools.LoadMCPTools([]string{"github__search"})
	})

	require.Nil(t, context.Tools.GetTool("github__search"))
}
