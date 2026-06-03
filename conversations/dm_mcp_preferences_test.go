// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dmMetaToolArgs(raw string) llm.ToolArgumentGetter {
	return func(args any) error {
		return json.Unmarshal([]byte(raw), args)
	}
}

func dmSearchToolNames(t *testing.T, llmCtx *llm.Context, query string) []string {
	t.Helper()

	searchTool := llmCtx.Tools.GetTool(mcp.SearchToolsName)
	require.NotNil(t, searchTool)

	resultJSON, err := searchTool.Resolver(context.Background(), llmCtx, dmMetaToolArgs(`{"query":"`+query+`"}`))
	require.NoError(t, err)

	var result mcp.SearchToolsResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))

	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func dmLoadTool(t *testing.T, llmCtx *llm.Context, name string) mcp.LoadToolResult {
	t.Helper()

	loadTool := llmCtx.Tools.GetTool(mcp.LoadToolName)
	require.NotNil(t, loadTool)

	resultJSON, err := loadTool.Resolver(context.Background(), llmCtx, dmMetaToolArgs(`{"name":"`+name+`"}`))
	require.NoError(t, err)

	var result mcp.LoadToolResult
	require.NoError(t, json.Unmarshal([]byte(resultJSON), &result))
	return result
}

func TestDMMessagePostedDisabledMCPServerNotReachableByDynamicMetaTools(t *testing.T) {
	const (
		githubOrigin = "https://github.example.com"
		jiraOrigin   = "https://jira.example.com"
	)

	env := setupDMTestEnv(t, dmMakeTextStream("Done"))
	env.mcpMgr.tools = []llm.Tool{
		{
			Name:         "github__search_code",
			Description:  "Search GitHub code",
			ServerOrigin: githubOrigin,
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
				return "github result", nil
			},
		},
		{
			Name:         "jira__get_issue",
			Description:  "Get a Jira issue",
			ServerOrigin: jiraOrigin,
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
				return "jira result", nil
			},
		},
	}
	_, err := mcp.SaveUserPreferences(env.mmClient, env.userID, &mcp.UserToolProviderPreferences{
		DisabledServers: []string{githubOrigin},
	})
	require.NoError(t, err)

	env.conversations.MessageHasBeenPosted(nil, &model.Post{
		Id:        "post1",
		UserId:    env.userID,
		ChannelId: env.channelID,
		Message:   "Use a tool",
	})

	env.fakeLLM.mu.Lock()
	require.Len(t, env.fakeLLM.requests, 1)
	llmCtx := env.fakeLLM.requests[0].Context
	env.fakeLLM.mu.Unlock()
	require.NotNil(t, llmCtx)
	require.NotNil(t, llmCtx.Tools)

	assert.Empty(t, dmSearchToolNames(t, llmCtx, "github"))
	assert.Contains(t, dmSearchToolNames(t, llmCtx, "jira"), "jira__get_issue")
	assert.False(t, dmLoadTool(t, llmCtx, "github__search_code").Loaded)
	assert.True(t, dmLoadTool(t, llmCtx, "jira__get_issue").Loaded)
	assert.Nil(t, llmCtx.Tools.GetTool("github__search_code"))
	assert.NotNil(t, llmCtx.Tools.GetTool("jira__get_issue"))
}
