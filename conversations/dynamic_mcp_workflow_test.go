// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

type dynamicWorkflowLLM struct {
	mu       sync.Mutex
	calls    int
	requests []llm.CompletionRequest
}

func (l *dynamicWorkflowLLM) ChatCompletion(request llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.requests = append(l.requests, request)
	l.calls++

	switch l.calls {
	case 1:
		return dynamicWorkflowStream(llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{
			ID:        "search-1",
			Name:      mcp.SearchToolsName,
			Arguments: json.RawMessage(`{"query":"jira issue"}`),
		}}}), nil
	case 2:
		return dynamicWorkflowStream(llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{
			ID:        "load-1",
			Name:      mcp.LoadToolName,
			Arguments: json.RawMessage(`{"name":"jira__get_issue"}`),
		}}}), nil
	case 3:
		return dynamicWorkflowStream(llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{{
			ID:        "issue-1",
			Name:      "jira__get_issue",
			Arguments: json.RawMessage(`{"key":"JIRA-1"}`),
		}}}), nil
	default:
		return dynamicWorkflowStream(llm.TextStreamEvent{Type: llm.EventTypeText, Value: "JIRA-1 details returned"}), nil
	}
}

func (l *dynamicWorkflowLLM) ChatCompletionNoStream(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	result, err := l.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (l *dynamicWorkflowLLM) CountTokens(string) int { return 0 }
func (l *dynamicWorkflowLLM) InputTokenLimit() int   { return 100000 }

func dynamicWorkflowStream(events ...llm.TextStreamEvent) *llm.TextStreamResult {
	stream := make(chan llm.TextStreamEvent, len(events)+1)
	for _, event := range events {
		stream <- event
	}
	stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	close(stream)
	return &llm.TextStreamResult{Stream: stream}
}

func TestDynamicMCPStrictSearchLoadCallHappyPath(t *testing.T) {
	const origin = "https://jira.example.com"

	convStore, conv := loadedStateConversationStore()
	loadedStore := &loadedStateStore{}
	resolverCalls := 0
	jiraTool := llm.Tool{
		Name:         "jira__get_issue",
		Description:  "fetch Jira issue details",
		ServerOrigin: origin,
		Schema: llm.NewJSONSchemaFromStruct[struct {
			Key string `json:"key"`
		}](),
		Resolver: func(_ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			resolverCalls++
			var args struct {
				Key string `json:"key"`
			}
			require.NoError(t, argsGetter(&args))
			require.Equal(t, "JIRA-1", args.Key)
			return "JIRA-1 details", nil
		},
	}

	builder := newChannelFollowUpTestBuilder(t, []llm.Tool{jiraTool}, &channelFollowUpTestConfig{})
	builder.SetLoadedMCPToolStore(loadedStore)
	lm := &dynamicWorkflowLLM{}
	bot := loadedStateBot(lm)
	llmContext := builder.BuildLLMContextUserRequest(
		bot,
		&model.User{Id: "user-id", Username: "user", Locale: "en"},
		&model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"},
		builder.WithLLMContextDefaultTools(bot),
	)
	c := &Conversations{
		convService: conversation.NewService(convStore, nil, nil, nil),
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"get_issue": {policy: mcp.ToolPolicyAutoRunInDM, enabled: true},
			},
		},
	}

	streamResult, err := c.ProcessDMRequest(conv.ID, lm, llmContext)
	require.NoError(t, err)
	text, readErr := streamResult.Stream.ReadAll()
	require.NoError(t, readErr)

	require.Equal(t, "JIRA-1 details returned", text)
	require.Equal(t, 1, resolverCalls)
	require.Len(t, loadedStore.rows, 1)
	require.Equal(t, store.LoadedMCPTool{
		ConversationID: conv.ID,
		BotID:          "bot-id",
		UserID:         "user-id",
		ToolName:       "jira__get_issue",
		ServerOrigin:   origin,
		BareName:       "get_issue",
		CreatedAt:      loadedStore.rows[0].CreatedAt,
		UpdatedAt:      loadedStore.rows[0].UpdatedAt,
	}, loadedStore.rows[0])

	require.Len(t, lm.requests, 4)
	require.NotNil(t, lm.requests[2].Context.Tools.GetTool("jira__get_issue"), "load_tool must materialize the schema before the business call")

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 6)

	var searchBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[0].Content, &searchBlocks))
	require.Equal(t, mcp.SearchToolsName, searchBlocks[0].Name)
	require.Equal(t, conversation.StatusAutoApproved, searchBlocks[0].Status)

	var loadResultBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[3].Content, &loadResultBlocks))
	require.Contains(t, loadResultBlocks[0].Content, `"loaded":true`)
	require.Contains(t, loadResultBlocks[0].Content, `"name":"jira__get_issue"`)

	var businessResultBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[5].Content, &businessResultBlocks))
	require.Equal(t, "JIRA-1 details", businessResultBlocks[0].Content)
	require.Equal(t, conversation.StatusSuccess, businessResultBlocks[0].Status)
}

