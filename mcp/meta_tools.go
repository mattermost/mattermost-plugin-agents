// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

const (
	SearchToolsName = "search_tools"
	LoadToolName    = "load_tool"
)

// UnloadedMCPToolUserHint returns the canonical message returned to the LLM
// when it tries to call an MCP tool that is visible in the registry but has
// not yet been loaded into the active tool store. Callers that surface this
// to the model should reuse this helper so the wording (and the suggested
// load_tool invocation) stays consistent across entry points.
func UnloadedMCPToolUserHint(name string) string {
	return fmt.Sprintf(`tool %s is available but not loaded. Call %s with {"name":%q} before calling it.`, name, LoadToolName, name)
}

type SearchToolsArgs struct {
	Query string `json:"query" jsonschema:"Search query for finding available MCP tools,minLength=1"`
}

type LoadToolArgs struct {
	Name string `json:"name" jsonschema:"Exact namespaced MCP tool name to load,minLength=1"`
}

type SearchToolsResult struct {
	Tools []SearchToolsResultItem `json:"tools"`
}

type SearchToolsResultItem struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

type LoadToolResult struct {
	Loaded  bool                    `json:"loaded"`
	Name    string                  `json:"name,omitempty"`
	Schema  any                     `json:"schema,omitempty"`
	Matches []SearchToolsResultItem `json:"matches,omitempty"`
	Error   string                  `json:"error,omitempty"`
}

func IsMCPMetaTool(name string) bool {
	return name == SearchToolsName || name == LoadToolName
}

func NewMetaTools(registry *ToolRegistry) []llm.Tool {
	return []llm.Tool{
		{
			Name:        SearchToolsName,
			Description: "Search available MCP tools by keyword.",
			Schema:      llm.NewJSONSchemaFromStruct[SearchToolsArgs](),
			Resolver:    searchToolsResolver(registry),
		},
		{
			Name:        LoadToolName,
			Description: "Load an MCP tool by exact namespaced name.",
			Schema:      llm.NewJSONSchemaFromStruct[LoadToolArgs](),
			Resolver:    loadToolResolver(registry),
		},
	}
}

func searchToolsResolver(registry *ToolRegistry) llm.ToolResolver {
	return func(_ context.Context, llmContext *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
		observe := func(result string) {
			if llmContext != nil {
				llmContext.ObserveMCPDynamicToolEvent("search", result)
			}
		}

		var args SearchToolsArgs
		if err := argsGetter(&args); err != nil {
			observe("error")
			return "", err
		}

		query := strings.TrimSpace(args.Query)
		items := []SearchToolsResultItem{}
		if query != "" {
			if llmContext != nil {
				llmContext.MarkMCPDynamicToolSearch()
			}
			if registry != nil {
				if found := searchResultsToMetaToolItems(registry.Search(query, DefaultMCPToolSearchLimit)); len(found) > 0 {
					items = found
				}
			}
		}

		if len(items) == 0 {
			observe("empty")
		} else {
			observe("success")
		}

		return marshalMetaToolResult(SearchToolsResult{Tools: items})
	}
}

func loadToolResolver(registry *ToolRegistry) llm.ToolResolver {
	return func(_ context.Context, llmContext *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
		observe := func(result string) {
			if llmContext != nil {
				llmContext.ObserveMCPDynamicToolEvent("load", result)
			}
		}

		var args LoadToolArgs
		if err := argsGetter(&args); err != nil {
			observe("error")
			return "", err
		}

		name := strings.TrimSpace(args.Name)
		if name == "" {
			observe("error")
			return marshalMetaToolResult(LoadToolResult{
				Loaded: false,
				Error:  "tool name is required",
			})
		}

		if llmContext == nil {
			return "", fmt.Errorf("%s: missing LLM context", LoadToolName)
		}
		if llmContext.Tools == nil {
			observe("error")
			return "", fmt.Errorf("%s: missing tool store", LoadToolName)
		}
		if registry == nil {
			observe("error")
			return "", fmt.Errorf("%s: tool registry unavailable", LoadToolName)
		}

		loaded := llmContext.Tools.LoadMCPTools([]string{name})
		if len(loaded) == 1 {
			llmContext.MarkMCPDynamicToolLoaded(loaded[0].Name)
			observe("loaded")
			return marshalMetaToolResult(LoadToolResult{
				Loaded: true,
				Name:   loaded[0].Name,
				Schema: loaded[0].Schema,
			})
		}

		// Already-loaded tools are idempotent successes.
		if existing := llmContext.Tools.GetTool(name); existing != nil {
			observe("loaded")
			return marshalMetaToolResult(LoadToolResult{
				Loaded: true,
				Name:   name,
				Schema: existing.Schema,
			})
		}

		observe("miss")
		return marshalMetaToolResult(LoadToolResult{
			Loaded: false,
			Error:  "tool not found",
			Matches: searchResultsToMetaToolItems(
				registry.ClosestMatches(name, DefaultMCPToolSearchLimit),
			),
		})
	}
}

func searchResultsToMetaToolItems(results []ToolSearchResult) []SearchToolsResultItem {
	if len(results) == 0 {
		return nil
	}

	items := make([]SearchToolsResultItem, 0, len(results))
	for _, result := range results {
		items = append(items, SearchToolsResultItem{
			Name:    result.Name,
			Summary: result.Summary,
		})
	}
	return items
}

func marshalMetaToolResult(result any) (string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
