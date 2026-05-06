// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channels

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inMemoryStore implements conversation.Store using in-memory maps. It provides
// just enough functionality for testing without a real database.
type inMemoryStore struct {
	conversations map[string]*store.Conversation
	turns         map[string]*store.Turn
	turnsByConv   map[string][]string // conversationID -> []turnID
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{
		conversations: make(map[string]*store.Conversation),
		turns:         make(map[string]*store.Turn),
		turnsByConv:   make(map[string][]string),
	}
}

func (s *inMemoryStore) CreateConversation(conv *store.Conversation) error {
	if _, exists := s.conversations[conv.ID]; exists {
		return fmt.Errorf("conversation already exists")
	}
	s.conversations[conv.ID] = conv
	return nil
}

func (s *inMemoryStore) GetConversation(id string) (*store.Conversation, error) {
	conv, ok := s.conversations[id]
	if !ok {
		return nil, store.ErrConversationNotFound
	}
	return conv, nil
}

func (s *inMemoryStore) GetConversationByThreadBotUser(_, _, _ string) (*store.Conversation, error) {
	return nil, store.ErrConversationNotFound
}

func (s *inMemoryStore) UpdateConversationTitle(id, title string) error {
	conv, ok := s.conversations[id]
	if !ok {
		return store.ErrConversationNotFound
	}
	conv.Title = title
	return nil
}

func (s *inMemoryStore) UpdateConversationRootPostID(id string, rootPostID string) error {
	conv, ok := s.conversations[id]
	if !ok {
		return store.ErrConversationNotFound
	}
	conv.RootPostID = &rootPostID
	return nil
}

func (s *inMemoryStore) CreateTurn(turn *store.Turn) error {
	s.turns[turn.ID] = turn
	s.turnsByConv[turn.ConversationID] = append(s.turnsByConv[turn.ConversationID], turn.ID)
	return nil
}

func (s *inMemoryStore) CreateTurnAutoSequence(turn *store.Turn) error {
	maxSeq, _ := s.GetMaxSequenceForConversation(turn.ConversationID)
	turn.Sequence = maxSeq + 1
	return s.CreateTurn(turn)
}

func (s *inMemoryStore) GetTurnsForConversation(conversationID string) ([]store.Turn, error) {
	ids := s.turnsByConv[conversationID]
	turns := make([]store.Turn, 0, len(ids))
	for _, id := range ids {
		if t, ok := s.turns[id]; ok {
			turns = append(turns, *t)
		}
	}
	return turns, nil
}

func (s *inMemoryStore) UpdateTurnContent(id string, content json.RawMessage) error {
	turn, ok := s.turns[id]
	if !ok {
		return fmt.Errorf("turn not found")
	}
	turn.Content = content
	return nil
}

func (s *inMemoryStore) UpdateTurnTokens(id string, tokensIn, tokensOut int64) error {
	turn, ok := s.turns[id]
	if !ok {
		return fmt.Errorf("turn not found")
	}
	turn.TokensIn = tokensIn
	turn.TokensOut = tokensOut
	return nil
}

func (s *inMemoryStore) GetMaxSequenceForConversation(conversationID string) (int, error) {
	maxSeq := 0
	for _, id := range s.turnsByConv[conversationID] {
		if t, ok := s.turns[id]; ok && t.Sequence > maxSeq {
			maxSeq = t.Sequence
		}
	}
	return maxSeq, nil
}

// testBotLookup implements conversation.BotLookup for tests.
type testBotLookup struct {
	botUserIDs map[string]bool
}

func (b *testBotLookup) IsAnyBot(userID string) bool {
	return b.botUserIDs[userID]
}

func (b *testBotLookup) GetBotConfigByID(string) (bool, int64, bool) {
	return false, 0, false
}

// fakeLLM implements llm.LanguageModel for tests. It returns a sequence of
// pre-configured streams, one per call to ChatCompletion.
type fakeLLM struct {
	calls   [][]llm.TextStreamEvent // events to return per call
	callIdx int
}

