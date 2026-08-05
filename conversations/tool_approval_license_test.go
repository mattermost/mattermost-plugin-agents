// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const toolLicenseRemoteOrigin = "https://jira.example.com"

// mockLicenseState registers GetConfig/GetLicense expectations reflecting the
// requested license state (enterprise-licensed or unlicensed).
func mockLicenseState(mockAPI *plugintest.API, licensed bool) {
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	if licensed {
		mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterprise}).Maybe()
	} else {
		mockAPI.On("GetLicense").Return((*model.License)(nil)).Maybe()
	}
}

// toolLicenseChecker returns a LicenseChecker whose IsBasicsLicensed reports
// the requested state.
func toolLicenseChecker(t *testing.T, licensed bool) *enterprise.LicenseChecker {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockLicenseState(mockAPI, licensed)
	return enterprise.NewLicenseChecker(pluginapi.NewClient(mockAPI, nil))
}

// toolLicenseFlowStore is an in-memory conversation.Store implementation for
// exercising tool approval flows without a database.
type toolLicenseFlowStore struct {
	mu            sync.Mutex
	conversations map[string]*store.Conversation
	turns         map[string][]store.Turn
	allTurns      map[string]*store.Turn
}

func newToolLicenseFlowStore() *toolLicenseFlowStore {
	return &toolLicenseFlowStore{
		conversations: make(map[string]*store.Conversation),
		turns:         make(map[string][]store.Turn),
		allTurns:      make(map[string]*store.Turn),
	}
}

func (s *toolLicenseFlowStore) CreateConversation(conv *store.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *conv
	s.conversations[conv.ID] = &cp
	return nil
}

func (s *toolLicenseFlowStore) GetConversation(id string) (*store.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok {
		return nil, store.ErrConversationNotFound
	}
	cp := *conv
	return &cp, nil
}

func (s *toolLicenseFlowStore) GetConversationByThreadBotUser(rootPostID, botID, userID string) (*store.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conv := range s.conversations {
		if conv.RootPostID != nil && *conv.RootPostID == rootPostID && conv.BotID == botID && conv.UserID == userID {
			cp := *conv
			return &cp, nil
		}
	}
	return nil, store.ErrConversationNotFound
}

func (s *toolLicenseFlowStore) UpdateConversationTitle(id, title string) error {
	return nil
}

func (s *toolLicenseFlowStore) UpdateConversationRootPostID(id string, rootPostID string) error {
	return nil
}

func (s *toolLicenseFlowStore) CreateTurn(turn *store.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *turn
	s.turns[turn.ConversationID] = append(s.turns[turn.ConversationID], cp)
	s.allTurns[turn.ID] = &cp
	return nil
}

func (s *toolLicenseFlowStore) CreateTurnAutoSequence(turn *store.Turn) error {
	maxSeq, err := s.GetMaxSequenceForConversation(turn.ConversationID)
	if err != nil {
		return err
	}
	turn.Sequence = maxSeq + 1
	return s.CreateTurn(turn)
}

func (s *toolLicenseFlowStore) GetTurnsForConversation(conversationID string) ([]store.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.turns[conversationID]
	result := make([]store.Turn, len(turns))
	copy(result, turns)
	return result, nil
}

func (s *toolLicenseFlowStore) UpdateTurnContent(id string, content json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.allTurns[id]
	if !ok {
		return fmt.Errorf("turn %s not found", id)
	}
	turn.Content = content
	for convID, turns := range s.turns {
		for i := range turns {
			if turns[i].ID == id {
				s.turns[convID][i].Content = content
			}
		}
	}
	return nil
}

func (s *toolLicenseFlowStore) UpdateTurnTokens(id string, tokensIn, tokensOut int64) error {
	return nil
}

func (s *toolLicenseFlowStore) GetMaxSequenceForConversation(conversationID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	maxSeq := 0
	for _, turn := range s.turns[conversationID] {
		if turn.Sequence > maxSeq {
			maxSeq = turn.Sequence
		}
	}
	return maxSeq, nil
}

// toolLicenseConversationStore returns a store seeded with a single
// conversation owned by user-id and bot-id.
func toolLicenseConversationStore() (*toolLicenseFlowStore, *store.Conversation) {
	convStore := newToolLicenseFlowStore()
	conv := &store.Conversation{
		ID:           "conv-id",
		UserID:       "user-id",
		BotID:        "bot-id",
		SystemPrompt: "system",
		Operation:    "conversation",
	}
	_ = convStore.CreateConversation(conv)
	return convStore, conv
}

type toolLicenseBuiltinProvider struct {
	tools []llm.Tool
}

func (p *toolLicenseBuiltinProvider) GetTools(*bots.Bot) []llm.Tool {
	return p.tools
}

type toolLicenseMCPToolProvider struct {
	tools []llm.Tool
}

func (p *toolLicenseMCPToolProvider) GetToolsForUser(string) ([]llm.Tool, *mcp.Errors) {
	return p.tools, nil
}

