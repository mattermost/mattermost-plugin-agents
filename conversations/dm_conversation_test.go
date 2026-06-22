// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/conversations"
	"github.com/mattermost/mattermost-plugin-agents/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/i18n"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/prompts"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- In-memory fake conversation store ------------------------------------

type fakeConvStore struct {
	mu            sync.Mutex
	conversations map[string]*store.Conversation
	turns         map[string][]store.Turn // keyed by conversationID
	allTurns      map[string]*store.Turn  // keyed by turn ID
}

func newFakeConvStore() *fakeConvStore {
	return &fakeConvStore{
		conversations: make(map[string]*store.Conversation),
		turns:         make(map[string][]store.Turn),
		allTurns:      make(map[string]*store.Turn),
	}
}

func (s *fakeConvStore) CreateConversation(conv *store.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if conv.RootPostID != nil {
		for _, existing := range s.conversations {
			if existing.RootPostID != nil && *existing.RootPostID == *conv.RootPostID &&
				existing.BotID == conv.BotID && existing.DeleteAt == 0 {
				return store.ErrConversationConflict
			}
		}
	}
	c := *conv
	s.conversations[conv.ID] = &c
	return nil
}

func (s *fakeConvStore) GetConversation(id string) (*store.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok || conv.DeleteAt != 0 {
		return nil, store.ErrConversationNotFound
	}
	c := *conv
	return &c, nil
}

func (s *fakeConvStore) GetConversationByThreadBotUser(rootPostID, botID, userID string) (*store.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conv := range s.conversations {
		if conv.RootPostID != nil && *conv.RootPostID == rootPostID &&
			conv.BotID == botID && conv.UserID == userID && conv.DeleteAt == 0 {
			c := *conv
			return &c, nil
		}
	}
	return nil, store.ErrConversationNotFound
}

func (s *fakeConvStore) UpdateConversationTitle(id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok {
		return store.ErrConversationNotFound
	}
	conv.Title = title
	return nil
}

func (s *fakeConvStore) UpdateConversationRootPostID(id string, rootPostID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, ok := s.conversations[id]
	if !ok {
		return store.ErrConversationNotFound
	}
	conv.RootPostID = &rootPostID
	return nil
}

func (s *fakeConvStore) CreateTurn(turn *store.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := *turn
	s.turns[turn.ConversationID] = append(s.turns[turn.ConversationID], t)
	s.allTurns[turn.ID] = &t
	return nil
}

func (s *fakeConvStore) CreateTurnAutoSequence(turn *store.Turn) error {
	maxSeq, _ := s.GetMaxSequenceForConversation(turn.ConversationID)
	turn.Sequence = maxSeq + 1
	return s.CreateTurn(turn)
}

func (s *fakeConvStore) GetTurnsForConversation(conversationID string) ([]store.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.turns[conversationID]
	result := make([]store.Turn, len(turns))
	copy(result, turns)
	return result, nil
}

func (s *fakeConvStore) UpdateTurnContent(id string, content json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.allTurns[id]
	if !ok {
		return fmt.Errorf("turn %s not found", id)
	}
	t.Content = content
	for convID, turns := range s.turns {
		for i := range turns {
			if turns[i].ID == id {
				s.turns[convID][i].Content = content
			}
		}
	}
	return nil
}

