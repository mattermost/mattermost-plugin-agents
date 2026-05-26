// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
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
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type loadedStateFlowStore struct {
	mu            sync.Mutex
	conversations map[string]*store.Conversation
	turns         map[string][]store.Turn
	allTurns      map[string]*store.Turn
}

func newLoadedStateFlowStore() *loadedStateFlowStore {
	return &loadedStateFlowStore{
		conversations: make(map[string]*store.Conversation),
		turns:         make(map[string][]store.Turn),
		allTurns:      make(map[string]*store.Turn),
	}
}

func (s *loadedStateFlowStore) CreateConversation(conv *store.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *conv
	s.conversations[conv.ID] = &cp
	return nil
}

func (s *loadedStateFlowStore) GetConversation(id string) (*store.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok {
		return nil, store.ErrConversationNotFound
	}
	cp := *conv
	return &cp, nil
}

func (s *loadedStateFlowStore) GetConversationByThreadBotUser(rootPostID, botID, userID string) (*store.Conversation, error) {
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

func (s *loadedStateFlowStore) UpdateConversationTitle(id, title string) error {
	return nil
}

func (s *loadedStateFlowStore) UpdateConversationRootPostID(id string, rootPostID string) error {
	return nil
}

func (s *loadedStateFlowStore) CreateTurn(turn *store.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *turn
	s.turns[turn.ConversationID] = append(s.turns[turn.ConversationID], cp)
	s.allTurns[turn.ID] = &cp
	return nil
}

func (s *loadedStateFlowStore) CreateTurnAutoSequence(turn *store.Turn) error {
	maxSeq, err := s.GetMaxSequenceForConversation(turn.ConversationID)
	if err != nil {
		return err
	}
	turn.Sequence = maxSeq + 1
	return s.CreateTurn(turn)
}

func (s *loadedStateFlowStore) GetTurnsForConversation(conversationID string) ([]store.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.turns[conversationID]
	result := make([]store.Turn, len(turns))
	copy(result, turns)
	return result, nil
}

func (s *loadedStateFlowStore) GetTurnByPostID(postID string) (*store.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.allTurns {
		if t.PostID != nil && *t.PostID == postID {
			c := *t
			return &c, nil
		}
	}
	return nil, nil
}

func (s *loadedStateFlowStore) UpdateTurnContent(id string, content json.RawMessage) error {
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

func (s *loadedStateFlowStore) UpdateTurnPostID(id string, postID *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.allTurns[id]
	if !ok {
		return fmt.Errorf("turn %s not found", id)
	}
	turn.PostID = postID
	for convID, turns := range s.turns {
		for i := range turns {
			if turns[i].ID == id {
				s.turns[convID][i].PostID = postID
			}
		}
	}
	return nil
}

func (s *loadedStateFlowStore) DeleteResponseTurns(conversationID, postID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.turns[conversationID]

	anchorSeq := -1
	for _, t := range turns {
		if t.Role == "assistant" && t.PostID != nil && *t.PostID == postID {
			anchorSeq = t.Sequence
			break
		}
	}
	if anchorSeq < 0 {
		return nil
	}
	userSeq := 0
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Role == "user" && turns[i].Sequence < anchorSeq {
			userSeq = turns[i].Sequence
			break
		}
	}

	keep := turns[:0]
	for _, t := range turns {
		if t.Sequence > userSeq && t.Sequence < anchorSeq {
			delete(s.allTurns, t.ID)
			continue
		}
		keep = append(keep, t)
	}
	s.turns[conversationID] = keep
	return nil
}

func (s *loadedStateFlowStore) UpdateTurnTokens(id string, tokensIn, tokensOut int64) error {
	return nil
}

func (s *loadedStateFlowStore) GetMaxSequenceForConversation(conversationID string) (int, error) {
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

type loadedStateStore struct {
	rows []store.LoadedMCPTool
}

func (s *loadedStateStore) UpsertLoadedMCPTool(tool store.LoadedMCPTool) error {
	s.rows = append(s.rows, tool)
	return nil
}

func (s *loadedStateStore) ListLoadedMCPTools(conversationID, botID, userID string) ([]store.LoadedMCPTool, error) {
	var result []store.LoadedMCPTool
	for _, row := range s.rows {
		if row.ConversationID == conversationID && row.BotID == botID && row.UserID == userID {
			result = append(result, row)
		}
	}
	return result, nil
}

func (s *loadedStateStore) DeleteLoadedMCPTool(conversationID, botID, userID, toolName string) error {
	return nil
}

func (s *loadedStateStore) DeleteLoadedMCPToolsByNames(conversationID, botID, userID string, toolNames []string) error {
	return nil
}

type loadedStateLLM struct {
	mu       sync.Mutex
	requests []llm.CompletionRequest
}

func (l *loadedStateLLM) ChatCompletion(_ context.Context, request llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	l.mu.Lock()
	l.requests = append(l.requests, request)
	l.mu.Unlock()
	return llm.NewStreamFromString("done"), nil
}

func (l *loadedStateLLM) ChatCompletionNoStream(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (string, error) {
	return "title", nil
}

func (l *loadedStateLLM) CountTokens(string) int { return 0 }
func (l *loadedStateLLM) InputTokenLimit() int   { return 100000 }

type loadedStateStreamingService struct {
	wg sync.WaitGroup
}

func (s *loadedStateStreamingService) StreamToNewPost(context.Context, string, string, *llm.TextStreamResult, *model.Post, string) error {
	return nil
}

func (s *loadedStateStreamingService) StreamToNewDM(context.Context, string, *llm.TextStreamResult, string, *model.Post, string) error {
	return nil
}

func (s *loadedStateStreamingService) StreamToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
}

func (s *loadedStateStreamingService) StreamContinuationToPost(context.Context, *llm.TextStreamResult, *model.Post, string, string) {
	s.wg.Done()
}

func (s *loadedStateStreamingService) StopStreaming(string) {}

func (s *loadedStateStreamingService) GetStreamingContext(ctx context.Context, postID string) (context.Context, error) {
	s.wg.Add(1)
	return ctx, nil
}

func (s *loadedStateStreamingService) FinishStreaming(string) {}

func (s *loadedStateStreamingService) waitForStreaming() {
	s.wg.Wait()
}

func loadedStateBot(lm llm.LanguageModel) *bots.Bot {
	return bots.NewBot(
		llm.BotConfig{
			ID:                    "bot-id",
			Name:                  "matty",
			DisplayName:           "Matty",
			AutoEnableNewMCPTools: true,
			MCPDynamicToolLoading: true,
			UserAccessLevel:       llm.UserAccessLevelAll,
			ChannelAccessLevel:    llm.ChannelAccessLevelAll,
		},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: "bot-id", Username: "matty", DisplayName: "Matty"},
		lm,
	)
}

func loadedStateTool() llm.Tool {
	return llm.Tool{
		Name:         "jira__get_issue",
		Description:  "fetch Jira issue details",
		ServerOrigin: "https://jira.example.com",
		Schema:       map[string]any{"type": "object"},
		Resolver: func(*llm.Context, llm.ToolArgumentGetter) (string, error) {
			return "restored-result", nil
		},
	}
}

func loadedStateBuilder(t *testing.T, loadedStore llmcontext.LoadedMCPToolStore) *llmcontext.Builder {
	t.Helper()

	config := &channelFollowUpTestConfig{}
	builder := newChannelFollowUpTestBuilder(t, []llm.Tool{loadedStateTool()}, config)
	builder.SetLoadedMCPToolStore(loadedStore)
	return builder
}

func loadedStateConversationStore() (*loadedStateFlowStore, *store.Conversation) {
	convStore := newLoadedStateFlowStore()
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

func TestProcessDMRequestThreadsConversationIDIntoContext(t *testing.T) {
	convStore, _ := loadedStateConversationStore()
	convService := conversation.NewService(convStore, nil, nil, nil)
	lm := &loadedStateLLM{}
	c := &Conversations{convService: convService}
	llmContext := &llm.Context{Tools: llm.NewNoTools()}

	streamResult, err := c.ProcessDMRequest(context.Background(), "conv-id", lm, llmContext)
	require.NoError(t, err)
	_, readErr := streamResult.Stream.ReadAll()
	require.NoError(t, readErr)

	require.Equal(t, "conv-id", llmContext.ConversationID)
	require.Len(t, lm.requests, 1)
	require.Equal(t, "conv-id", lm.requests[0].Context.ConversationID)
}

func TestHandleToolCallRestoresLoadedToolForApprovalExecution(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	blocks := []conversation.ContentBlock{
		{
			Type:   conversation.BlockTypeToolUse,
			ID:     "tool-use-1",
			Name:   "jira__get_issue",
			Input:  json.RawMessage(`{}`),
			Status: conversation.StatusPending,
		},
	}
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

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	bot := loadedStateBot(&loadedStateLLM{})
	botsService.SetBotsForTesting([]*bots.Bot{bot})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", "user-id").Return(&model.User{Id: "user-id", Username: "user"}, nil).Once()

	loadedStore := &loadedStateStore{rows: []store.LoadedMCPTool{
		{ConversationID: conv.ID, BotID: "bot-id", UserID: "user-id", ToolName: "jira__get_issue"},
	}}
	c := &Conversations{
		mmClient:       mmClient,
		contextBuilder: loadedStateBuilder(t, loadedStore),
		bots:           botsService,
		convService:    conversation.NewService(convStore, nil, nil, nil),
	}

	approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
	approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, channel, []string{"tool-use-1"}))

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	var updatedBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[0].Content, &updatedBlocks))
	require.Equal(t, conversation.StatusSuccess, updatedBlocks[0].Status)

	var resultBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
	require.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
	require.Equal(t, "restored-result", resultBlocks[0].Content)
}

