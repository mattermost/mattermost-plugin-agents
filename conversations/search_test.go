// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const searchToolResult = "Result 1 by @alice: the launch moved to Friday"

func embeddedTestTool(bareName, result string) llm.Tool {
	return llm.Tool{
		Name:         llm.NamespaceMCPToolName("mattermost", bareName),
		Description:  bareName,
		ServerOrigin: mcp.EmbeddedClientKey,
		Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
		Resolver: func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
			return result, nil
		},
	}
}

func embeddedSearchTestTools() []llm.Tool {
	return []llm.Tool{
		embeddedTestTool("search_posts", searchToolResult),
		embeddedTestTool("read_post", "post"),
		embeddedTestTool("read_channel", "channel"),
		embeddedTestTool("create_post", "created"),
	}
}

// setupSearchTestEnv returns a DM test env whose agent uses cfg and whose
// embedded MCP server offers tools.
func setupSearchTestEnv(t *testing.T, tools []llm.Tool, configure func(*llm.BotConfig), responses ...*llm.TextStreamResult) (*dmTestEnv, *bots.Bot) {
	t.Helper()
	env := setupDMTestEnv(t, responses...)
	env.mcpMgr.tools = tools
	env.mmClient.dmChannelID = env.channelID
	env.streamService.finished = make(chan string, 4)
	env.policyChecker.setAutoRun(mcp.EmbeddedClientKey, "search_posts")
	for i := 1; i <= 10; i++ {
		args := make([]any, i)
		for j := range args {
			args[j] = mock.Anything
		}
		env.mockAPI.On("LogDebug", args...).Maybe()
		env.mockAPI.On("LogWarn", args...).Maybe()
	}

	cfg := llm.BotConfig{
		ID:                    env.botID,
		Name:                  "ai",
		DisplayName:           "AI",
		AutoEnableNewMCPTools: true,
		MCPDynamicToolLoading: true,
	}
	if configure != nil {
		configure(&cfg)
	}
	bot := bots.NewBot(cfg,
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: env.botID, Username: "ai", DisplayName: "AI"},
		env.fakeLLM,
	)
	env.botService.SetBotsForTesting([]*bots.Bot{bot})
	return env, bot
}

func waitForStreamFinished(t *testing.T, env *dmTestEnv) {
	t.Helper()
	select {
	case <-env.streamService.finished:
	case <-time.After(10 * time.Second):
		t.Fatal("search response never finished streaming")
	}
}

func searchConversation(t *testing.T, env *dmTestEnv, questionPostID string) *store.Conversation {
	t.Helper()
	conv, err := env.convService.GetConversationByThread(questionPostID, env.botID, env.userID)
	require.NoError(t, err)
	require.NotNil(t, conv)
	return conv
}

func visibleToolNames(llmCtx *llm.Context) []string {
	var names []string
	for _, tool := range llmCtx.Tools.GetTools() {
		names = append(names, tool.Name)
	}
	return names
}

func TestHandleSearchRunsToolLoopInAgentDM(t *testing.T) {
	tests := []struct {
		name           string
		dynamicLoading bool
		searchToolName string
	}{
		{name: "dynamic tool loading preloads the search tools", dynamicLoading: true, searchToolName: "search_posts"},
		{name: "full MCP catalog exposes search_posts once", dynamicLoading: false, searchToolName: "mattermost__search_posts"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolCall := llm.ToolCall{
				ID:           "call_search",
				Name:         tt.searchToolName,
				Arguments:    json.RawMessage(`{"query":"launch"}`),
				ServerOrigin: mcp.EmbeddedClientKey,
			}
			env, bot := setupSearchTestEnv(t, embeddedSearchTestTools(),
				func(cfg *llm.BotConfig) { cfg.MCPDynamicToolLoading = tt.dynamicLoading },
				dmMakeToolCallStream([]llm.ToolCall{toolCall}),
				dmMakeTextStream("The launch moved to Friday."),
			)

			questionPost, err := env.conversations.HandleSearch(context.Background(), bot, env.user, conversations.SearchRequest{Query: "when is the launch?"})
			require.NoError(t, err)
			waitForStreamFinished(t, env)

			assert.Equal(t, env.userID, questionPost.UserId)
			assert.Equal(t, env.channelID, questionPost.ChannelId)
			assert.Equal(t, "when is the launch?", questionPost.Message)

			require.NotEmpty(t, env.mmClient.createdPosts)
			responsePost := env.mmClient.createdPosts[0]
			assert.Equal(t, env.botID, responsePost.UserId)
			assert.Equal(t, questionPost.Id, responsePost.RootId)

			conv := searchConversation(t, env, questionPost.Id)
			assert.Equal(t, llm.OperationSearch, conv.Operation)

			env.fakeLLM.mu.Lock()
			requests := env.fakeLLM.requests
			env.fakeLLM.mu.Unlock()
			require.Len(t, requests, 2)

			var searchTools []string
			for _, name := range visibleToolNames(requests[0].Context) {
				if llm.BareMCPToolName(name) == "search_posts" {
					searchTools = append(searchTools, name)
				}
			}
			assert.Equal(t, []string{tt.searchToolName}, searchTools)

			var toolResult string
			for _, turn := range env.convStore.turnsFor(conv.ID) {
				blocks, unmarshalErr := conversation.UnmarshalBlocks(turn.Content)
				require.NoError(t, unmarshalErr)
				for _, block := range blocks {
					if block.Type == conversation.BlockTypeToolResult {
						toolResult = block.Content
					}
				}
			}
			assert.Equal(t, searchToolResult, toolResult)
		})
	}
}