func (s *fakeConvStore) GetTurnByPostID(postID string) (*store.Turn, error) {
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

func (s *fakeConvStore) UpdateTurnPostID(id string, postID *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.allTurns[id]
	if !ok {
		return fmt.Errorf("turn %s not found", id)
	}
	t.PostID = postID
	for convID, turns := range s.turns {
		for i := range turns {
			if turns[i].ID == id {
				s.turns[convID][i].PostID = postID
			}
		}
	}
	return nil
}

func (s *fakeConvStore) DeleteResponseTurns(conversationID, postID string) error {
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

func (s *fakeConvStore) UpdateTurnTokens(id string, tokensIn, tokensOut int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.allTurns[id]
	if !ok {
		return fmt.Errorf("turn %s not found", id)
	}
	t.TokensIn = tokensIn
	t.TokensOut = tokensOut
	for convID, turns := range s.turns {
		for i := range turns {
			if turns[i].ID == id {
				s.turns[convID][i].TokensIn = tokensIn
				s.turns[convID][i].TokensOut = tokensOut
			}
		}
	}
	return nil
}

func (s *fakeConvStore) GetMaxSequenceForConversation(conversationID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.turns[conversationID]
	maxSeq := 0
	for _, t := range turns {
		if t.Sequence > maxSeq {
			maxSeq = t.Sequence
		}
	}
	return maxSeq, nil
}

// getConv is a test helper to retrieve a conversation by ID.
func (s *fakeConvStore) getConv(id string) *store.Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conversations[id]
	if !ok {
		return nil
	}
	cp := *c
	return &cp
}

// turnsFor is a test helper to retrieve turns for a conversation.
func (s *fakeConvStore) turnsFor(convID string) []store.Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.turns[convID]
	result := make([]store.Turn, len(turns))
	copy(result, turns)
	return result
}

// --- Fake LLM that records requests and returns canned streams -------------

type dmTestLLM struct {
	mu        sync.Mutex
	responses []*llm.TextStreamResult
	callIdx   int
	requests  []llm.CompletionRequest
}

func newDMTestLLM(responses ...*llm.TextStreamResult) *dmTestLLM {
	return &dmTestLLM{responses: responses}
}

func (f *dmTestLLM) ChatCompletion(_ context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	if f.callIdx >= len(f.responses) {
		return nil, fmt.Errorf("no more responses configured")
	}
	resp := f.responses[f.callIdx]
	f.callIdx++
	return resp, nil
}

func (f *dmTestLLM) ChatCompletionNoStream(_ context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	return "Test Title", nil
}

func (f *dmTestLLM) CountTokens(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}
func (f *dmTestLLM) InputTokenLimit() int  { return 100000 }
func (f *dmTestLLM) OutputTokenLimit() int { return 8192 }

// dmMakeTextStream creates a TextStreamResult that emits the given text and closes.
func dmMakeTextStream(text string) *llm.TextStreamResult {
	ch := make(chan llm.TextStreamEvent, 3)
	go func() {
		defer close(ch)
		ch <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: text}
		ch <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	}()
	return &llm.TextStreamResult{Stream: ch}
}

// dmMakeToolCallStream creates a TextStreamResult that emits tool calls and closes.
func dmMakeToolCallStream(toolCalls []llm.ToolCall) *llm.TextStreamResult {
	ch := make(chan llm.TextStreamEvent, 3)
	go func() {
		defer close(ch)
		ch <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		ch <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	}()
	return &llm.TextStreamResult{Stream: ch}
}

// --- Fake policy checker ---------------------------------------------------

type dmPolicyChecker struct {
	policies map[string]string // key: "origin:name", value: policy string
}

func newDMPolicyChecker() *dmPolicyChecker {
	return &dmPolicyChecker{policies: make(map[string]string)}
}

func (f *dmPolicyChecker) setAutoRun(origin, name string) {
	f.policies[origin+":"+name] = mcp.ToolPolicyAutoRunInDM
}

func (f *dmPolicyChecker) GetToolPolicy(serverBaseURL, toolName string) (string, bool) {
	key := serverBaseURL + ":" + toolName
	p, ok := f.policies[key]
	if !ok {
		return "ask", false
	}
	return p, true
}

// --- DM test environment ---------------------------------------------------

type dmTestEnv struct {
	convStore     *fakeConvStore
	convService   *conversation.Service
	conversations *conversations.Conversations
	fakeLLM       *dmTestLLM
	policyChecker *dmPolicyChecker
	streamService *fakeStreamingService
	mockAPI       *plugintest.API
	mmClient      *fakeMMClient
	mcpMgr        *testMCPClientManager
	botID         string
	userID        string
	channelID     string
	channel       *model.Channel
	user          *model.User
}