func TestHandleToolCallFailsSafelyWhenLoadedToolRevoked(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	blocks := []conversation.ContentBlock{
		{
			Type:         conversation.BlockTypeToolUse,
			ID:           "tool-use-1",
			Name:         "jira__get_issue",
			ServerOrigin: "https://jira.example.com",
			Input:        json.RawMessage(`{}`),
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			MCPBareName:  "get_issue",
			Status:       conversation.StatusPending,
		},
	}
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

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	bot := loadedStateBot(&loadedStateLLM{})
	botsService.SetBotsForTesting([]*bots.Bot{bot})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", "user-id").Return(&model.User{Id: "user-id", Username: "user"}, nil).Once()

	c := &Conversations{
		mmClient:       mmClient,
		contextBuilder: loadedStateBuilder(t, &loadedStateStore{}),
		bots:           botsService,
		convService:    conversation.NewService(convStore, nil, nil, nil),
	}

	approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
	approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, channel, []string{"tool-use-1"}))

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	var updatedBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[0].Content, &updatedBlocks))
	require.Equal(t, conversation.StatusError, updatedBlocks[0].Status)

	var resultBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
	require.Contains(t, resultBlocks[0].Content, "available but not loaded")
	require.Contains(t, resultBlocks[0].Content, "load_tool")
}

