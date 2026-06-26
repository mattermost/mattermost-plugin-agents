// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/require"
)

type mcpTelemetryEvent struct {
	botName string
	event   string
	result  string
}

type fakeMCPDynamicTelemetry struct {
	events []mcpTelemetryEvent
}

func (t *fakeMCPDynamicTelemetry) ObserveMCPDynamicToolEvent(botName, event, result string) {
	t.events = append(t.events, mcpTelemetryEvent{botName: botName, event: event, result: result})
}

func TestNewMetaToolsDefinitions(t *testing.T) {
	tools := NewMetaTools(NewToolRegistry(nil))

	require.Len(t, tools, 2)
	require.Equal(t, []string{SearchToolsName, LoadToolName}, []string{tools[0].Name, tools[1].Name})
	require.Equal(t, "Search available MCP tools by keyword.", tools[0].Description)
	require.Equal(t, "Load an MCP tool by exact namespaced name.", tools[1].Description)
	for _, tool := range tools {
		require.NotEmpty(t, tool.Description)
		require.NotNil(t, tool.Schema)
		require.NotNil(t, tool.Resolver)
		require.Empty(t, tool.ServerOrigin)
	}

	require.True(t, IsMCPMetaTool(SearchToolsName))
	require.True(t, IsMCPMetaTool(LoadToolName))
	require.False(t, IsMCPMetaTool("jira__search"))
	require.False(t, IsMCPMetaTool(""))
}

func TestSearchToolsReturnsTopEightNameSummary(t *testing.T) {
	tools := make([]llm.Tool, 0, 10)
	for i := 9; i >= 0; i-- {
		tools = append(tools, testRegistryTool(fmt.Sprintf("server__tool_%02d", i), "shared capability", "https://server.example.com"))
	}
	searchTool := metaToolByName(t, NewMetaTools(NewToolRegistry(tools)), SearchToolsName)

	resultJSON, err := searchTool.Resolver(context.Background(), &llm.Context{}, metaToolArgs(`{"query":"shared"}`))
	require.NoError(t, err)

	var result SearchToolsResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.Len(t, result.Tools, DefaultMCPToolSearchLimit)
	for _, item := range result.Tools {
		require.NotEmpty(t, item.Name)
		require.Equal(t, "shared capability", item.Summary)
	}

	var raw map[string][]map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &raw))
	require.NotContains(t, raw["tools"][0], "score")
}

func TestSearchToolsEmptyQueryReturnsEmptyList(t *testing.T) {
	searchTool := metaToolByName(t, NewMetaTools(NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__get_issue", "Get issue", "https://jira.example.com"),
	})), SearchToolsName)

	for _, rawArgs := range []string{`{"query":""}`, `{"query":"   "}`} {
		resultJSON, err := searchTool.Resolver(context.Background(), &llm.Context{}, metaToolArgs(rawArgs))
		require.NoError(t, err)

		var result SearchToolsResult
		require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
		require.Empty(t, result.Tools)
		require.JSONEq(t, `{"tools":[]}`, resultJSON)
	}
}

func TestSearchToolsNilContextStillReturnsResults(t *testing.T) {
	searchTool := metaToolByName(t, NewMetaTools(NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__get_issue", "Get issue", "https://jira.example.com"),
	})), SearchToolsName)

	resultJSON, err := searchTool.Resolver(context.Background(), nil, metaToolArgs(`{"query":"issue"}`))
	require.NoError(t, err)

	var result SearchToolsResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.Len(t, result.Tools, 1)
	require.Equal(t, "jira__get_issue", result.Tools[0].Name)
}

func TestSearchToolsUsesFilteredRegistryOnly(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__get_issue", "Get a Jira issue", "https://jira.example.com"),
	})
	searchTool := metaToolByName(t, NewMetaTools(registry), SearchToolsName)
	llmContext := &llm.Context{Tools: llm.NewToolStore()}
	llmContext.Tools.AddTools([]llm.Tool{
		testRegistryTool("github__create_pull_request", "Create a pull request", "https://github.example.com"),
	})

	resultJSON, err := searchTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"query":"pull request"}`))
	require.NoError(t, err)

	var result SearchToolsResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.Empty(t, result.Tools)
}

func TestLoadToolExactLookupAddsToolToVisibleStore(t *testing.T) {
	registryTool := testRegistryTool("jira__get_issue", "Get a Jira issue", "https://jira.example.com")
	registry := NewToolRegistry([]llm.Tool{registryTool})
	metaTools := NewMetaTools(registry)
	loadTool := metaToolByName(t, metaTools, LoadToolName)
	llmContext := &llm.Context{Tools: llm.NewToolStore()}
	llmContext.Tools.AddTools(metaTools)
	llmContext.Tools.SetUnloadedMCPTools([]llm.Tool{registryTool})

	resultJSON, err := loadTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"name":"jira__get_issue"}`))
	require.NoError(t, err)

	var result LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.True(t, result.Loaded)
	require.Equal(t, "jira__get_issue", result.Name)
	require.Equal(t, map[string]any{"name": "jira__get_issue"}, result.Schema)
	require.NotNil(t, llmContext.Tools.GetTool("jira__get_issue"))
	require.False(t, llmContext.Tools.IsUnloadedMCPTool("jira__get_issue"))
}