func setupDMTestEnv(t *testing.T, llmResponses ...*llm.TextStreamResult) *dmTestEnv {
	t.Helper()

	const (
		botID     = "bot1"
		botUserID = "bot1"
		userID    = "user1"
		channelID = "dm_channel"
		teamID    = "team1"
	)

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)

	siteName := "Test"
	defaultLocale := "en"
	mockAPI.On("GetConfig").Return(&model.Config{
		TeamSettings: model.TeamSettings{SiteName: &siteName},
		LocalizationSettings: model.LocalizationSettings{
			DefaultServerLocale: &defaultLocale,
		},
	}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "advanced"}).Maybe()
	mockAPI.On("GetTeam", teamID).Return(&model.Team{Id: teamID, Name: "test"}, nil).Maybe()

	botsService := bots.New(mockAPI, client, licenseChecker, nil, nil, &http.Client{}, nil)

	fLLM := newDMTestLLM(llmResponses...)

	// Register a bot so it can be retrieved
	mockAPI.On("GetUser", botUserID).Return(&model.User{
		Id:       botUserID,
		Username: "ai",
		IsBot:    true,
	}, nil).Maybe()
	mockAPI.On("GetBotUsers").Return(nil, nil).Maybe()

	channel := &model.Channel{
		Id:   channelID,
		Type: model.ChannelTypeDirect,
		Name: botUserID + "__" + userID,
	}
	user := &model.User{Id: userID, Username: "testuser", Locale: "en"}
	mmClient := &fakeMMClient{
		users: map[string]*model.User{
			userID: user,
		},
		channels: map[string]*model.Channel{
			channelID: channel,
		},
		kv:              make(map[string]interface{}),
		allowCreatePost: true,
	}

	i18nBundle := i18n.Init()
	promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	streamSvc := &fakeStreamingService{}
	policyChecker := newDMPolicyChecker()

	// Create the context builder with minimal setup
	tp := &testToolProvider{tools: nil}
	mcpMgr := &testMCPClientManager{}
	contextBuilder := llmcontext.NewLLMContextBuilder(client, tp, mcpMgr, nil)

	convFakeStore := newFakeConvStore()
	convSvc := conversation.NewService(convFakeStore, promptsManager, nil, botsService)
	botsService.SetBotsForTesting([]*bots.Bot{
		bots.NewBot(
			llm.BotConfig{
				ID:                    botID,
				Name:                  "ai",
				DisplayName:           "AI",
				AutoEnableNewMCPTools: true,
				MCPDynamicToolLoading: true,
			},
			llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
			&model.Bot{UserId: botUserID, Username: "ai", DisplayName: "AI"},
			fLLM,
		),
	})

	convs := conversations.New(
		promptsManager,
		mmClient,
		streamSvc,
		contextBuilder,
		botsService,
		nil, // db
		licenseChecker,
		i18nBundle,
		nil, // meetings
		&testToolCallingConfig{},
	)
	convs.SetToolPolicyChecker(policyChecker)
	convs.SetConversationService(convSvc)

	return &dmTestEnv{
		convStore:     convFakeStore,
		convService:   convSvc,
		conversations: convs,
		fakeLLM:       fLLM,
		policyChecker: policyChecker,
		streamService: streamSvc,
		mockAPI:       mockAPI,
		mmClient:      mmClient,
		mcpMgr:        mcpMgr,
		botID:         botID,
		userID:        userID,
		channelID:     channelID,
		channel:       channel,
		user:          user,
	}
}

// testMCPClientManager implements llmcontext.MCPClientManager for testing.
type testMCPClientManager struct {
	tools  []llm.Tool
	errors *mcp.Errors
}