func (f *fakeLLM) ChatCompletion(_ llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	if f.callIdx >= len(f.calls) {
		return nil, fmt.Errorf("unexpected call #%d to ChatCompletion", f.callIdx)
	}
	events := f.calls[f.callIdx]
	f.callIdx++
	ch := make(chan llm.TextStreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return &llm.TextStreamResult{Stream: ch}, nil
}

func (f *fakeLLM) ChatCompletionNoStream(_ llm.CompletionRequest, _ ...llm.LanguageModelOption) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (f *fakeLLM) CountTokens(_ string) int { return 0 }
func (f *fakeLLM) InputTokenLimit() int     { return 100000 }

// makeTool creates a minimal llm.Tool with a simple resolver for testing.
func makeTool(name, result string) llm.Tool {
	return llm.Tool{
		Name:        name,
		Description: "test tool",
		Resolver: func(_ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			return result, nil
		},
	}
}

// makeToolWithError creates a tool whose resolver always returns an error.
func makeToolWithError(name, errMsg string) llm.Tool {
	return llm.Tool{
		Name:        name,
		Description: "test tool that errors",
		Resolver: func(_ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "", fmt.Errorf("%s", errMsg)
		},
	}
}

// textStreamEvents creates a simple text+end event sequence.
func textStreamEvents(text string) []llm.TextStreamEvent {
	return []llm.TextStreamEvent{
		{Type: llm.EventTypeText, Value: text},
		{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 100, OutputTokens: 50}},
		{Type: llm.EventTypeEnd},
	}
}

// toolCallStreamEvents creates events that include a tool call, usage, then end.
func toolCallStreamEvents(toolCallID, toolName string, args json.RawMessage) []llm.TextStreamEvent {
	return []llm.TextStreamEvent{
		{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
			{ID: toolCallID, Name: toolName, Arguments: args},
		}},
		{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 200, OutputTokens: 80}},
		{Type: llm.EventTypeEnd},
	}
}

func setupConvSvc(s *inMemoryStore) *conversation.Service {
	bots := &testBotLookup{botUserIDs: map[string]bool{}}
	return conversation.NewService(s, nil, nil, bots)
}

