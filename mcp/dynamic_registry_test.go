// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/stretchr/testify/require"
)

func TestToolRegistryLookupAndList(t *testing.T) {
	tools := []llm.Tool{
		testRegistryTool("mattermost__search_users", "Search users", "https://mattermost.example.com"),
		testRegistryTool("jira__get_issue", "Get a Jira issue", "https://jira.example.com"),
	}

	registry := NewToolRegistry(tools)

	require.Equal(t, 2, registry.Len())
	require.Equal(t, []string{"jira__get_issue", "mattermost__search_users"}, registryEntryNames(registry.List()))

	entry, ok := registry.Lookup("jira__get_issue")
	require.True(t, ok)
	require.Equal(t, "jira__get_issue", entry.Name)
	require.Equal(t, "get_issue", entry.BareName)
	require.Equal(t, "https://jira.example.com", entry.ServerOrigin)
	require.Equal(t, "Get a Jira issue", entry.RetrievalSummary)
	require.Equal(t, tools[1].Schema, entry.Tool.Schema)

	result, err := entry.Tool.Resolver(context.Background(), &llm.Context{}, func(args any) error { return nil })
	require.NoError(t, err)
	require.Equal(t, "jira__get_issue resolved", result)
}

func TestToolRegistrySearchTop8Deterministic(t *testing.T) {
	tools := make([]llm.Tool, 0, 10)
	for i := 9; i >= 0; i-- {
		tools = append(tools, testRegistryTool(fmt.Sprintf("server__tool_%02d", i), "shared capability", "https://server.example.com"))
	}
	registry := NewToolRegistry(tools)

	results := registry.Search("shared", 0)

	require.Len(t, results, DefaultMCPToolSearchLimit)
	require.Equal(t, []string{
		"server__tool_00",
		"server__tool_01",
		"server__tool_02",
		"server__tool_03",
		"server__tool_04",
		"server__tool_05",
		"server__tool_06",
		"server__tool_07",
	}, registrySearchResultNames(results))
	for i := 1; i < len(results); i++ {
		require.LessOrEqual(t, results[i].Score, results[i-1].Score)
	}
}

func TestToolRegistrySearchEmptyQuery(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__get_issue", "Get issue", "https://jira.example.com"),
	})

	require.Nil(t, registry.Search("", 10))
	require.Nil(t, registry.Search("   ", 10))
}

func TestToolRegistrySearchUsesDescriptionOverrideForRetrievalOnly(t *testing.T) {
	tool := testRegistryTool("jira__get_issue", "Original schema description", "https://jira.example.com")
	registry := NewToolRegistry(
		[]llm.Tool{tool},
		WithToolRetrievalOverrides(map[string]ToolRetrievalOverride{
			ToolRetrievalOverrideKey("https://jira.example.com", "get_issue"): {
				Summary: "Find Jira incidents by key",
			},
		}),
	)

	results := registry.Search("incidents", 10)
	require.Len(t, results, 1)
	require.Equal(t, "jira__get_issue", results[0].Name)
	require.Equal(t, "Find Jira incidents by key", results[0].Summary)

	entry, ok := registry.Lookup("jira__get_issue")
	require.True(t, ok)
	require.Equal(t, "Original schema description", entry.Tool.Description)
	require.Equal(t, "Find Jira incidents by key", entry.RetrievalSummary)
}

func TestToolRegistryOverrideKeyUsesBareName(t *testing.T) {
	require.Equal(
		t,
		ToolRetrievalOverrideKey("https://jira.example.com", "get_issue"),
		ToolRetrievalOverrideKey("https://jira.example.com", "jira__get_issue"),
	)
}

func TestToolRegistryDuplicateNamespacedToolLastWins(t *testing.T) {
	first := testRegistryTool("jira__search", "First search description", "https://jira.example.com")
	first.Schema = map[string]any{"version": "first"}
	second := testRegistryTool("jira__search", "Second search description", "https://jira.example.com")
	second.Schema = map[string]any{"version": "second"}

	registry := NewToolRegistry([]llm.Tool{first, second})

	require.Equal(t, 1, registry.Len())
	require.Equal(t, []string{"jira__search"}, registryEntryNames(registry.List()))

	entry, ok := registry.Lookup("jira__search")
	require.True(t, ok)
	require.Equal(t, "Second search description", entry.Tool.Description)
	require.Equal(t, map[string]any{"version": "second"}, entry.Tool.Schema)
}