func (m *testMCPClientManager) GetToolsForUser(context.Context, string) ([]llm.Tool, *mcp.Errors) {
	return m.tools, m.errors
}

// --- Test: new DM creates conversation entity and returns stream ----------

func TestDMNewConversation_CreatesConversationAndTurns(t *testing.T) {
	env := setupDMTestEnv(t, dmMakeTextStream("Hello!"))

	post := &model.Post{
		Id:        "post1",
		UserId:    env.userID,
		ChannelId: env.channelID,
		Message:   "Hello bot",
	}

	convResult, err := env.conversations.CreateOrGetDMConversation(
		env.botID,
		env.user,
		env.channel,
		post,
		nil, // llmContext
	)
	require.NoError(t, err)
	require.NotNil(t, convResult)
	require.NotEmpty(t, convResult.ConversationID)
	require.True(t, convResult.IsNew)

	streamResult, err := env.conversations.ProcessDMRequest(
		context.Background(),
		convResult.ConversationID,
		env.fakeLLM,
		nil, // llmContext
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, streamResult)
	require.NotNil(t, streamResult.Stream)

	// Verify conversation was created
	conv := env.convStore.getConv(convResult.ConversationID)
	require.NotNil(t, conv)
	assert.Equal(t, env.userID, conv.UserID)
	assert.Equal(t, env.botID, conv.BotID)
	assert.Equal(t, "conversation", conv.Operation)

	// Verify user turn exists
	turns := env.convStore.turnsFor(convResult.ConversationID)
	require.GreaterOrEqual(t, len(turns), 1)
	assert.Equal(t, "user", turns[0].Role)
	assert.Equal(t, 1, turns[0].Sequence)
}

// --- Test: continuing DM reads turns, not posts ---------------------------

func TestDMContinueConversation_ReadsTurnsNotPosts(t *testing.T) {
	env := setupDMTestEnv(t, dmMakeTextStream("Follow-up response"))

	// Pre-create a conversation
	rootPostID := "root1"
	channelID := env.channelID
	createResult, err := env.convService.CreateConversation(conversation.CreateConversationParams{
		UserID:       env.userID,
		BotID:        env.botID,
		ChannelID:    &channelID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful.",
		UserMessage:  "First message",
	})
	require.NoError(t, err)

	post := &model.Post{
		Id:        "post2",
		UserId:    env.userID,
		ChannelId: env.channelID,
		RootId:    rootPostID,
		Message:   "Follow up question",
	}

	convResult, err := env.conversations.CreateOrGetDMConversation(
		env.botID,
		env.user,
		env.channel,
		post,
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, convResult)

	streamResult, err := env.conversations.ProcessDMRequest(
		context.Background(),
		convResult.ConversationID,
		env.fakeLLM,
		nil,
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, streamResult)

	// Should use existing conversation
	assert.Equal(t, createResult.ConversationID, convResult.ConversationID)
	assert.False(t, convResult.IsNew)

	// Verify the new user turn was appended
	turns := env.convStore.turnsFor(convResult.ConversationID)
	require.GreaterOrEqual(t, len(turns), 2)
	lastUserTurn := turns[len(turns)-1]
	assert.Equal(t, "user", lastUserTurn.Role)

	var blocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(lastUserTurn.Content, &blocks))
	require.Len(t, blocks, 1)
	assert.Equal(t, "Follow up question", blocks[0].Text)

	// The LLM should have been called with posts built from turns (system + user1 + user2)
	env.fakeLLM.mu.Lock()
	require.Len(t, env.fakeLLM.requests, 1)
	req := env.fakeLLM.requests[0]
	env.fakeLLM.mu.Unlock()
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	assert.GreaterOrEqual(t, len(req.Posts), 3) // system + 2 user turns
}

// --- Test: auto-run tools use ToolRunner and write turns with shared=true -