func TestAnalyzeChannelAndInterval(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "AnalyzeChannel creates conversation with tool turns and returns result",
			run: func(t *testing.T) {
				memStore := newInMemoryStore()
				convSvc := setupConvSvc(memStore)

				// LLM: first call returns tool call, second call returns text.
				fakeLM := &fakeLLM{
					calls: [][]llm.TextStreamEvent{
						toolCallStreamEvents("tc1", "read_channel", json.RawMessage(`{}`)),
						textStreamEvents("Here is the summary."),
					},
				}

				tools := llm.NewToolStore(nil, false)
				tools.AddTools([]llm.Tool{
					makeTool("read_channel", "channel posts here"),
					makeTool("get_channel_info", "channel info here"),
				})

				ctx := llm.NewContext()
				ctx.Channel = &model.Channel{Id: "chan123", DisplayName: "Test Channel"}
				ctx.RequestingUser = &model.User{Id: "user1"}
				ctx.Tools = tools

				ch := New(fakeLM, nil, nil, nil, convSvc)
				result, err := ch.AnalyzeChannelWithRequest(ctx, "user1", "bot1", "test system prompt", "test user prompt", "summary")
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.NotEmpty(t, result.ConversationID)

				// Read stream to completion.
				text, err := result.Stream.ReadAll()
				require.NoError(t, err)
				assert.Equal(t, "Here is the summary.", text)

				// Verify conversation was created.
				conv, err := memStore.GetConversation(result.ConversationID)
				require.NoError(t, err)
				assert.Equal(t, llm.OperationChannelSummary, conv.Operation)
				assert.Equal(t, "user1", conv.UserID)
				assert.Equal(t, "bot1", conv.BotID)
				// Channel analysis is DM-delivered and personal, so the
				// conversation must be owner-only (no ChannelID). This
				// gates GET /conversations/{id} into the threadless
				// (owner-only) branch rather than channel-membership.
				assert.Nil(t, conv.ChannelID, "channel analysis conversation must be owner-only")

				// Verify turns: user(1) + assistant-tool(2) + tool-result(3) = 3 turns.
				turns, err := memStore.GetTurnsForConversation(result.ConversationID)
				require.NoError(t, err)
				require.Len(t, turns, 3)

				assert.Equal(t, "user", turns[0].Role)
				assert.Equal(t, 1, turns[0].Sequence)

				assert.Equal(t, "assistant", turns[1].Role)
				assert.Equal(t, 2, turns[1].Sequence)
				// Verify token counts on assistant tool turn.
				assert.Equal(t, int64(200), turns[1].TokensIn)
				assert.Equal(t, int64(80), turns[1].TokensOut)

				assert.Equal(t, "tool_result", turns[2].Role)
				assert.Equal(t, 3, turns[2].Sequence)

				// Verify tool result content is shared.
				var resultBlocks []conversation.ContentBlock
				err = json.Unmarshal(turns[2].Content, &resultBlocks)
				require.NoError(t, err)
				require.Len(t, resultBlocks, 1)
				assert.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
				require.NotNil(t, resultBlocks[0].Shared)
				assert.True(t, *resultBlocks[0].Shared)
			},
		},
		{
			name: "Interval creates conversation without tools",
			run: func(t *testing.T) {
				memStore := newInMemoryStore()
				convSvc := setupConvSvc(memStore)

				fakeLM := &fakeLLM{
					calls: [][]llm.TextStreamEvent{
						textStreamEvents("Here is the interval summary."),
					},
				}

				// Interval needs prompts and a client for post fetching.
				// Use a minimal setup that bypasses prompt formatting by
				// pre-populating the context parameters.
				ctx := llm.NewContext()
				ctx.Channel = &model.Channel{Id: "chan456", DisplayName: "Interval Channel"}
				ctx.RequestingUser = &model.User{Id: "user2"}

				ch := New(fakeLM, nil, nil, nil, convSvc)

				// Call IntervalWithRequest directly to avoid needing real
				// post-fetching infrastructure.
				result, err := ch.IntervalWithRequest(
					ctx,
					"user2",
					"bot2",
					"formatted system prompt",
					"formatted user prompt",
					"summarize_channel_range_system",
				)
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.NotEmpty(t, result.ConversationID)

				text, err := result.Stream.ReadAll()
				require.NoError(t, err)
				assert.Equal(t, "Here is the interval summary.", text)

				conv, err := memStore.GetConversation(result.ConversationID)
				require.NoError(t, err)
				assert.Equal(t, llm.OperationChannelInterval, conv.Operation)
				assert.Equal(t, "user2", conv.UserID)
				assert.Equal(t, "bot2", conv.BotID)
				assert.Nil(t, conv.ChannelID, "interval conversation must be owner-only")

				// Verify turns: just user(1). No tool turns.
				turns, err := memStore.GetTurnsForConversation(result.ConversationID)
				require.NoError(t, err)
				require.Len(t, turns, 1)
				assert.Equal(t, "user", turns[0].Role)
			},
		},
		{
			name: "AnalyzeChannel with multiple tool rounds writes sequential turns",
			run: func(t *testing.T) {
				memStore := newInMemoryStore()
				convSvc := setupConvSvc(memStore)

				// LLM: tool call -> tool call -> text.
				fakeLM := &fakeLLM{
					calls: [][]llm.TextStreamEvent{
						toolCallStreamEvents("tc1", "read_channel", json.RawMessage(`{}`)),
						toolCallStreamEvents("tc2", "get_channel_info", json.RawMessage(`{}`)),
						textStreamEvents("Final analysis."),
					},
				}

				tools := llm.NewToolStore(nil, false)
				tools.AddTools([]llm.Tool{
					makeTool("read_channel", "posts data"),
					makeTool("get_channel_info", "info data"),
				})

				ctx := llm.NewContext()
				ctx.Channel = &model.Channel{Id: "chan789", DisplayName: "Multi-tool Channel"}
				ctx.RequestingUser = &model.User{Id: "user3"}
				ctx.Tools = tools

				ch := New(fakeLM, nil, nil, nil, convSvc)
				result, err := ch.AnalyzeChannelWithRequest(ctx, "user3", "bot3", "test system prompt", "test user prompt", "deep_analysis")
				require.NoError(t, err)

				text, err := result.Stream.ReadAll()
				require.NoError(t, err)
				assert.Equal(t, "Final analysis.", text)

				// Turns: user(1) + 2*(assistant+tool_result) = 5 turns.
				turns, err := memStore.GetTurnsForConversation(result.ConversationID)
				require.NoError(t, err)
				require.Len(t, turns, 5)

				// Verify sequential sequence numbers.
				assert.Equal(t, 1, turns[0].Sequence) // user
				assert.Equal(t, 2, turns[1].Sequence) // assistant tool round 1
				assert.Equal(t, 3, turns[2].Sequence) // tool_result round 1
				assert.Equal(t, 4, turns[3].Sequence) // assistant tool round 2
				assert.Equal(t, 5, turns[4].Sequence) // tool_result round 2
			},
		},
		{
			name: "AnalyzeChannel tool execution error records error in tool result",
			run: func(t *testing.T) {
				memStore := newInMemoryStore()
				convSvc := setupConvSvc(memStore)

				// LLM: tool call that will error -> then text response.
				fakeLM := &fakeLLM{
					calls: [][]llm.TextStreamEvent{
						toolCallStreamEvents("tc1", "read_channel", json.RawMessage(`{}`)),
						textStreamEvents("Summary despite error."),
					},
				}

				tools := llm.NewToolStore(nil, false)
				tools.AddTools([]llm.Tool{
					makeToolWithError("read_channel", "connection refused"),
					makeTool("get_channel_info", "info"),
				})

				ctx := llm.NewContext()
				ctx.Channel = &model.Channel{Id: "chanErr", DisplayName: "Error Channel"}
				ctx.RequestingUser = &model.User{Id: "user4"}
				ctx.Tools = tools

				ch := New(fakeLM, nil, nil, nil, convSvc)
				result, err := ch.AnalyzeChannelWithRequest(ctx, "user4", "bot4", "test system prompt", "test user prompt", "summary")
				require.NoError(t, err)

				text, err := result.Stream.ReadAll()
				require.NoError(t, err)
				assert.Equal(t, "Summary despite error.", text)

				// Verify tool result turn has error status.
				turns, err := memStore.GetTurnsForConversation(result.ConversationID)
				require.NoError(t, err)
				require.Len(t, turns, 3) // user + assistant-tool + tool-result

				var resultBlocks []conversation.ContentBlock
				err = json.Unmarshal(turns[2].Content, &resultBlocks)
				require.NoError(t, err)
				require.Len(t, resultBlocks, 1)
				assert.Equal(t, conversation.StatusError, resultBlocks[0].Status)
				assert.Contains(t, resultBlocks[0].Content, "connection refused")
			},
		},
		{
			name: "AnalyzeChannel no tool calls returns result directly",
			run: func(t *testing.T) {
				memStore := newInMemoryStore()
				convSvc := setupConvSvc(memStore)

				// LLM returns text without tool calls.
				fakeLM := &fakeLLM{
					calls: [][]llm.TextStreamEvent{
						textStreamEvents("Direct answer."),
					},
				}

				tools := llm.NewToolStore(nil, false)
				tools.AddTools([]llm.Tool{
					makeTool("read_channel", "posts"),
					makeTool("get_channel_info", "info"),
				})

				ctx := llm.NewContext()
				ctx.Channel = &model.Channel{Id: "chanDirect", DisplayName: "Direct Channel"}
				ctx.RequestingUser = &model.User{Id: "user5"}
				ctx.Tools = tools

				ch := New(fakeLM, nil, nil, nil, convSvc)
				result, err := ch.AnalyzeChannelWithRequest(ctx, "user5", "bot5", "test system prompt", "test user prompt", "summary")
				require.NoError(t, err)

				text, err := result.Stream.ReadAll()
				require.NoError(t, err)
				assert.Equal(t, "Direct answer.", text)

				// Only user turn, no tool turns.
				turns, err := memStore.GetTurnsForConversation(result.ConversationID)
				require.NoError(t, err)
				require.Len(t, turns, 1)
				assert.Equal(t, "user", turns[0].Role)
			},
		},
		{
			name: "AnalyzeChannel token tracking through tool rounds",
			run: func(t *testing.T) {
				memStore := newInMemoryStore()
				convSvc := setupConvSvc(memStore)

				fakeLM := &fakeLLM{
					calls: [][]llm.TextStreamEvent{
						// Tool round with specific usage.
						{
							{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
								{ID: "tc1", Name: "read_channel", Arguments: json.RawMessage(`{}`)},
							}},
							{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 500, OutputTokens: 150}},
							{Type: llm.EventTypeEnd},
						},
						// Final response with different usage.
						{
							{Type: llm.EventTypeText, Value: "Token tracked summary."},
							{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 800, OutputTokens: 300}},
							{Type: llm.EventTypeEnd},
						},
					},
				}

				tools := llm.NewToolStore(nil, false)
				tools.AddTools([]llm.Tool{makeTool("read_channel", "data")})

				ctx := llm.NewContext()
				ctx.Channel = &model.Channel{Id: "chanTokens", DisplayName: "Token Channel"}
				ctx.RequestingUser = &model.User{Id: "user6"}
				ctx.Tools = tools

				ch := New(fakeLM, nil, nil, nil, convSvc)
				result, err := ch.AnalyzeChannelWithRequest(ctx, "user6", "bot6", "test system prompt", "test user prompt", "summary")
				require.NoError(t, err)
				_, err = result.Stream.ReadAll()
				require.NoError(t, err)

				turns, err := memStore.GetTurnsForConversation(result.ConversationID)
				require.NoError(t, err)
				require.Len(t, turns, 3) // user + assistant-tool + tool_result

				// Verify tool round assistant turn has the tool round's token counts.
				assert.Equal(t, int64(500), turns[1].TokensIn)
				assert.Equal(t, int64(150), turns[1].TokensOut)
			},
		},
		{
			name: "AnalyzeChannel tool turn blocks are marked shared",
			run: func(t *testing.T) {
				memStore := newInMemoryStore()
				convSvc := setupConvSvc(memStore)

				fakeLM := &fakeLLM{
					calls: [][]llm.TextStreamEvent{
						toolCallStreamEvents("tc1", "read_channel", json.RawMessage(`{}`)),
						textStreamEvents("Done."),
					},
				}

				tools := llm.NewToolStore(nil, false)
				tools.AddTools([]llm.Tool{makeTool("read_channel", "data")})

				ctx := llm.NewContext()
				ctx.Channel = &model.Channel{Id: "chanShared", DisplayName: "Shared Channel"}
				ctx.RequestingUser = &model.User{Id: "user7"}
				ctx.Tools = tools

				ch := New(fakeLM, nil, nil, nil, convSvc)
				result, err := ch.AnalyzeChannelWithRequest(ctx, "user7", "bot7", "test system prompt", "test user prompt", "summary")
				require.NoError(t, err)
				_, err = result.Stream.ReadAll()
				require.NoError(t, err)

				turns, err := memStore.GetTurnsForConversation(result.ConversationID)
				require.NoError(t, err)
				require.Len(t, turns, 3)

				// Check assistant tool_use blocks are shared.
				var assistantBlocks []conversation.ContentBlock
				err = json.Unmarshal(turns[1].Content, &assistantBlocks)
				require.NoError(t, err)
				for _, block := range assistantBlocks {
					if block.Type == conversation.BlockTypeToolUse {
						require.NotNil(t, block.Shared)
						assert.True(t, *block.Shared, "tool_use block should be shared=true")
					}
				}

				// Check tool_result blocks are shared.
				var resultBlocks []conversation.ContentBlock
				err = json.Unmarshal(turns[2].Content, &resultBlocks)
				require.NoError(t, err)
				for _, block := range resultBlocks {
					if block.Type == conversation.BlockTypeToolResult {
						require.NotNil(t, block.Shared)
						assert.True(t, *block.Shared, "tool_result block should be shared=true")
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.run(t)
		})
	}
}

