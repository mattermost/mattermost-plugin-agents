// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	serviceAccountRemoteOrigin = "https://jira.example.com"
	serviceAccountBotUserID    = "bot-user-id"
)

// serviceAccountTestBot returns an agent without dynamic MCP tool loading, so provided tools resolve immediately.
func serviceAccountTestBot(useServiceAccount bool) *bots.Bot {
	return newServiceAccountTestBot(func(cfg *llm.BotConfig) { cfg.UseServiceAccountAuth = useServiceAccount }, &loadedStateLLM{})
}

func newServiceAccountTestBot(configure func(*llm.BotConfig), lm llm.LanguageModel) *bots.Bot {
	cfg := llm.BotConfig{
		ID:                    "bot-id",
		Name:                  "matty",
		DisplayName:           "Matty",
		AutoEnableNewMCPTools: true,
		UserAccessLevel:       llm.UserAccessLevelAll,
		ChannelAccessLevel:    llm.ChannelAccessLevelAll,
	}
	configure(&cfg)
	return bots.NewBot(
		cfg,
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: serviceAccountBotUserID, Username: "matty", DisplayName: "Matty"},
		lm,
	)
}

// The human initiator approves, but execution resolves against the re-derived
// SA catalog, with embedded/plugin tools connected as the initiator unless the
// agent uses its bot account permissions.
func TestHandleToolCallExecutesFromServiceAccountCatalog(t *testing.T) {
	tests := []struct {
		name           string
		botPermissions bool
		wantLocalActor string
	}{
		{name: "embedded and plugin tools run as the initiator"},
		{name: "bot permissions run embedded and plugin tools as the agent bot", botPermissions: true, wantLocalActor: serviceAccountBotUserID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executed := 0
			saTool := channelFollowUpTestMCPTool("sa_jira__get_issue", serviceAccountRemoteOrigin, "service account Jira")
			saTool.Resolver = func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				executed++
				return "mcp:sa_jira__get_issue", nil
			}
			provider := &countingMCPToolProvider{
				// A different Jira tool, so the assertions cannot pass by resolving the wrong catalog.
				tools:   []llm.Tool{channelFollowUpTestMCPTool("jira__get_issue", serviceAccountRemoteOrigin, "user OAuth Jira")},
				saTools: []llm.Tool{saTool},
			}

			convStore := newLoadedStateFlowStore()
			conv := &store.Conversation{
				ID:           "conv-id",
				UserID:       "user-id",
				BotID:        serviceAccountBotUserID,
				SystemPrompt: "system",
				Operation:    "conversation",
			}
			require.NoError(t, convStore.CreateConversation(conv))

			blocks := []conversation.ContentBlock{{
				Type:         conversation.BlockTypeToolUse,
				ID:           "tool-use-1",
				Name:         "sa_jira__get_issue",
				ServerOrigin: serviceAccountRemoteOrigin,
				Input:        json.RawMessage(`{}`),
				Status:       conversation.StatusPending,
			}}
			content, err := json.Marshal(blocks)
			require.NoError(t, err)
			approvalPostID := "approval-post-id"
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &approvalPostID,
				Role:           "assistant",
				Content:        content,
				Sequence:       1,
			}))

			bot := newServiceAccountTestBot(func(cfg *llm.BotConfig) {
				cfg.UseServiceAccountAuth = true
				cfg.ExperimentalUseBotPermissions = tt.botPermissions
			}, &loadedStateLLM{})
			c := serviceAccountConversations(t, convStore, provider, bot)

			approvalPost := &model.Post{Id: approvalPostID, UserId: serviceAccountBotUserID}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
			channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

			require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, channel, []string{"tool-use-1"}, nil))

			require.Equal(t, []string{serviceAccountBotUserID}, provider.SAIdentities(),
				"the approval resume must re-derive the catalog for the agent bot identity")
			require.Equal(t, []string{"user-id"}, provider.SAInvokers(),
				"access policy on the SA catalog is always evaluated for the initiator")
			require.Equal(t, []string{tt.wantLocalActor}, provider.SALocalActors())
			require.Equal(t, 0, provider.Calls(), "service account agents never build the per-user remotes catalog")
			require.Equal(t, 1, executed, "the service account resolver must run exactly once")

			turns, err := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, err)
			require.Len(t, turns, 2)

			var updatedBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[0].Content, &updatedBlocks))
			require.Equal(t, conversation.StatusSuccess, updatedBlocks[0].Status)

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
			require.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
			require.Equal(t, "mcp:sa_jira__get_issue", resultBlocks[0].Content)
		})
	}
}