func TestDMAutoRunTools_ToolRunnerExecutesAndWritesTurns(t *testing.T) {
	toolCall := llm.ToolCall{
		ID:           "tc_01",
		Name:         "get_weather",
		Arguments:    json.RawMessage(`{"city":"NYC"}`),
		ServerOrigin: "https://mcp.example.com",
	}

	env := setupDMTestEnv(t,
		dmMakeToolCallStream([]llm.ToolCall{toolCall}),
		dmMakeTextStream("It's 72F and sunny"),
	)

	env.policyChecker.setAutoRun("https://mcp.example.com", "get_weather")

	toolStore := llm.NewToolStore()
	toolStore.AddTools([]llm.Tool{
		{
			Name:         "get_weather",
			Description:  "Gets the weather",
			ServerOrigin: "https://mcp.example.com",
			Resolver: func(_ context.Context, _ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
				return "72F and sunny", nil
			},
		},
	})
	llmCtx := &llm.Context{Tools: toolStore}

	post := &model.Post{
		Id:        "post1",
		UserId:    env.userID,
		ChannelId: env.channelID,
		Message:   "What is the weather?",
	}

	convResult, err := env.conversations.CreateOrGetDMConversation(
		env.botID,
		env.user,
		env.channel,
		post,
		llmCtx,
	)
	require.NoError(t, err)
	require.NotNil(t, convResult)
	require.NotEmpty(t, convResult.ConversationID)

	streamResult, err := env.conversations.ProcessDMRequest(
		context.Background(),
		convResult.ConversationID,
		env.fakeLLM,
		llmCtx,
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, streamResult)

	// Consume the stream so the tool runner goroutine completes
	// and tool turns are written via the callback.
	_, _ = streamResult.Stream.ReadAll()

	// Verify tool turns were written
	turns := env.convStore.turnsFor(convResult.ConversationID)

	// Expect: user(1) + assistant-with-tool-use(2) + tool_result(3)
	require.GreaterOrEqual(t, len(turns), 3)

	foundToolUse := false
	foundToolResult := false
	for _, turn := range turns {
		var blocks []conversation.ContentBlock
		if unmarshalErr := json.Unmarshal(turn.Content, &blocks); unmarshalErr != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == conversation.BlockTypeToolUse {
				foundToolUse = true
				require.NotNil(t, b.Shared, "shared field should be set")
				assert.True(t, *b.Shared, "DM tool blocks should have shared=true")
			}
			if b.Type == conversation.BlockTypeToolResult {
				foundToolResult = true
				require.NotNil(t, b.Shared, "shared field should be set")
				assert.True(t, *b.Shared, "DM tool result blocks should have shared=true")
			}
		}
	}
	assert.True(t, foundToolUse, "should have tool_use content block")
	assert.True(t, foundToolResult, "should have tool_result content block")
}

// --- Test: manual approval returns stream with unresolved tool calls ------

func TestDMManualApprovalTools_ToolRunnerReturnsUnresolved(t *testing.T) {
	toolCall := llm.ToolCall{
		ID:           "tc_01",
		Name:         "run_dangerous",
		Arguments:    json.RawMessage(`{"cmd":"delete"}`),
		ServerOrigin: "https://mcp.example.com",
	}

	env := setupDMTestEnv(t,
		dmMakeToolCallStream([]llm.ToolCall{toolCall}),
	)

	// No auto-run policy -> manual approval required
	// (default policy is "ask")
	toolStore := llm.NewToolStore()
	toolStore.AddTools([]llm.Tool{
		{
			Name:         "run_dangerous",
			Description:  "Runs a dangerous command",
			ServerOrigin: "https://mcp.example.com",
			Resolver: func(_ context.Context, ctx *llm.Context, args llm.ToolArgumentGetter) (string, error) {
				return "should not execute", nil
			},
		},
	})
	llmCtx := &llm.Context{Tools: toolStore}

	post := &model.Post{
		Id:        "post1",
		UserId:    env.userID,
		ChannelId: env.channelID,
		Message:   "Do something dangerous",
	}

	convResult, err := env.conversations.CreateOrGetDMConversation(
		env.botID,
		env.user,
		env.channel,
		post,
		llmCtx,
	)
	require.NoError(t, err)
	require.NotNil(t, convResult)

	streamResult, err := env.conversations.ProcessDMRequest(
		context.Background(),
		convResult.ConversationID,
		env.fakeLLM,
		llmCtx,
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, streamResult)
	require.NotNil(t, streamResult.Stream)

	// Consume stream and check for tool call events
	var foundToolCalls bool
	for event := range streamResult.Stream.Stream {
		if event.Type == llm.EventTypeToolCalls {
			foundToolCalls = true
		}
	}
	assert.True(t, foundToolCalls, "stream should contain unresolved tool call events")

	// No tool_result turns should exist
	turns := env.convStore.turnsFor(convResult.ConversationID)
	for _, turn := range turns {
		assert.NotEqual(t, "tool_result", turn.Role,
			"no tool_result turns should exist when tools weren't executed")
	}
}