func TestHandleSearchSystemPrompt(t *testing.T) {
	const startChannelID = "startchannel00000000000000"

	tests := []struct {
		name        string
		request     conversations.SearchRequest
		contains    []string
		notContains []string
	}{
		{
			name:        "search bar query without a team cites through the redirect route",
			request:     conversations.SearchRequest{Query: "launch"},
			contains:    []string{"/_redirect/pl/<post_id>"},
			notContains: []string{startChannelID},
		},
		{
			name: "ask-channel points the agent at the channel and cites with the team name",
			request: conversations.SearchRequest{
				Query:   "launch",
				Team:    &model.Team{Id: "teamid", Name: "eng"},
				Channel: &model.Channel{Id: startChannelID, DisplayName: "Launch Planning"},
			},
			contains:    []string{"/eng/pl/<post_id>", startChannelID, "Launch Planning"},
			notContains: []string{"/_redirect/pl/"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, bot := setupSearchTestEnv(t, embeddedSearchTestTools(), nil, dmMakeTextStream("Nothing found."))

			questionPost, err := env.conversations.HandleSearch(context.Background(), bot, env.user, tt.request)
			require.NoError(t, err)
			waitForStreamFinished(t, env)

			prompt := searchConversation(t, env, questionPost.Id).SystemPrompt
			for _, want := range tt.contains {
				assert.Contains(t, prompt, want)
			}
			for _, unwanted := range tt.notContains {
				assert.NotContains(t, prompt, unwanted)
			}
		})
	}
}

func TestHandleSearchWithoutSearchTool(t *testing.T) {
	tests := []struct {
		name      string
		tools     []llm.Tool
		configure func(*llm.BotConfig)
	}{
		{name: "embedded search tool unavailable", tools: []llm.Tool{embeddedTestTool("create_post", "created")}},
		{name: "agent has tools disabled", tools: embeddedSearchTestTools(), configure: func(cfg *llm.BotConfig) { cfg.DisableTools = true }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, bot := setupSearchTestEnv(t, tt.tools, tt.configure)
			updated := make(chan string, 4)
			env.mmClient.onUpdatePost = func(post *model.Post) { updated <- post.Message }

			questionPost, err := env.conversations.HandleSearch(context.Background(), bot, env.user, conversations.SearchRequest{Query: "launch"})
			require.NoError(t, err)

			select {
			case message := <-updated:
				assert.Contains(t, message, "search_posts")
			case <-time.After(10 * time.Second):
				t.Fatal("placeholder was never updated")
			}

			conv, err := env.convService.GetConversationByThread(questionPost.Id, env.botID, env.userID)
			require.NoError(t, err)
			assert.Nil(t, conv, "no conversation should be created when the agent cannot search")

			env.fakeLLM.mu.Lock()
			defer env.fakeLLM.mu.Unlock()
			assert.Empty(t, env.fakeLLM.requests)
		})
	}
}

func TestSearchThreadReplyKeepsSearchTools(t *testing.T) {
	tests := []struct {
		name          string
		operation     string
		wantPreloaded bool
	}{
		{name: "search conversation", operation: llm.OperationSearch, wantPreloaded: true},
		{name: "regular conversation", operation: llm.OperationConversation, wantPreloaded: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, _ := setupSearchTestEnv(t, embeddedSearchTestTools(), nil, dmMakeTextStream("Follow-up answer"))

			rootPostID := model.NewId()
			channelID := env.channelID
			_, err := env.convService.CreateConversation(conversation.CreateConversationParams{
				UserID:       env.userID,
				BotID:        env.botID,
				ChannelID:    &channelID,
				RootPostID:   &rootPostID,
				Operation:    tt.operation,
				SystemPrompt: "system",
				UserMessage:  "first question",
			})
			require.NoError(t, err)

			env.conversations.MessageHasBeenPosted(nil, &model.Post{
				Id:        model.NewId(),
				UserId:    env.userID,
				ChannelId: env.channelID,
				RootId:    rootPostID,
				Message:   "and what about the beta?",
			})
			waitForStreamFinished(t, env)

			env.fakeLLM.mu.Lock()
			require.Len(t, env.fakeLLM.requests, 1)
			llmCtx := env.fakeLLM.requests[0].Context
			env.fakeLLM.mu.Unlock()

			if tt.wantPreloaded {
				assert.Contains(t, visibleToolNames(llmCtx), "search_posts")
			} else {
				assert.NotContains(t, visibleToolNames(llmCtx), "search_posts")
			}
		})
	}
}