func TestLoadToolDoesNotLoadBareName(t *testing.T) {
	jiraSearch := testRegistryTool("jira__search", "Search Jira", "https://jira.example.com")
	registry := NewToolRegistry([]llm.Tool{jiraSearch})
	loadTool := metaToolByName(t, NewMetaTools(registry), LoadToolName)
	llmContext := &llm.Context{Tools: llm.NewToolStore()}
	llmContext.Tools.SetUnloadedMCPTools([]llm.Tool{jiraSearch})

	resultJSON, err := loadTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"name":"search"}`))
	require.NoError(t, err)

	var result LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.False(t, result.Loaded)
	require.Equal(t, "tool not found", result.Error)
	require.Contains(t, metaToolResultItemNames(result.Matches), "jira__search")
	require.Nil(t, llmContext.Tools.GetTool("jira__search"))
}

func TestLoadToolMissReturnsClosestFilteredMatches(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("mattermost__search_users", "Find people", "https://mattermost.example.com"),
	})
	loadTool := metaToolByName(t, NewMetaTools(registry), LoadToolName)
	llmContext := &llm.Context{Tools: llm.NewToolStore()}

	resultJSON, err := loadTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"name":"search user"}`))
	require.NoError(t, err)

	var result LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.False(t, result.Loaded)
	require.Equal(t, "tool not found", result.Error)
	require.Equal(t, []string{"mattermost__search_users"}, metaToolResultItemNames(result.Matches))
	require.Nil(t, llmContext.Tools.GetTool("mattermost__search_users"))
}

func TestLoadToolEmptyNameReturnsJSONError(t *testing.T) {
	loadTool := metaToolByName(t, NewMetaTools(NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__get_issue", "Get issue", "https://jira.example.com"),
	})), LoadToolName)

	resultJSON, err := loadTool.Resolver(context.Background(), &llm.Context{Tools: llm.NewToolStore()}, metaToolArgs(`{"name":"   "}`))
	require.NoError(t, err)

	var result LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	require.False(t, result.Loaded)
	require.Equal(t, "tool name is required", result.Error)
	require.Empty(t, result.Matches)
}

func TestLoadToolInvariantFailuresReturnErrors(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__get_issue", "Get issue", "https://jira.example.com"),
	})
	tests := []struct {
		name       string
		tool       llm.Tool
		context    *llm.Context
		wantErrMsg string
	}{
		{
			name:       "missing LLM context",
			tool:       metaToolByName(t, NewMetaTools(registry), LoadToolName),
			context:    nil,
			wantErrMsg: "missing LLM context",
		},
		{
			name:       "missing registry",
			tool:       metaToolByName(t, NewMetaTools(nil), LoadToolName),
			context:    &llm.Context{Tools: llm.NewToolStore()},
			wantErrMsg: "tool registry unavailable",
		},
		{
			name:       "missing tool store",
			tool:       metaToolByName(t, NewMetaTools(registry), LoadToolName),
			context:    &llm.Context{},
			wantErrMsg: "missing tool store",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.tool.Resolver(context.Background(), tt.context, metaToolArgs(`{"name":"jira__get_issue"}`))
			require.Error(t, err)
			require.Empty(t, result)
			require.Contains(t, err.Error(), tt.wantErrMsg)
		})
	}
}

func TestLoadToolDoesNotMutateRegistryToolSchema(t *testing.T) {
	tool := testRegistryTool("jira__get_issue", "Original schema description", "https://jira.example.com")
	tool.Schema = map[string]any{"source": "original"}
	registry := NewToolRegistry(
		[]llm.Tool{tool},
		WithToolRetrievalOverrides(map[string]ToolRetrievalOverride{
			ToolRetrievalOverrideKey("https://jira.example.com", "get_issue"): {
				Summary: "Override retrieval summary",
			},
		}),
	)
	metaTools := NewMetaTools(registry)
	searchTool := metaToolByName(t, metaTools, SearchToolsName)
	loadTool := metaToolByName(t, metaTools, LoadToolName)
	llmContext := &llm.Context{Tools: llm.NewToolStore()}
	llmContext.Tools.SetUnloadedMCPTools([]llm.Tool{tool})

	searchJSON, err := searchTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"query":"override"}`))
	require.NoError(t, err)
	var searchResult SearchToolsResult
	require.NoError(t, json.Unmarshal([]byte(searchJSON), &searchResult))
	require.Equal(t, "Override retrieval summary", searchResult.Tools[0].Summary)

	loadJSON, err := loadTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"name":"jira__get_issue"}`))
	require.NoError(t, err)
	var loadResult LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(loadJSON), &loadResult))
	require.True(t, loadResult.Loaded)
	require.Equal(t, map[string]any{"source": "original"}, loadResult.Schema)
	require.Equal(t, "Original schema description", llmContext.Tools.GetTool("jira__get_issue").Description)
}