func TestDMUnknownToolReturnsErrorInsteadOfApproval(t *testing.T) {
	toolCall := llm.ToolCall{
		ID:           "tc_unknown",
		Name:         "ghost_tool",
		Arguments:    json.RawMessage(`{"query":"hello"}`),
		ServerOrigin: "https://mcp.example.com",
	}

	env := setupDMTestEnv(t,
		dmMakeToolCallStream([]llm.ToolCall{toolCall}),
		dmMakeTextStream("I cannot use that tool"),
	)

	llmCtx := &llm.Context{Tools: llm.NewNoTools()}
	post := &model.Post{
		Id:        "post1",
		UserId:    env.userID,
		ChannelId: env.channelID,
		Message:   "Use a ghost tool",
	}

	convResult, err := env.conversations.CreateOrGetDMConversation(
		env.botID,
		env.user,
		env.channel,
		post,
		llmCtx,
	)
	require.NoError(t, err)
	require.NotNil(t, convResult)

	streamResult, err := env.conversations.ProcessDMRequest(
		context.Background(),
		convResult.ConversationID,
		env.fakeLLM,
		llmCtx,
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, streamResult)

	text, readErr := streamResult.Stream.ReadAll()
	require.NoError(t, readErr)
	assert.Equal(t, "I cannot use that tool", text)

	turns := env.convStore.turnsFor(convResult.ConversationID)
	var foundErrorToolUse bool
	var foundErrorToolResult bool
	for _, turn := range turns {
		var blocks []conversation.ContentBlock
		if unmarshalErr := json.Unmarshal(turn.Content, &blocks); unmarshalErr != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case conversation.BlockTypeToolUse:
				if b.Name == "ghost_tool" {
					assert.Equal(t, conversation.StatusError, b.Status)
					foundErrorToolUse = true
				}
			case conversation.BlockTypeToolResult:
				if b.ToolUseID == "tc_unknown" {
					assert.Equal(t, conversation.StatusError, b.Status)
					assert.Contains(t, b.Content, "unknown tool ghost_tool")
					foundErrorToolResult = true
				}
			}
		}
	}
	assert.True(t, foundErrorToolUse, "unknown tool_use should be persisted as an error, not pending approval")
	assert.True(t, foundErrorToolResult, "unknown tool should have an error tool_result")

	env.fakeLLM.mu.Lock()
	require.Len(t, env.fakeLLM.requests, 2)
	secondReq := env.fakeLLM.requests[1]
	env.fakeLLM.mu.Unlock()
	require.NotEmpty(t, secondReq.Posts)
	botPost := secondReq.Posts[len(secondReq.Posts)-1]
	require.Len(t, botPost.ToolUse, 1)
	assert.Equal(t, llm.ToolCallStatusError, botPost.ToolUse[0].Status)
	assert.Equal(t, "unknown tool ghost_tool", botPost.ToolUse[0].Result)
}