// A DM tool call on an "ask" tool runs through the real tool loop without
// stopping for approval only when the service account agent bypasses approvals.
func TestProcessDMRequestServiceAccountBypassToolApproval(t *testing.T) {
	const toolName = "jira__get_issue"

	tests := []struct {
		name     string
		bypass   bool
		wantRuns int
		wantText string
	}{
		{name: "ask tool waits for approval"},
		{name: "bypass runs the ask tool", bypass: true, wantRuns: 1, wantText: "issue fetched"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs := 0
			tool := channelFollowUpTestMCPTool(toolName, serviceAccountRemoteOrigin, "Jira")
			tool.Resolver = func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
				runs++
				return "JIRA-1 details", nil
			}
			provider := &countingMCPToolProvider{saTools: []llm.Tool{tool}}
			builder := newSingleBuildLLMContextBuilder(t, provider)
			lm := &toolThenTextLLM{call: llm.ToolCall{ID: "call-1", Name: toolName, ServerOrigin: serviceAccountRemoteOrigin, Arguments: json.RawMessage(`{}`)}, text: "issue fetched"}
			bot := newServiceAccountTestBot(func(cfg *llm.BotConfig) {
				cfg.UseServiceAccountAuth = true
				cfg.ExperimentalBypassToolApproval = tt.bypass
			}, lm)
			llmContext := builder.BuildLLMContextUserRequest(
				bot,
				&model.User{Id: "user-id", Username: "user", Locale: "en"},
				&model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"},
				builder.WithLLMContextTools(context.Background(), bot),
			)

			convStore, conv := loadedStateConversationStore()
			c := &Conversations{
				convService: conversation.NewService(convStore, nil, nil, nil),
				toolPolicyChecker: mapPolicyChecker{
					serviceAccountRemoteOrigin: {"get_issue": {policy: mcp.ToolPolicyAsk, enabled: true}},
				},
			}

			result, err := c.ProcessDMRequest(context.Background(), conv.ID, lm, llmContext, 0)
			require.NoError(t, err)
			text := ""
			for event := range result.Stream.Stream {
				if event.Type == llm.EventTypeText {
					text += event.Value.(string)
				}
			}

			require.Equal(t, tt.wantRuns, runs)
			require.Equal(t, tt.wantText, text)
			turns, err := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, err)
			if !tt.bypass {
				require.Empty(t, turns, "a call awaiting approval is not executed or persisted as a tool round")
				return
			}
			require.Len(t, turns, 2)
			var toolUse, toolResult []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[0].Content, &toolUse))
			require.NoError(t, json.Unmarshal(turns[1].Content, &toolResult))
			require.Equal(t, toolName, toolUse[0].Name)
			require.Equal(t, conversation.StatusAutoApproved, toolUse[0].Status)
			require.Equal(t, "JIRA-1 details", toolResult[0].Content)
		})
	}
}

// toolThenTextLLM requests one tool call, then answers with text.
type toolThenTextLLM struct {
	call  llm.ToolCall
	text  string
	calls int
}

func (l *toolThenTextLLM) ChatCompletion(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	l.calls++
	if l.calls == 1 {
		return dynamicWorkflowStream(llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{l.call}}), nil
	}
	return dynamicWorkflowStream(llm.TextStreamEvent{Type: llm.EventTypeText, Value: l.text}), nil
}

func (l *toolThenTextLLM) ChatCompletionNoStream(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (string, error) {
	return l.text, nil
}

func (l *toolThenTextLLM) CountTokens(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}
func (l *toolThenTextLLM) InputTokenLimit() int  { return 100000 }
func (l *toolThenTextLLM) OutputTokenLimit() int { return 8192 }

// Tool calls on an "ask" tool need approval and a Share step unless a service
// account agent has the experimental bypass setting on.
func TestServiceAccountBypassToolApproval(t *testing.T) {
	const toolName = "jira__get_issue"

	tests := []struct {
		name           string
		serviceAccount bool
		bypass         bool
		wantAutoRun    bool
	}{
		{name: "service account agent still asks", serviceAccount: true},
		{name: "bypass setting auto-runs and shares", serviceAccount: true, bypass: true, wantAutoRun: true},
		{name: "bypass setting is ignored without service account auth", bypass: true},
	}

	for _, tt := range tests {
		for _, isDM := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s (DM=%v)", tt.name, isDM), func(t *testing.T) {
				tool := channelFollowUpTestMCPTool(toolName, serviceAccountRemoteOrigin, "Jira")
				provider := &countingMCPToolProvider{tools: []llm.Tool{tool}, saTools: []llm.Tool{tool}}
				c := &Conversations{
					contextBuilder: newSingleBuildLLMContextBuilder(t, provider),
					toolPolicyChecker: mapPolicyChecker{
						serviceAccountRemoteOrigin: {"get_issue": {policy: mcp.ToolPolicyAsk, enabled: true}},
					},
				}
				bot := newServiceAccountTestBot(func(cfg *llm.BotConfig) {
					cfg.UseServiceAccountAuth = tt.serviceAccount
					cfg.ExperimentalBypassToolApproval = tt.bypass
				}, &loadedStateLLM{})
				user := &model.User{Id: "user-id", Username: "user"}
				channel := &model.Channel{Id: "channel-id", Type: model.ChannelTypeOpen}
				if isDM {
					channel.Type = model.ChannelTypeDirect
				}

				llmCtx := c.buildConversationContextWithTools(context.Background(), bot, user, channel, "")

				call := llm.ToolCall{Name: toolName, ServerOrigin: serviceAccountRemoteOrigin}
				require.Equal(t, tt.wantAutoRun, c.shouldAutoExecuteTool(llmCtx, isDM)(call))
				turns := []toolrunner.ToolTurn{{AssistantToolCalls: []llm.ToolCall{call}}}
				require.Equal(t, tt.wantAutoRun, c.allToolsAutoRunEverywhere(turns, llmCtx))
			})
		}
	}
}

func serviceAccountConversations(t *testing.T, convStore *loadedStateFlowStore, provider llmcontext.MCPToolProvider, bot *bots.Bot) *Conversations {
	t.Helper()

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := toolLicenseChecker(t, true)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{bot})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", "user-id").Return(&model.User{Id: "user-id", Username: "user"}, nil).Maybe()

	return &Conversations{
		mmClient:       mmClient,
		contextBuilder: newSingleBuildLLMContextBuilder(t, provider),
		bots:           botsService,
		licenseChecker: licenseChecker,
		convService:    conversation.NewService(convStore, nil, nil, nil),
	}
}