func TestToolRegistryDuplicateBareNamesDifferentNamespaces(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__search", "Search Jira", "https://jira.example.com"),
		testRegistryTool("github__search", "Search GitHub", "https://github.example.com"),
	})

	require.Equal(t, 2, registry.Len())
	require.Equal(t, []string{"github__search", "jira__search"}, registryEntryNames(registry.List()))

	jiraEntry, ok := registry.Lookup("jira__search")
	require.True(t, ok)
	require.Equal(t, "https://jira.example.com", jiraEntry.ServerOrigin)

	githubEntry, ok := registry.Lookup("github__search")
	require.True(t, ok)
	require.Equal(t, "https://github.example.com", githubEntry.ServerOrigin)
}

func TestToolRegistryClosestMatchesUsesBM25(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__get_issue", "Get a Jira issue", "https://jira.example.com"),
		testRegistryTool("github__create_pull_request", "Create a pull request", "https://github.example.com"),
	})

	results := registry.ClosestMatches("issue", 10)

	require.NotEmpty(t, results)
	require.Equal(t, "jira__get_issue", results[0].Name)
}

func TestToolRegistryClosestMatchesFallbackForMiss(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("mattermost__search_users", "Find people", "https://mattermost.example.com"),
		testRegistryTool("github__create_pull_request", "Open collaboration review", "https://github.example.com"),
	})

	results := registry.ClosestMatches("matter", 10)

	require.Len(t, results, 1)
	require.Equal(t, "mattermost__search_users", results[0].Name)
	require.Greater(t, results[0].Score, 0.0)
}

func TestToolRegistryLookupBareNameDoesNotSucceed(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__search", "Search Jira", "https://jira.example.com"),
	})

	_, ok := registry.Lookup("search")
	require.False(t, ok)
}

func TestToolRegistryNilReceiverSafe(t *testing.T) {
	var registry *ToolRegistry

	require.Equal(t, 0, registry.Len())
	require.Nil(t, registry.List())

	entry, ok := registry.Lookup("jira__search")
	require.False(t, ok)
	require.Empty(t, entry)

	require.Nil(t, registry.Search("jira", 10))
	require.Nil(t, registry.ClosestMatches("jira", 10))
}

func TestToolRegistryZeroValueSafe(t *testing.T) {
	registry := &ToolRegistry{}

	require.Equal(t, 0, registry.Len())
	require.Nil(t, registry.List())

	entry, ok := registry.Lookup("jira__search")
	require.False(t, ok)
	require.Empty(t, entry)

	require.Nil(t, registry.Search("jira", 10))
	require.Nil(t, registry.ClosestMatches("jira", 10))
}

func TestToolRegistrySkipsEmptyNamesAndSearchesEmptyDescriptionsByName(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("", "No runtime name", "https://jira.example.com"),
		testRegistryTool("jira__get_issue", "", "https://jira.example.com"),
	})

	require.Equal(t, 1, registry.Len())
	results := registry.Search("get issue", 10)
	require.Len(t, results, 1)
	require.Equal(t, "jira__get_issue", results[0].Name)
}

func TestToolRegistryOnlyContainsProvidedFilteredTools(t *testing.T) {
	registry := NewToolRegistry([]llm.Tool{
		testRegistryTool("jira__get_issue", "Get Jira issue", "https://jira.example.com"),
	})

	require.Equal(t, []string{"jira__get_issue"}, registryEntryNames(registry.List()))

	_, ok := registry.Lookup("github__create_pull_request")
	require.False(t, ok)
	require.Nil(t, registry.Search("pull request", 10))
	require.Nil(t, registry.ClosestMatches("github", 10))
}

func testRegistryTool(name, desc, origin string) llm.Tool {
	return llm.Tool{
		Name:         name,
		Description:  desc,
		Schema:       map[string]any{"name": name},
		ServerOrigin: origin,
		Resolver: func(_ context.Context, context *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			return name + " resolved", nil
		},
	}
}

func registryEntryNames(entries []ToolRegistryEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func registrySearchResultNames(results []ToolSearchResult) []string {
	names := make([]string, 0, len(results))
	for _, result := range results {
		names = append(names, result.Name)
	}
	return names
}