func TestDynamicMCPLoadedToolStillRequiresApprovalWhenPolicyAsks(t *testing.T) {
	const origin = "https://jira.example.com"

	convStore, conv := loadedStateConversationStore()
	loadedStore := &loadedStateStore{}
	resolverCalls := 0
	jiraTool := llm.Tool{
		Name:         "jira__get_issue",
		Description:  "fetch Jira issue details",
		ServerOrigin: origin,
		Schema: llm.NewJSONSchemaFromStruct[struct {
			Key string `json:"key"`
		}](),
		Resolver: func(_ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			resolverCalls++
			var args struct {
				Key string `json:"key"`
			}
			require.NoError(t, argsGetter(&args))
			return "JIRA-1 details", nil
		},
	}

	builder := newChannelFollowUpTestBuilder(t, []llm.Tool{jiraTool}, &channelFollowUpTestConfig{})
	builder.SetLoadedMCPToolStore(loadedStore)
	lm := &dynamicWorkflowLLM{}
	bot := loadedStateBot(lm)
	llmContext := builder.BuildLLMContextUserRequest(
		bot,
		&model.User{Id: "user-id", Username: "user", Locale: "en"},
		&model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"},
		builder.WithLLMContextDefaultTools(bot),
	)
	c := &Conversations{
		convService: conversation.NewService(convStore, nil, nil, nil),
		toolPolicyChecker: mapPolicyChecker{
			origin: {
				"get_issue": {policy: mcp.ToolPolicyAsk, enabled: true},
			},
		},
	}

	streamResult, err := c.ProcessDMRequest(conv.ID, lm, llmContext)
	require.NoError(t, err)

	foundPendingBusinessTool := false
	for event := range streamResult.Stream.Stream {
		if event.Type != llm.EventTypeToolCalls {
			continue
		}
		toolCalls, ok := event.Value.([]llm.ToolCall)
		require.True(t, ok)
		require.Len(t, toolCalls, 1)
		if toolCalls[0].Name == "jira__get_issue" {
			foundPendingBusinessTool = true
		}
	}

	require.True(t, foundPendingBusinessTool, "loaded business tool should still surface for approval when policy is ask")
	require.Zero(t, resolverCalls, "approval-only tools must not execute during the dynamic load flow")
	require.Len(t, loadedStore.rows, 1, "load_tool should still persist the newly loaded tool")
	require.Len(t, lm.requests, 3)
	require.NotNil(t, lm.requests[2].Context.Tools.GetTool("jira__get_issue"), "load_tool must materialize the schema before the approval-gated business call")

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 4)

	var loadResultBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[3].Content, &loadResultBlocks))
	require.Len(t, loadResultBlocks, 1)
	require.Contains(t, loadResultBlocks[0].Content, `"loaded":true`)
	require.Contains(t, loadResultBlocks[0].Content, `"name":"jira__get_issue"`)
}

func TestDynamicMCPMetaToolsBypassApproval(t *testing.T) {
	store := llm.NewNoTools()
	store.AddTools([]llm.Tool{
		{Name: mcp.SearchToolsName, Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "{}", nil }},
		{Name: mcp.LoadToolName, Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "{}", nil }},
		{Name: "jira__transition_issue", ServerOrigin: "https://jira.example.com", Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) { return "ok", nil }},
	})
	shouldExecute := (&Conversations{}).shouldAutoExecuteTool(&llm.Context{Tools: store}, true)

	require.True(t, shouldExecute(llm.ToolCall{Name: mcp.SearchToolsName}))
	require.True(t, shouldExecute(llm.ToolCall{Name: mcp.LoadToolName}))
	require.False(t, shouldExecute(llm.ToolCall{Name: "jira__transition_issue"}))
}
