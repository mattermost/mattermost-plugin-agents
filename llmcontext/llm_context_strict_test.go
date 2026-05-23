// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	storepkg "github.com/mattermost/mattermost-plugin-agents/store"
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
	loadedStore := &fakeLoadedMCPToolStore{}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
			testMCPTool("mattermost__get_channel_info", mcp.EmbeddedClientKey, "get channel metadata"),
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
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
	require.Empty(t, loadedStore.upserts)
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
	resultJSON, err := loadTool.Resolver(context, contextToolArgs(`{"name":"jira__get_issue"}`))
	require.NoError(t, err)

	var result mcp.LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.True(t, result.Loaded)
	require.Equal(t, "jira__get_issue", result.Name)

	loadedTool := mustTool(t, context.Tools, "jira__get_issue")
	require.Equal(t, originalTool.Schema, loadedTool.Schema)
	require.Equal(t, originalTool.ServerOrigin, loadedTool.ServerOrigin)
	resolved, err := loadedTool.Resolver(context, contextToolArgs(`{}`))
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
	resultJSON, err := loadTool.Resolver(context, contextToolArgs(`{"name":"jira__get_issue"}`))
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

func TestStrictRestoresLoadedTools(t *testing.T) {
	loadedStore := &fakeLoadedMCPToolStore{
		rows: []storepkg.LoadedMCPTool{loadedMCPToolRow("jira__get_issue")},
	}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
			testMCPTool("github__search", "https://github.example.com", "search GitHub code"),
		}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextConversationID("conv-id"))

	require.ElementsMatch(t, []string{"builtin", "jira__get_issue", mcp.SearchToolsName, mcp.LoadToolName}, toolNames(context.Tools))
	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
	require.Nil(t, context.Tools.GetTool("github__search"))
	require.Equal(t, 1, loadedStore.listCalls)
}

func TestStrictDoesNotRestoreWithoutConversationID(t *testing.T) {
	loadedStore := &fakeLoadedMCPToolStore{
		rows: []storepkg.LoadedMCPTool{loadedMCPToolRow("jira__get_issue")},
	}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
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
	require.Zero(t, loadedStore.listCalls)
}

func TestLoadToolPersistsLoadedName(t *testing.T) {
	loadedStore := &fakeLoadedMCPToolStore{}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})
	context := buildToolsContext(builder, bot, builder.WithLLMContextConversationID("conv-id"))

	loadTool := mustTool(t, context.Tools, mcp.LoadToolName)
	resultJSON, err := loadTool.Resolver(context, contextToolArgs(`{"name":"jira__get_issue"}`))
	require.NoError(t, err)

	var result mcp.LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.True(t, result.Loaded)
	require.Len(t, loadedStore.upserts, 1)
	assert.Equal(t, "conv-id", loadedStore.upserts[0].ConversationID)
	assert.Equal(t, "bot-id", loadedStore.upserts[0].BotID)
	assert.Equal(t, "user-id", loadedStore.upserts[0].UserID)
	assert.Equal(t, "jira__get_issue", loadedStore.upserts[0].ToolName)
	assert.Equal(t, "https://jira.example.com", loadedStore.upserts[0].ServerOrigin)
	assert.Equal(t, "get_issue", loadedStore.upserts[0].BareName)
	assert.NotZero(t, loadedStore.upserts[0].CreatedAt)
	assert.Equal(t, loadedStore.upserts[0].CreatedAt, loadedStore.upserts[0].UpdatedAt)
}

func TestStrictDropsAndDeletesRevokedLoadedTool(t *testing.T) {
	loadedStore := &fakeLoadedMCPToolStore{
		rows: []storepkg.LoadedMCPTool{loadedMCPToolRow("github__search")},
	}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextConversationID("conv-id"))

	require.Nil(t, context.Tools.GetTool("github__search"))
	require.Equal(t, []string{"conv-id\x00bot-id\x00user-id\x00github__search"}, loadedStore.deletes)
}

func TestStrictDoesNotPersistRawSchema(t *testing.T) {
	loadedType := reflect.TypeOf(storepkg.LoadedMCPTool{})
	_, hasSchema := loadedType.FieldByName("Schema")
	require.False(t, hasSchema)

	loadedStore := &fakeLoadedMCPToolStore{}
	tool := testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details")
	tool.Schema = map[string]any{"source": "current-registry"}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{tool}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})
	context := buildToolsContext(builder, bot, builder.WithLLMContextConversationID("conv-id"))

	loadTool := mustTool(t, context.Tools, mcp.LoadToolName)
	_, err := loadTool.Resolver(context, contextToolArgs(`{"name":"jira__get_issue"}`))
	require.NoError(t, err)

	require.Len(t, loadedStore.upserts, 1)
	require.Equal(t, "jira__get_issue", loadedStore.upserts[0].ToolName)
}