type toolLicenseTestConfig struct{}

func (c *toolLicenseTestConfig) GetEnableLLMTrace() bool {
	return false
}

func (c *toolLicenseTestConfig) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

func toolLicenseTestMCPTool(name, origin, description string) llm.Tool {
	return llm.Tool{
		Name:         name,
		Description:  description,
		ServerOrigin: origin,
		Schema:       llm.NewJSONSchemaFromStruct[struct{}](),
		Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "mcp:" + name, nil
		},
	}
}

// toolLicenseTestBot returns a bot whose every provided tool is immediately
// resolvable in the visible tool store.
func toolLicenseTestBot() *bots.Bot {
	return bots.NewBot(
		llm.BotConfig{
			ID:                    "bot-id",
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
			UserAccessLevel:       llm.UserAccessLevelAll,
			ChannelAccessLevel:    llm.ChannelAccessLevelAll,
		},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: "bot-id", Username: "matty", DisplayName: "Matty"},
		nil,
	)
}

// toolLicenseTestBuilder builds a context builder whose license state matches
// the scenario under test: unlicensed builders drop remote MCP tools at
// supply time, mirroring production.
func toolLicenseTestBuilder(t *testing.T, licensed bool) *llmcontext.Builder {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockLicenseState(mockAPI, licensed)
	mockAPI.On("GetTeam", "team-id").Return(&model.Team{Id: "team-id", Name: "team"}, nil).Maybe()
	for i := 1; i <= 10; i++ {
		args := make([]interface{}, i)
		for j := range args {
			args[j] = mock.Anything
		}
		mockAPI.On("LogDebug", args...).Maybe()
		mockAPI.On("LogInfo", args...).Maybe()
		mockAPI.On("LogWarn", args...).Maybe()
		mockAPI.On("LogError", args...).Maybe()
	}

	builtinTools := []llm.Tool{toolLicenseTestMCPTool("builtin_tool", "", "built-in tool")}
	mcpTools := []llm.Tool{
		toolLicenseTestMCPTool("mattermost__read_channel", mcp.EmbeddedClientKey, "read channel posts"),
		toolLicenseTestMCPTool("jira__get_issue", toolLicenseRemoteOrigin, "fetch Jira issue"),
	}

	return llmcontext.NewLLMContextBuilder(
		pluginapi.NewClient(mockAPI, nil),
		&toolLicenseBuiltinProvider{tools: builtinTools},
		&toolLicenseMCPToolProvider{tools: mcpTools},
		&toolLicenseTestConfig{},
	)
}

func toolLicenseConversations(t *testing.T, convStore *toolLicenseFlowStore, licensed bool) *Conversations {
	t.Helper()

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := toolLicenseChecker(t, licensed)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{toolLicenseTestBot()})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", "user-id").Return(&model.User{Id: "user-id", Username: "user"}, nil).Maybe()

	return &Conversations{
		mmClient:       mmClient,
		contextBuilder: toolLicenseTestBuilder(t, licensed),
		bots:           botsService,
		licenseChecker: licenseChecker,
		convService:    conversation.NewService(convStore, nil, nil, nil),
	}
}

// TestHandleToolCallLicenseGate pins the license split for tool approvals:
// built-in tools (empty ServerOrigin) and embedded Mattermost MCP tools
// (mcp.EmbeddedClientKey) never require a license, while tools from remote
// MCP servers require one to execute. Rejections never require a license.
func TestHandleToolCallLicenseGate(t *testing.T) {
	tests := []struct {
		name       string
		toolName   string
		origin     string
		licensed   bool
		accept     bool
		wantErr    error
		wantStatus string
		wantResult string
	}{
		{
			name:       "embedded MCP tool executes without license",
			toolName:   "mattermost__read_channel",
			origin:     mcp.EmbeddedClientKey,
			licensed:   false,
			accept:     true,
			wantStatus: conversation.StatusSuccess,
			wantResult: "mcp:mattermost__read_channel",
		},
		{
			name:       "built-in tool executes without license",
			toolName:   "builtin_tool",
			origin:     "",
			licensed:   false,
			accept:     true,
			wantStatus: conversation.StatusSuccess,
			wantResult: "mcp:builtin_tool",
		},
		{
			name:     "remote MCP tool approval is rejected without license",
			toolName: "jira__get_issue",
			origin:   toolLicenseRemoteOrigin,
			licensed: false,
			accept:   true,
			wantErr:  ErrRemoteMCPNotLicensed,
		},
		{
			name:       "remote MCP tool executes with license",
			toolName:   "jira__get_issue",
			origin:     toolLicenseRemoteOrigin,
			licensed:   true,
			accept:     true,
			wantStatus: conversation.StatusSuccess,
			wantResult: "mcp:jira__get_issue",
		},
		{
			name:       "remote MCP tool rejection is allowed without license",
			toolName:   "jira__get_issue",
			origin:     toolLicenseRemoteOrigin,
			licensed:   false,
			accept:     false,
			wantStatus: conversation.StatusRejected,
			wantResult: "Tool call rejected by user",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := toolLicenseConversationStore()

			blocks := []conversation.ContentBlock{{
				Type:         conversation.BlockTypeToolUse,
				ID:           "tool-use-1",
				Name:         tc.toolName,
				ServerOrigin: tc.origin,
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

			c := toolLicenseConversations(t, convStore, tc.licensed)

			approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
			channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

			var acceptedIDs []string
			if tc.accept {
				acceptedIDs = []string{"tool-use-1"}
			}

			err = c.HandleToolCall("user-id", approvalPost, channel, acceptedIDs)

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				// The pending decision must be left untouched so the user
				// can act again once the server is licensed.
				require.Len(t, turns, 1)
				var untouched []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(turns[0].Content, &untouched))
				require.Equal(t, conversation.StatusPending, untouched[0].Status)
				return
			}

			require.NoError(t, err)
			require.Len(t, turns, 2)

			var updatedBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[0].Content, &updatedBlocks))
			require.Equal(t, tc.wantStatus, updatedBlocks[0].Status)

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
			require.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
			require.Equal(t, tc.wantResult, resultBlocks[0].Content)
		})
	}
}