// TestAnalysisResultConversationID verifies that the AnalysisResult.ConversationID
// matches the conversation stored in the store -- a critical property for
// the streaming layer to link the post to the conversation.
func TestAnalysisResultConversationID(t *testing.T) {
	memStore := newInMemoryStore()
	convSvc := setupConvSvc(memStore)

	fakeLM := &fakeLLM{
		calls: [][]llm.TextStreamEvent{
			textStreamEvents("Quick answer."),
		},
	}

	tools := llm.NewToolStore(nil, false)
	tools.AddTools([]llm.Tool{
		makeTool("read_channel", "posts"),
		makeTool("get_channel_info", "info"),
	})

	ctx := llm.NewContext()
	ctx.Channel = &model.Channel{Id: "chanMatch", DisplayName: "Match Channel"}
	ctx.RequestingUser = &model.User{Id: "user8"}
	ctx.Tools = tools

	ch := New(fakeLM, nil, nil, nil, convSvc)
	result, err := ch.AnalyzeChannelWithRequest(ctx, "user8", "bot8", "test system prompt", "test user prompt", "summary")
	require.NoError(t, err)
	_, _ = result.Stream.ReadAll()

	// The conversation ID on the result must correspond to an actual conversation.
	conv, err := memStore.GetConversation(result.ConversationID)
	require.NoError(t, err)
	assert.Equal(t, result.ConversationID, conv.ID)
}

// TestIntervalWithRequestConversationID verifies the same for Interval.
func TestIntervalWithRequestConversationID(t *testing.T) {
	memStore := newInMemoryStore()
	convSvc := setupConvSvc(memStore)

	fakeLM := &fakeLLM{
		calls: [][]llm.TextStreamEvent{
			textStreamEvents("Interval done."),
		},
	}

	ctx := llm.NewContext()
	ctx.Channel = &model.Channel{Id: "chanInt", DisplayName: "Int Channel"}
	ctx.RequestingUser = &model.User{Id: "user9"}

	ch := New(fakeLM, nil, nil, nil, convSvc)
	result, err := ch.IntervalWithRequest(
		ctx, "user9", "bot9",
		"sys prompt", "user prompt", "preset",
	)
	require.NoError(t, err)
	_, _ = result.Stream.ReadAll()

	conv, err := memStore.GetConversation(result.ConversationID)
	require.NoError(t, err)
	assert.Equal(t, result.ConversationID, conv.ID)
	assert.Equal(t, llm.OperationChannelInterval, conv.Operation)
}