func TestHandleToolCallDoesNotUseNameOnlyMismatchedTool(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	blocks := []conversation.ContentBlock{
		{
			Type:         conversation.BlockTypeToolUse,
			ID:           "tool-use-1",
			Name:         "jira__get_issue",
			ServerOrigin: "https://different.example.com",
			Input:        json.RawMessage(`{}`),
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			MCPBareName:  "get_issue",
			Status:       conversation.StatusPending,
		},
	}
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

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	bot := loadedStateBot(&loadedStateLLM{})
	botsService.SetBotsForTesting([]*bots.Bot{bot})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", "user-id").Return(&model.User{Id: "user-id", Username: "user"}, nil).Once()

	loadedStore := &loadedStateStore{rows: []store.LoadedMCPTool{
		{ConversationID: conv.ID, BotID: "bot-id", UserID: "user-id", ToolName: "jira__get_issue"},
	}}
	c := &Conversations{
		mmClient:       mmClient,
		contextBuilder: loadedStateBuilder(t, loadedStore),
		bots:           botsService,
		convService:    conversation.NewService(convStore, nil, nil, nil),
	}

	approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
	approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

	require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, channel, []string{"tool-use-1"}))

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	var updatedBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[0].Content, &updatedBlocks))
	require.Equal(t, conversation.StatusError, updatedBlocks[0].Status)

	var resultBlocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
	require.Contains(t, resultBlocks[0].Content, "no longer matches the approved tool metadata")
}

func TestStreamToolFollowUpRestoresLoadedTool(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	loadedStore := &loadedStateStore{rows: []store.LoadedMCPTool{
		{ConversationID: conv.ID, BotID: "bot-id", UserID: "user-id", ToolName: "jira__get_issue"},
	}}
	lm := &loadedStateLLM{}
	bot := loadedStateBot(lm)
	streamingService := &loadedStateStreamingService{}
	c := &Conversations{
		contextBuilder:   loadedStateBuilder(t, loadedStore),
		convService:      conversation.NewService(convStore, nil, nil, nil),
		streamingService: streamingService,
	}

	err := c.streamToolFollowUp(
		context.Background(),
		bot,
		&model.User{Id: "user-id", Username: "user"},
		&model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"},
		&model.Post{Id: "root-post-id"},
		conv,
		true,
	)
	require.NoError(t, err)
	streamingService.waitForStreaming()
	require.Len(t, lm.requests, 1)
	require.NotNil(t, lm.requests[0].Context.Tools.GetTool("jira__get_issue"))
	require.Equal(t, "conv-id", lm.requests[0].Context.ConversationID)
}