// TestHandleToolResultLicenseGate pins the license split for the second-stage
// share/keep-private decision: sharing output from a remote MCP tool requires
// a license, while embedded Mattermost MCP output and keep-private decisions
// do not.
func TestHandleToolResultLicenseGate(t *testing.T) {
	tests := []struct {
		name       string
		toolName   string
		origin     string
		licensed   bool
		accept     bool
		wantErr    error
		wantShared bool
	}{
		{
			name:       "embedded MCP result shares without license",
			toolName:   "mattermost__read_channel",
			origin:     mcp.EmbeddedClientKey,
			licensed:   false,
			accept:     true,
			wantShared: true,
		},
		{
			name:     "remote MCP result share is rejected without license",
			toolName: "jira__get_issue",
			origin:   toolLicenseRemoteOrigin,
			licensed: false,
			accept:   true,
			wantErr:  ErrRemoteMCPNotLicensed,
		},
		{
			name:       "remote MCP result shares with license",
			toolName:   "jira__get_issue",
			origin:     toolLicenseRemoteOrigin,
			licensed:   true,
			accept:     true,
			wantShared: true,
		},
		{
			name:       "remote MCP result keep-private is allowed without license",
			toolName:   "jira__get_issue",
			origin:     toolLicenseRemoteOrigin,
			licensed:   false,
			accept:     false,
			wantShared: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := toolLicenseConversationStore()

			resultPostID := "result-post-id"
			assistantBlocks := []conversation.ContentBlock{{
				Type:         conversation.BlockTypeToolUse,
				ID:           "tool-use-1",
				Name:         tc.toolName,
				ServerOrigin: tc.origin,
				Input:        json.RawMessage(`{}`),
				Status:       conversation.StatusSuccess,
			}}
			assistantContent, err := json.Marshal(assistantBlocks)
			require.NoError(t, err)
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &resultPostID,
				Role:           "assistant",
				Content:        assistantContent,
				Sequence:       1,
			}))

			resultBlocks := []conversation.ContentBlock{{
				Type:      conversation.BlockTypeToolResult,
				ToolUseID: "tool-use-1",
				Content:   "tool output",
				Status:    conversation.StatusSuccess,
			}}
			resultContent, err := json.Marshal(resultBlocks)
			require.NoError(t, err)
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "result-turn",
				ConversationID: conv.ID,
				Role:           "tool_result",
				Content:        resultContent,
				Sequence:       2,
			}))

			c := toolLicenseConversations(t, convStore, tc.licensed)

			resultPost := &model.Post{Id: resultPostID, UserId: "bot-id"}
			resultPost.AddProp(streaming.ConversationIDProp, conv.ID)
			// DM channel: HandleToolCall streams DM follow-ups, so
			// HandleToolResult returns after recording the decision.
			channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

			var acceptedIDs []string
			if tc.accept {
				acceptedIDs = []string{"tool-use-1"}
			}

			err = c.HandleToolResult("user-id", resultPost, channel, acceptedIDs)

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			require.Len(t, turns, 2)
			var updatedResults []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[1].Content, &updatedResults))

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Nil(t, updatedResults[0].DecidedAt, "rejected submission must not record a decision")
				return
			}

			require.NoError(t, err)
			require.NotNil(t, updatedResults[0].DecidedAt, "decision must be recorded")
			if tc.wantShared {
				require.NotNil(t, updatedResults[0].Shared)
				require.True(t, *updatedResults[0].Shared)
			} else {
				require.True(t, updatedResults[0].Shared == nil || !*updatedResults[0].Shared)
			}
		})
	}
}
