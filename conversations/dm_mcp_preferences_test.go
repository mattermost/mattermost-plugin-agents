// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dmPreferenceGithubOrigin = "https://github.example.com"
	dmPreferenceJiraOrigin   = "https://jira.example.com"
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

func configureDMPreferenceMCPTools(env *dmTestEnv) {
	env.mcpMgr.tools = []llm.Tool{
		{
			Name:         "github__search_code",
			Description:  "Search GitHub code",
			ServerOrigin: dmPreferenceGithubOrigin,
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
				return "github result", nil
			},
		},
		{
			Name:         "jira__get_issue",
			Description:  "Get a Jira issue",
			ServerOrigin: dmPreferenceJiraOrigin,
			Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
			Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
				return "jira result", nil
			},
		},
	}
}

func saveDisabledGithubPreference(t *testing.T, env *dmTestEnv) {
	t.Helper()

	_, err := mcp.SaveUserPreferences(env.mmClient, env.userID, &mcp.UserToolProviderPreferences{
		DisabledServers: []string{dmPreferenceGithubOrigin},
	})
	require.NoError(t, err)
}

func assertGithubDisabledJiraReachable(t *testing.T, llmCtx *llm.Context) {
	t.Helper()

	require.NotNil(t, llmCtx)
	require.NotNil(t, llmCtx.Tools)

	assert.Empty(t, dmSearchToolNames(t, llmCtx, "github"))
	assert.Contains(t, dmSearchToolNames(t, llmCtx, "jira"), "jira__get_issue")
	assert.False(t, dmLoadTool(t, llmCtx, "github__search_code").Loaded)
	assert.True(t, dmLoadTool(t, llmCtx, "jira__get_issue").Loaded)
	assert.Nil(t, llmCtx.Tools.GetTool("github__search_code"))
	assert.NotNil(t, llmCtx.Tools.GetTool("jira__get_issue"))
}

func TestDMMessagePostedDisabledMCPServerNotReachableByDynamicMetaTools(t *testing.T) {
	env := setupDMTestEnv(t, dmMakeTextStream("Done"))
	configureDMPreferenceMCPTools(env)
	saveDisabledGithubPreference(t, env)

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

	assertGithubDisabledJiraReachable(t, llmCtx)
}

func TestGroupDMMentionPostedDisabledMCPServerNotReachableByDynamicMetaTools(t *testing.T) {
	env := setupDMTestEnv(t, dmMakeTextStream("Done"))
	env.channel.Type = model.ChannelTypeGroup
	configureDMPreferenceMCPTools(env)
	saveDisabledGithubPreference(t, env)

	post := &model.Post{
		Id:        "post1",
		UserId:    env.userID,
		ChannelId: env.channelID,
		Message:   "@ai use a tool",
	}
	env.mmClient.postThreads = map[string]*model.PostList{
		post.Id: {
			Order: []string{post.Id},
			Posts: map[string]*model.Post{
				post.Id: post,
			},
		},
	}

	env.conversations.MessageHasBeenPosted(nil, post)

	env.fakeLLM.mu.Lock()
	require.Len(t, env.fakeLLM.requests, 1)
	llmCtx := env.fakeLLM.requests[0].Context
	env.fakeLLM.mu.Unlock()

	assertGithubDisabledJiraReachable(t, llmCtx)
}