func TestLoadToolResultWireShape(t *testing.T) {
	registryTool := testRegistryTool("jira__get_issue", "Get a Jira issue", "https://jira.example.com")
	registry := NewToolRegistry([]llm.Tool{registryTool})
	loadTool := metaToolByName(t, NewMetaTools(registry), LoadToolName)
	llmContext := &llm.Context{Tools: llm.NewToolStore()}
	llmContext.Tools.SetUnloadedMCPTools([]llm.Tool{registryTool})

	t.Run("success", func(t *testing.T) {
		resultJSON, err := loadTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"name":"jira__get_issue"}`))
		require.NoError(t, err)

		var raw map[string]any
		require.NoError(t, json.Unmarshal([]byte(resultJSON), &raw))
		require.Equal(t, true, raw["loaded"])
		require.Equal(t, "jira__get_issue", raw["name"])
		require.NotContains(t, raw, "error")
		require.NotContains(t, raw, "matches")
	})

	t.Run("miss", func(t *testing.T) {
		resultJSON, err := loadTool.Resolver(context.Background(), &llm.Context{Tools: llm.NewToolStore()}, metaToolArgs(`{"name":"jira__unknown"}`))
		require.NoError(t, err)

		var raw map[string]any
		require.NoError(t, json.Unmarshal([]byte(resultJSON), &raw))
		require.Equal(t, false, raw["loaded"])
		require.Equal(t, "tool not found", raw["error"])
		require.NotContains(t, raw, "name")
		require.NotContains(t, raw, "schema")
	})
}

func TestMetaToolsNilRegistryResolvers(t *testing.T) {
	metaTools := NewMetaTools(nil)
	searchTool := metaToolByName(t, metaTools, SearchToolsName)
	loadTool := metaToolByName(t, metaTools, LoadToolName)

	searchJSON, err := searchTool.Resolver(context.Background(), &llm.Context{}, metaToolArgs(`{"query":"issue"}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"tools":[]}`, searchJSON)

	_, err = loadTool.Resolver(context.Background(), &llm.Context{Tools: llm.NewToolStore()}, metaToolArgs(`{"name":"jira__get_issue"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool registry unavailable")
}

func TestSearchToolsTelemetry(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__get_issue", "Get a Jira issue", "https://jira.example.com"),
	})
	searchTool := metaToolByName(t, NewMetaTools(registry), SearchToolsName)

	tests := []struct {
		name       string
		context    *llm.Context
		argsGetter llm.ToolArgumentGetter
		wantResult string
		wantErr    bool
	}{
		{
			name:       "success",
			context:    &llm.Context{BotUsername: "matty", ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: &fakeMCPDynamicTelemetry{}}},
			argsGetter: metaToolArgs(`{"query":"jira issue"}`),
			wantResult: "success",
		},
		{
			name:       "empty query",
			context:    &llm.Context{BotUsername: "matty", ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: &fakeMCPDynamicTelemetry{}}},
			argsGetter: metaToolArgs(`{"query":"   "}`),
			wantResult: "empty",
		},
		{
			name:       "no results",
			context:    &llm.Context{BotUsername: "matty", ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: &fakeMCPDynamicTelemetry{}}},
			argsGetter: metaToolArgs(`{"query":"no matching tool"}`),
			wantResult: "empty",
		},
		{
			name:    "decode error",
			context: &llm.Context{BotUsername: "matty", ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: &fakeMCPDynamicTelemetry{}}},
			argsGetter: func(any) error {
				return errors.New("decode")
			},
			wantResult: "error",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := searchTool.Resolver(context.Background(), tt.context, tt.argsGetter)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			telemetry := tt.context.ToolRuntime.MCPDynamicToolTelemetry.(*fakeMCPDynamicTelemetry)
			require.Equal(t, []mcpTelemetryEvent{{botName: "matty", event: "search", result: tt.wantResult}}, telemetry.events)
		})
	}
}

