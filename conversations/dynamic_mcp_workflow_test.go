// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

type dynamicWorkflowLLM struct {
	mu       sync.Mutex
	calls    int
	requests []llm.CompletionRequest
}

func (l *dynamicWorkflowLLM) ChatCompletion(_ context.Context, request llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
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

func (l *dynamicWorkflowLLM) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	result, err := l.ChatCompletion(ctx, request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (l *dynamicWorkflowLLM) CountTokens(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}
func (l *dynamicWorkflowLLM) InputTokenLimit() int  { return 100000 }
func (l *dynamicWorkflowLLM) OutputTokenLimit() int { return 8192 }

func dynamicWorkflowStream(events ...llm.TextStreamEvent) *llm.TextStreamResult {
	stream := make(chan llm.TextStreamEvent, len(events)+1)
	for _, event := range events {
		stream <- event
	}
	stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	close(stream)
	return &llm.TextStreamResult{Stream: stream}
}

func TestDynamicMCPStrictSearchLoadCallDerivesLoadedTools(t *testing.T) {
	const origin = "https://jira.example.com"

	tests := []struct {
		name                      string
		policy                    string
		expectedText              string
		expectedResolverCalls     int
		expectedBusinessToolCall  bool
		expectedLLMRequests       int
		expectedTurns             int
		expectedBusinessToolEvent bool
	}{
		{
			name:                      "auto-run policy executes loaded tool",
			policy:                    mcp.ToolPolicyAutoRunInDM,
			expectedText:              "JIRA-1 details returned",
			expectedResolverCalls:     1,
			expectedBusinessToolCall:  true,
			expectedLLMRequests:       4,
			expectedTurns:             6,
			expectedBusinessToolEvent: true,
		},
		{
			name:                     "ask policy leaves loaded tool pending approval",
			policy:                   mcp.ToolPolicyAsk,
			expectedResolverCalls:    0,
			expectedBusinessToolCall: true,
			expectedLLMRequests:      3,
			expectedTurns:            4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			resolverCalls := 0
			jiraTool := llm.Tool{
				Name:         "jira__get_issue",
				Description:  "fetch Jira issue details",
				ServerOrigin: origin,
				Schema: llm.NewJSONSchemaFromStruct[struct {
					Key string `json:"key"`
				}](),
				Resolver: func(_ context.Context, _ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
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
			lm := &dynamicWorkflowLLM{}
			bot := loadedStateBot(lm)
			llmContext := builder.BuildLLMContextUserRequest(
				bot,
				&model.User{Id: "user-id", Username: "user", Locale: "en"},
				&model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"},
				builder.WithLLMContextDefaultTools(context.Background(), bot),
			)
			c := &Conversations{
				convService: conversation.NewService(convStore, nil, nil, nil),
				toolPolicyChecker: mapPolicyChecker{
					origin: {
						"get_issue": {policy: tt.policy, enabled: true},
					},
				},
			}

			streamResult, err := c.ProcessDMRequest(context.Background(), conv.ID, lm, llmContext, 0)
			require.NoError(t, err)

			text := ""
			foundBusinessToolCall := false
			for event := range streamResult.Stream.Stream {
				switch event.Type {
				case llm.EventTypeText:
					value, ok := event.Value.(string)
					require.True(t, ok)
					text += value
				case llm.EventTypeToolCalls:
					toolCalls, ok := event.Value.([]llm.ToolCall)
					require.True(t, ok)
					for _, toolCall := range toolCalls {
						if toolCall.Name == "jira__get_issue" {
							foundBusinessToolCall = true
						}
					}
				}
			}

			require.Equal(t, tt.expectedText, text)
			require.Equal(t, tt.expectedBusinessToolCall, foundBusinessToolCall)
			require.Equal(t, tt.expectedResolverCalls, resolverCalls)

			require.Len(t, lm.requests, tt.expectedLLMRequests)
			require.NotNil(t, lm.requests[2].Context.Tools.GetTool("jira__get_issue"), "load_tool must materialize the schema before the business call")

			turns, err := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, err)
			require.Len(t, turns, tt.expectedTurns)

			var loadResultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[3].Content, &loadResultBlocks))
			require.Len(t, loadResultBlocks, 1)
			require.Contains(t, loadResultBlocks[0].Content, `"loaded":true`)
			require.Contains(t, loadResultBlocks[0].Content, `"name":"jira__get_issue"`)

			var loadPayload mcp.LoadToolResult
			require.NoError(t, json.Unmarshal([]byte(loadResultBlocks[0].Content), &loadPayload))
			require.True(t, loadPayload.Loaded)
			require.Equal(t, "jira__get_issue", loadPayload.Name)

			if tt.expectedBusinessToolEvent {
				var searchBlocks []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(turns[0].Content, &searchBlocks))
				require.Equal(t, mcp.SearchToolsName, searchBlocks[0].Name)
				require.Equal(t, conversation.StatusAutoApproved, searchBlocks[0].Status)

				var businessResultBlocks []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(turns[5].Content, &businessResultBlocks))
				require.Equal(t, "JIRA-1 details", businessResultBlocks[0].Content)
				require.Equal(t, conversation.StatusSuccess, businessResultBlocks[0].Status)

				derived := conversation.DeriveLoadedMCPTools(turns)
				require.Equal(t, []string{"jira__get_issue"}, derived)
			}
		})
	}
}

func TestDynamicMCPMetaToolsBypassApproval(t *testing.T) {
	store := llm.NewNoTools()
	store.AddTools([]llm.Tool{
		{Name: mcp.SearchToolsName, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "{}", nil }},
		{Name: mcp.LoadToolName, Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "{}", nil }},
		{Name: "jira__transition_issue", ServerOrigin: "https://jira.example.com", Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) { return "ok", nil }},
	})
	shouldExecute := (&Conversations{}).shouldAutoExecuteTool(&llm.Context{Tools: store}, true)

	cases := []struct {
		name           string
		toolCallName   string
		expectAutoExec bool
	}{
		{name: "search_tools meta-tool bypasses approval", toolCallName: mcp.SearchToolsName, expectAutoExec: true},
		{name: "load_tool meta-tool bypasses approval", toolCallName: mcp.LoadToolName, expectAutoExec: true},
		{name: "business tool requires approval", toolCallName: "jira__transition_issue", expectAutoExec: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expectAutoExec, shouldExecute(llm.ToolCall{Name: tc.toolCallName}))
		})
	}
}