// --- Test: conversation ID returned for setting on response post ----------

func TestDMConversationIDProp_ReturnedForResponsePost(t *testing.T) {
	tests := []struct {
		name        string
		isNewConv   bool
		preCreateFn func(t *testing.T, env *dmTestEnv) string // returns rootPostID
	}{
		{
			name:      "new conversation returns conversation ID",
			isNewConv: true,
		},
		{
			name:      "continuing conversation returns conversation ID",
			isNewConv: false,
			preCreateFn: func(t *testing.T, env *dmTestEnv) string {
				rootPostID := "root1"
				channelID := env.channelID
				_, err := env.convService.CreateConversation(conversation.CreateConversationParams{
					UserID:       env.userID,
					BotID:        env.botID,
					ChannelID:    &channelID,
					RootPostID:   &rootPostID,
					Operation:    "conversation",
					SystemPrompt: "system",
					UserMessage:  "first",
				})
				require.NoError(t, err)
				return rootPostID
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupDMTestEnv(t, dmMakeTextStream("response"))

			var post *model.Post
			if tc.isNewConv {
				post = &model.Post{
					Id:        "post1",
					UserId:    env.userID,
					ChannelId: env.channelID,
					Message:   "Hello",
				}
			} else {
				rootPostID := tc.preCreateFn(t, env)
				post = &model.Post{
					Id:        "post2",
					UserId:    env.userID,
					ChannelId: env.channelID,
					RootId:    rootPostID,
					Message:   "Follow up",
				}
			}

			convResult, err := env.conversations.CreateOrGetDMConversation(
				env.botID,
				env.user,
				env.channel,
				post,
				nil,
			)
			require.NoError(t, err)
			assert.NotEmpty(t, convResult.ConversationID,
				"conversation ID must be returned so caller can set it on the response post")
		})
	}
}

// --- Test: title generation flag ------------------------------------------

func TestDMTitleGeneration_IsNewFlagForCaller(t *testing.T) {
	tests := []struct {
		name      string
		isNew     bool
		setupFn   func(t *testing.T, env *dmTestEnv) *model.Post
		expectNew bool
	}{
		{
			name:  "new conversation sets IsNew=true",
			isNew: true,
			setupFn: func(t *testing.T, env *dmTestEnv) *model.Post {
				return &model.Post{
					Id:        "post1",
					UserId:    env.userID,
					ChannelId: env.channelID,
					Message:   "Hello",
				}
			},
			expectNew: true,
		},
		{
			name:  "continuation sets IsNew=false",
			isNew: false,
			setupFn: func(t *testing.T, env *dmTestEnv) *model.Post {
				rootPostID := "root1"
				channelID := env.channelID
				_, err := env.convService.CreateConversation(conversation.CreateConversationParams{
					UserID:       env.userID,
					BotID:        env.botID,
					ChannelID:    &channelID,
					RootPostID:   &rootPostID,
					Operation:    "conversation",
					SystemPrompt: "system",
					UserMessage:  "first",
				})
				require.NoError(t, err)
				return &model.Post{
					Id:        "post2",
					UserId:    env.userID,
					ChannelId: env.channelID,
					RootId:    rootPostID,
					Message:   "Follow up",
				}
			},
			expectNew: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupDMTestEnv(t, dmMakeTextStream("response"))
			post := tc.setupFn(t, env)

			convResult, err := env.conversations.CreateOrGetDMConversation(
				env.botID,
				env.user,
				env.channel,
				post,
				nil,
			)
			require.NoError(t, err)
			assert.Equal(t, tc.expectNew, convResult.IsNew,
				"IsNew flag should indicate whether title generation is needed")
		})
	}
}

// --- Test: DM tool shared flag is always true ------------------------------