func TestRegenerateViaConversationRestoresLoadedTool(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	loadedStore := &loadedStateStore{rows: []store.LoadedMCPTool{
		{ConversationID: conv.ID, BotID: "bot-id", UserID: "user-id", ToolName: "jira__get_issue"},
	}}
	lm := &loadedStateLLM{}
	bot := loadedStateBot(lm)
	c := &Conversations{
		contextBuilder: loadedStateBuilder(t, loadedStore),
		convService:    conversation.NewService(convStore, nil, nil, nil),
		configProvider: &channelFollowUpTestConfig{},
	}
	post := &model.Post{Id: "response-post-id"}
	post.AddProp(streaming.ConversationIDProp, conv.ID)

	streamResult, err := c.regenerateViaConversation(
		context.Background(),
		bot,
		&model.User{Id: "user-id", Username: "user"},
		&model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"},
		post,
		"root-post-id",
	)
	require.NoError(t, err)
	_, readErr := streamResult.ReadAll()
	require.NoError(t, readErr)

	require.Len(t, lm.requests, 1)
	require.NotNil(t, lm.requests[0].Context.Tools.GetTool("jira__get_issue"))
	require.Equal(t, "conv-id", lm.requests[0].Context.ConversationID)
}

func TestHandleToolResultScopesSharedToClickedPost(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	postAID := "post-a"
	postBID := "post-b"

	assistantBlocksA := []conversation.ContentBlock{{
		Type: conversation.BlockTypeToolUse, ID: "tool-use-a", Name: "search", Status: conversation.StatusSuccess,
	}}
	assistantBlocksB := []conversation.ContentBlock{{
		Type: conversation.BlockTypeToolUse, ID: "tool-use-b", Name: "search", Status: conversation.StatusSuccess,
	}}
	resultBlocksA := []conversation.ContentBlock{{
		Type: conversation.BlockTypeToolResult, ToolUseID: "tool-use-a", Content: "result-a", Status: conversation.StatusSuccess,
	}}
	resultBlocksB := []conversation.ContentBlock{{
		Type: conversation.BlockTypeToolResult, ToolUseID: "tool-use-b", Content: "result-b", Status: conversation.StatusSuccess,
	}}

	contentA, err := json.Marshal(assistantBlocksA)
	require.NoError(t, err)
	contentB, err := json.Marshal(assistantBlocksB)
	require.NoError(t, err)
	resultContentA, err := json.Marshal(resultBlocksA)
	require.NoError(t, err)
	resultContentB, err := json.Marshal(resultBlocksB)
	require.NoError(t, err)

	for _, turn := range []store.Turn{
		{ID: "assistant-a", ConversationID: conv.ID, PostID: &postAID, Role: "assistant", Content: contentA, Sequence: 1},
		{ID: "assistant-b", ConversationID: conv.ID, PostID: &postBID, Role: "assistant", Content: contentB, Sequence: 2},
		{ID: "result-a", ConversationID: conv.ID, Role: "tool_result", Content: resultContentA, Sequence: 3},
		{ID: "result-b", ConversationID: conv.ID, Role: "tool_result", Content: resultContentB, Sequence: 4},
	} {
		require.NoError(t, convStore.CreateTurn(&turn))
	}

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{loadedStateBot(&loadedStateLLM{})})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()

	c := &Conversations{
		mmClient:    mmClient,
		bots:        botsService,
		convService: conversation.NewService(convStore, nil, nil, nil),
	}

	clickedPost := &model.Post{Id: postAID, UserId: "bot-id"}
	clickedPost.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

	require.NoError(t, c.HandleToolResult(
		context.Background(),
		"user-id",
		clickedPost,
		channel,
		[]string{"tool-use-a", "tool-use-b"},
	))

	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 4)

	var updatedAssistantA, updatedAssistantB, updatedResultA, updatedResultB []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[0].Content, &updatedAssistantA))
	require.NoError(t, json.Unmarshal(turns[1].Content, &updatedAssistantB))
	require.NoError(t, json.Unmarshal(turns[2].Content, &updatedResultA))
	require.NoError(t, json.Unmarshal(turns[3].Content, &updatedResultB))

	require.NotNil(t, updatedAssistantA[0].Shared)
	require.True(t, *updatedAssistantA[0].Shared)
	require.Nil(t, updatedAssistantB[0].Shared)
	require.NotNil(t, updatedResultA[0].Shared)
	require.True(t, *updatedResultA[0].Shared)
	require.Nil(t, updatedResultB[0].Shared)
}