func TestLoadToolTelemetry(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__get_issue", "Get a Jira issue", "https://jira.example.com"),
	})

	t.Run("loaded", func(t *testing.T) {
		telemetry := &fakeMCPDynamicTelemetry{}
		loadTool := metaToolByName(t, NewMetaTools(registry), LoadToolName)
		llmContext := &llm.Context{
			BotUsername: "matty",
			Tools:       llm.NewToolStore(),
			ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: telemetry},
		}
		llmContext.Tools.SetUnloadedMCPTools([]llm.Tool{testRegistryTool("jira__get_issue", "Get a Jira issue", "https://jira.example.com")})

		_, err := loadTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"name":"jira__get_issue"}`))
		require.NoError(t, err)
		require.Equal(t, []mcpTelemetryEvent{{botName: "matty", event: "load", result: "loaded"}}, telemetry.events)
	})

	t.Run("miss", func(t *testing.T) {
		telemetry := &fakeMCPDynamicTelemetry{}
		loadTool := metaToolByName(t, NewMetaTools(registry), LoadToolName)
		llmContext := &llm.Context{BotUsername: "matty", Tools: llm.NewToolStore(), ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: telemetry}}

		_, err := loadTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"name":"jira__missing"}`))
		require.NoError(t, err)
		require.Equal(t, []mcpTelemetryEvent{{botName: "matty", event: "load", result: "miss"}}, telemetry.events)
	})

	t.Run("empty name", func(t *testing.T) {
		telemetry := &fakeMCPDynamicTelemetry{}
		loadTool := metaToolByName(t, NewMetaTools(registry), LoadToolName)
		llmContext := &llm.Context{BotUsername: "matty", Tools: llm.NewToolStore(), ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: telemetry}}

		_, err := loadTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"name":"   "}`))
		require.NoError(t, err)
		require.Equal(t, []mcpTelemetryEvent{{botName: "matty", event: "load", result: "error"}}, telemetry.events)
	})

	t.Run("missing tool store", func(t *testing.T) {
		telemetry := &fakeMCPDynamicTelemetry{}
		loadTool := metaToolByName(t, NewMetaTools(registry), LoadToolName)
		llmContext := &llm.Context{BotUsername: "matty", ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: telemetry}}

		_, err := loadTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"name":"jira__get_issue"}`))
		require.Error(t, err)
		require.Equal(t, []mcpTelemetryEvent{{botName: "matty", event: "load", result: "error"}}, telemetry.events)
	})

	t.Run("decode error", func(t *testing.T) {
		telemetry := &fakeMCPDynamicTelemetry{}
		loadTool := metaToolByName(t, NewMetaTools(registry), LoadToolName)
		llmContext := &llm.Context{BotUsername: "matty", Tools: llm.NewToolStore(), ToolRuntime: llm.ToolRuntimeContext{MCPDynamicToolTelemetry: telemetry}}

		_, err := loadTool.Resolver(context.Background(), llmContext, func(any) error {
			return errors.New("decode")
		})
		require.Error(t, err)
		require.Equal(t, []mcpTelemetryEvent{{botName: "matty", event: "load", result: "error"}}, telemetry.events)
	})
}

func TestSearchLoadStateMarkers(t *testing.T) {
	registryTool := testRegistryTool("jira__get_issue", "Get a Jira issue", "https://jira.example.com")
	registry := NewToolRegistry([]llm.Tool{registryTool})
	metaTools := NewMetaTools(registry)
	searchTool := metaToolByName(t, metaTools, SearchToolsName)
	loadTool := metaToolByName(t, metaTools, LoadToolName)
	llmContext := &llm.Context{Tools: llm.NewToolStore()}
	llmContext.Tools.SetUnloadedMCPTools([]llm.Tool{registryTool})

	_, err := searchTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"query":"jira issue"}`))
	require.NoError(t, err)
	require.True(t, llmContext.ToolRuntime.MCPDynamicToolSearchUsed)

	_, err = loadTool.Resolver(context.Background(), llmContext, metaToolArgs(`{"name":"jira__get_issue"}`))
	require.NoError(t, err)
	require.True(t, llmContext.ToolRuntime.MCPDynamicLoadedToolNames["jira__get_issue"])
	require.True(t, llmContext.ShouldRecordMCPDynamicSearchLoadCallSuccess("jira__get_issue"))
	require.False(t, llmContext.ShouldRecordMCPDynamicSearchLoadCallSuccess("jira__get_issue"))
}

func metaToolByName(t *testing.T, tools []llm.Tool, name string) llm.Tool {
	t.Helper()

	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	require.FailNow(t, "meta tool not found", "name=%s", name)
	return llm.Tool{}
}

func metaToolArgs(raw string) llm.ToolArgumentGetter {
	return func(args any) error {
		return json.Unmarshal([]byte(raw), args)
	}
}

func metaToolResultItemNames(items []SearchToolsResultItem) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}