func TestDMToolSharedFlag_AlwaysTrue(t *testing.T) {
	toolCall := llm.ToolCall{
		ID:           "tc_01",
		Name:         "tool_a",
		Arguments:    json.RawMessage(`{}`),
		ServerOrigin: "https://example.com",
	}

	env := setupDMTestEnv(t,
		dmMakeToolCallStream([]llm.ToolCall{toolCall}),
		dmMakeTextStream("Done"),
	)

	env.policyChecker.setAutoRun("https://example.com", "tool_a")

	toolStore := llm.NewToolStore()
	toolStore.AddTools([]llm.Tool{
		{
			Name:         "tool_a",
			Description:  "A tool",
			ServerOrigin: "https://example.com",
			Resolver: func(_ context.Context, _ *llm.Context, args llm.ToolArgumentGetter) (string, error) {
				return "result", nil
			},
		},
	})

	post := &model.Post{
		Id:        "post1",
		UserId:    env.userID,
		ChannelId: env.channelID,
		Message:   "Run tool_a",
	}

	convResult, err := env.conversations.CreateOrGetDMConversation(
		env.botID,
		env.user,
		env.channel,
		post,
		&llm.Context{Tools: toolStore},
	)
	require.NoError(t, err)

	streamResult, err := env.conversations.ProcessDMRequest(
		context.Background(),
		convResult.ConversationID,
		env.fakeLLM,
		&llm.Context{Tools: toolStore},
		0,
	)
	require.NoError(t, err)
	require.NotNil(t, streamResult)

	// Consume the stream so the tool runner goroutine completes
	// and tool turns are written via the callback.
	_, _ = streamResult.Stream.ReadAll()

	turns := env.convStore.turnsFor(convResult.ConversationID)
	for _, turn := range turns {
		var blocks []conversation.ContentBlock
		if unmarshalErr := json.Unmarshal(turn.Content, &blocks); unmarshalErr != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == conversation.BlockTypeToolUse || b.Type == conversation.BlockTypeToolResult {
				require.NotNil(t, b.Shared)
				assert.True(t, *b.Shared,
					"all tool blocks in DM must have shared=true (type=%s)", b.Type)
			}
		}
	}
}

// --- Test: completion request is built from turns, not from thread posts --

func TestDMCompletionRequest_BuiltFromTurns(t *testing.T) {
	env := setupDMTestEnv(t, dmMakeTextStream("response"))

	// Pre-create conversation with known content
	rootPostID := "root1"
	channelID := env.channelID
	createResult, err := env.convService.CreateConversation(conversation.CreateConversationParams{
		UserID:       env.userID,
		BotID:        env.botID,
		ChannelID:    &channelID,
		RootPostID:   &rootPostID,
		Operation:    "conversation",
		SystemPrompt: "Be helpful.",
		UserMessage:  "What is 2+2?",
	})
	require.NoError(t, err)

	post := &model.Post{
		Id:        "post2",
		UserId:    env.userID,
		ChannelId: env.channelID,
		RootId:    rootPostID,
		Message:   "And what is 3+3?",
	}

	convResult, err := env.conversations.CreateOrGetDMConversation(
		env.botID,
		env.user,
		env.channel,
		post,
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, createResult.ConversationID, convResult.ConversationID)

	_, err = env.conversations.ProcessDMRequest(
		context.Background(),
		convResult.ConversationID,
		env.fakeLLM,
		nil,
		0,
	)
	require.NoError(t, err)

	// Verify the CompletionRequest sent to the LLM
	env.fakeLLM.mu.Lock()
	require.Len(t, env.fakeLLM.requests, 1)
	req := env.fakeLLM.requests[0]
	env.fakeLLM.mu.Unlock()

	// First post should be system prompt
	assert.Equal(t, llm.PostRoleSystem, req.Posts[0].Role)
	assert.Equal(t, "Be helpful.", req.Posts[0].Message)

	// Should contain both user messages from turns
	assert.Contains(t, req.Posts[1].Message, "What is 2+2?")
	assert.Contains(t, req.Posts[2].Message, "And what is 3+3?")
}
