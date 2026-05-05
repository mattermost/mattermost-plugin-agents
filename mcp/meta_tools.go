// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"encoding/json"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

const (
	SearchToolsName = "search_tools"
	LoadToolName    = "load_tool"
)

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

type LoadedToolRecorder func(llmContext *llm.Context, entry MCPToolRegistryEntry) error

type MetaToolOption func(*metaToolOptions)

type metaToolOptions struct {
	loadedToolRecorder LoadedToolRecorder
}

func WithLoadedToolRecorder(recorder LoadedToolRecorder) MetaToolOption {
	return func(options *metaToolOptions) {
		options.loadedToolRecorder = recorder
	}
}

func IsMCPMetaTool(name string) bool {
	return name == SearchToolsName || name == LoadToolName
}

func NewMetaTools(registry *MCPToolRegistry, opts ...MetaToolOption) []llm.Tool {
	options := metaToolOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	return []llm.Tool{
		{
			Name:        SearchToolsName,
			Description: "Use this internal helper to find available MCP tools before loading one. It searches only tools available in the active filtered registry and returns exact names with summaries.",
			Schema:      llm.NewJSONSchemaFromStruct[SearchToolsArgs](),
			Resolver:    searchToolsResolver(registry),
		},
		{
			Name:        LoadToolName,
			Description: "Use this internal helper with exact names returned by search_tools. After loading, the selected MCP tool can be called by that exact name.",
			Schema:      llm.NewJSONSchemaFromStruct[LoadToolArgs](),
			Resolver:    loadToolResolver(registry, options.loadedToolRecorder),
		},
	}
}

func searchToolsResolver(registry *MCPToolRegistry) llm.ToolResolver {
	return func(_ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
		var args SearchToolsArgs
		if err := argsGetter(&args); err != nil {
			return "", err
		}

		query := strings.TrimSpace(args.Query)
		result := SearchToolsResult{
			Tools: []SearchToolsResultItem{},
		}
		if query == "" || registry == nil {
			return marshalMetaToolResult(result)
		}

		result.Tools = searchResultsToMetaToolItems(registry.Search(query, DefaultMCPToolSearchLimit))
		if result.Tools == nil {
			result.Tools = []SearchToolsResultItem{}
		}

		return marshalMetaToolResult(result)
	}
}

func loadToolResolver(registry *MCPToolRegistry, recorder LoadedToolRecorder) llm.ToolResolver {
	return func(llmContext *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
		var args LoadToolArgs
		if err := argsGetter(&args); err != nil {
			return "", err
		}

		name := strings.TrimSpace(args.Name)
		if name == "" {
			return marshalMetaToolResult(LoadToolResult{
				Loaded: false,
				Error:  "tool name is required",
			})
		}

		entry, ok := registry.Lookup(name)
		if !ok {
			return marshalMetaToolResult(LoadToolResult{
				Loaded: false,
				Error:  "tool not found",
				Matches: searchResultsToMetaToolItems(
					registry.ClosestMatches(name, DefaultMCPToolSearchLimit),
				),
			})
		}

		if llmContext == nil {
			return marshalMetaToolResult(LoadToolResult{
				Loaded: false,
				Error:  "cannot load tool without an LLM context",
			})
		}
		if llmContext.Tools == nil {
			return marshalMetaToolResult(LoadToolResult{
				Loaded: false,
				Error:  "cannot load tool without a visible tool store",
			})
		}

		llmContext.Tools.AddTools([]llm.Tool{entry.Tool})
		if recorder != nil {
			if err := recorder(llmContext, entry); err != nil {
				return "", err
			}
		}

		return marshalMetaToolResult(LoadToolResult{
			Loaded: true,
			Name:   entry.Name,
			Schema: entry.Tool.Schema,
		})
	}
}

func searchResultsToMetaToolItems(results []MCPToolSearchResult) []SearchToolsResultItem {
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