func TestStrictRestoresAfterBotAllowlist(t *testing.T) {
	jiraOrigin := "https://jira.example.com"
	githubOrigin := "https://github.example.com"
	loadedStore := &fakeLoadedMCPToolStore{
		rows: []storepkg.LoadedMCPTool{
			loadedMCPToolRow("github__search"),
			loadedMCPToolRow("jira__get_issue"),
		},
	}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", jiraOrigin, "fetch Jira issue details"),
			testMCPTool("github__search", githubOrigin, "search GitHub code"),
		}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
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

	context := buildToolsContext(builder, bot, builder.WithLLMContextConversationID("conv-id"))

	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
	require.Nil(t, context.Tools.GetTool("github__search"))
	require.Equal(t, []string{"conv-id\x00bot-id\x00user-id\x00github__search"}, loadedStore.deletes)
}

func TestStrictRestoresAfterUserDisabledFilter(t *testing.T) {
	githubOrigin := "https://github.example.com"
	loadedStore := &fakeLoadedMCPToolStore{
		rows: []storepkg.LoadedMCPTool{
			loadedMCPToolRow("github__search"),
			loadedMCPToolRow("jira__get_issue"),
		},
	}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
			testMCPTool("github__search", githubOrigin, "search GitHub code"),
		}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(
		builder,
		bot,
		builder.WithLLMContextConversationID("conv-id"),
		builder.WithLLMContextDisabledMCPServers([]string{"  " + githubOrigin + "/  "}),
	)

	require.NotNil(t, context.Tools.GetTool("jira__get_issue"))
	require.Nil(t, context.Tools.GetTool("github__search"))
	require.Equal(t, []string{"conv-id\x00bot-id\x00user-id\x00github__search"}, loadedStore.deletes)
}

func TestStrictOmitsButKeepsScopedFilteredLoadedTool(t *testing.T) {
	loadedStore := &fakeLoadedMCPToolStore{
		rows: []storepkg.LoadedMCPTool{loadedMCPToolRow("github__search")},
	}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("github__search", "https://github.example.com", "search GitHub code"),
		}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(
		builder,
		bot,
		builder.WithLLMContextConversationID("conv-id"),
		builder.WithLLMContextMCPToolFilter(func(tool llm.Tool) bool {
			return tool.Name != "github__search"
		}),
	)

	require.Nil(t, context.Tools.GetTool("github__search"))
	require.Empty(t, loadedStore.deletes)
}

func TestFlagOffIgnoresLoadedState(t *testing.T) {
	loadedStore := &fakeLoadedMCPToolStore{
		rows: []storepkg.LoadedMCPTool{loadedMCPToolRow("jira__get_issue")},
	}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
		}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: false,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextConversationID("conv-id"))

	require.ElementsMatch(t, []string{"builtin", "jira__get_issue"}, toolNames(context.Tools))
	require.Nil(t, context.Tools.GetTool(mcp.SearchToolsName))
	require.Zero(t, loadedStore.listCalls)
}

func TestStrictMarksOnlyUnloadedMCPTools(t *testing.T) {
	loadedStore := &fakeLoadedMCPToolStore{
		rows: []storepkg.LoadedMCPTool{loadedMCPToolRow("jira__get_issue")},
	}
	builder := newTestBuilder(t,
		&staticToolProvider{tools: []llm.Tool{testBuiltinTool("builtin")}},
		&staticMCPToolProvider{tools: []llm.Tool{
			testMCPTool("jira__get_issue", "https://jira.example.com", "fetch Jira issue details"),
			testMCPTool("github__search", "https://github.example.com", "search GitHub code"),
		}},
	)
	builder.SetLoadedMCPToolStore(loadedStore)
	bot := newTestBotWithConfig(llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	})

	context := buildToolsContext(builder, bot, builder.WithLLMContextConversationID("conv-id"))

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

func TestContextSetsMCPDynamicToolLoadingFlag(t *testing.T) {
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

			require.Equal(t, tt.enabled, context.MCPDynamicToolLoading)
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
	resultJSON, err := loadTool.Resolver(context, contextToolArgs(`{"name":"jira__ask_tool"}`))
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
