// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/i18n"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// fakeTurnStore implements TurnStore and records all calls for test assertions.
type fakeTurnStore struct {
	mu          sync.Mutex
	turns       []*store.Turn
	updateCalls []turnContentUpdate
	tokenCalls  []turnTokenUpdate
	createErr   error
	updateErr   error
	tokenErr    error
}

type turnContentUpdate struct {
	ID      string
	Content json.RawMessage
}

type turnTokenUpdate struct {
	ID        string
	TokensIn  int64
	TokensOut int64
}

func (f *fakeTurnStore) CreateTurnAutoSequence(turn *store.Turn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	// Simulate auto-sequence: find max sequence for this conversation and increment.
	maxSeq := 0
	for _, t := range f.turns {
		if t.ConversationID == turn.ConversationID && t.Sequence > maxSeq {
			maxSeq = t.Sequence
		}
	}
	turn.Sequence = maxSeq + 1
	f.turns = append(f.turns, turn)
	return nil
}

func (f *fakeTurnStore) UpdateTurnContent(id string, content json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updateCalls = append(f.updateCalls, turnContentUpdate{ID: id, Content: content})
	return nil
}

func (f *fakeTurnStore) UpdateTurnTokens(id string, tokensIn, tokensOut int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokenErr != nil {
		return f.tokenErr
	}
	f.tokenCalls = append(f.tokenCalls, turnTokenUpdate{ID: id, TokensIn: tokensIn, TokensOut: tokensOut})
	return nil
}

// parseContentBlocks is a test helper that unmarshals content JSON into content blocks.
func parseContentBlocks(t *testing.T, raw json.RawMessage) []conversation.ContentBlock {
	t.Helper()
	var blocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(raw, &blocks))
	return blocks
}

func TestStreamToPostTurnPersistence(t *testing.T) {
	const (
		postID         = "post-id"
		channelID      = "channel-id"
		botID          = "bot-id"
		requesterID    = "requester-id"
		conversationID = "conv-id"
	)

	t.Run("creates placeholder turn on stream start", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Hello"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.turns, 1)
		turn := ts.turns[0]
		require.Equal(t, "assistant", turn.Role)
		require.Equal(t, conversationID, turn.ConversationID)
		require.NotNil(t, turn.PostID)
		require.Equal(t, postID, *turn.PostID)
		require.Equal(t, json.RawMessage("[]"), turn.Content)
		require.Equal(t, 1, turn.Sequence)
	})

	t.Run("sequence number increments from existing turns", func(t *testing.T) {
		ts := &fakeTurnStore{}
		// Pre-populate 3 existing turns.
		for i := 0; i < 3; i++ {
			pid := fmt.Sprintf("old-post-%d", i)
			ts.turns = append(ts.turns, &store.Turn{
				ID:             fmt.Sprintf("old-turn-%d", i),
				ConversationID: conversationID,
				PostID:         &pid,
				Role:           "user",
				Content:        json.RawMessage("[]"),
				Sequence:       i,
			})
		}

		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Hi"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		// The new turn is the 4th entry (index 3).
		require.Len(t, ts.turns, 4)
		require.Equal(t, 3, ts.turns[3].Sequence)
	})

	t.Run("finalizes with text block", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Hello world"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.updateCalls, 1)
		blocks := parseContentBlocks(t, ts.updateCalls[0].Content)
		require.Len(t, blocks, 1)
		require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
		require.Equal(t, "Hello world", blocks[0].Text)
	})

	t.Run("finalizes with reasoning and text blocks", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 5)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeReasoning, Value: "Let me think"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeReasoning, Value: " about this"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeReasoningEnd, Value: llm.ReasoningData{
			Text:      "Let me think about this",
			Signature: "sig123",
		}}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "The answer is 42"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.updateCalls, 1)
		blocks := parseContentBlocks(t, ts.updateCalls[0].Content)
		require.Len(t, blocks, 2)
		require.Equal(t, conversation.BlockTypeThinking, blocks[0].Type)
		require.Equal(t, "Let me think about this", blocks[0].Text)
		require.Equal(t, "sig123", blocks[0].Signature)
		require.Equal(t, conversation.BlockTypeText, blocks[1].Type)
		require.Equal(t, "The answer is 42", blocks[1].Text)
	})

	t.Run("finalizes with annotations block", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		annotations := []llm.Annotation{
			{Type: llm.AnnotationTypeURLCitation, URL: "https://example.com", Title: "Example", StartIndex: 0, EndIndex: 10, Index: 1},
		}

		streamChannel := make(chan llm.TextStreamEvent, 3)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Search results"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeAnnotations, Value: annotations}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.updateCalls, 1)
		blocks := parseContentBlocks(t, ts.updateCalls[0].Content)
		require.GreaterOrEqual(t, len(blocks), 2)
		// Find the annotations block and verify it has data.
		var annotationsBlock *conversation.ContentBlock
		for i := range blocks {
			if blocks[i].Type == conversation.BlockTypeAnnotations {
				annotationsBlock = &blocks[i]
				break
			}
		}
		require.NotNil(t, annotationsBlock, "expected an annotations block in finalized content")
		require.NotNil(t, annotationsBlock.WebSearchContext, "annotations block should have WebSearchContext")
		require.Equal(t, 1, annotationsBlock.WebSearchContext.Count)
		// Verify the results contain the annotation data.
		var parsedAnnotations []llm.Annotation
		require.NoError(t, json.Unmarshal(annotationsBlock.WebSearchContext.Results, &parsedAnnotations))
		require.Len(t, parsedAnnotations, 1)
		require.Equal(t, "https://example.com", parsedAnnotations[0].URL)
	})

	t.Run("finalizes with token usage", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 3)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Response"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 100, OutputTokens: 50}}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.tokenCalls, 1)
		require.Equal(t, int64(100), ts.tokenCalls[0].TokensIn)
		require.Equal(t, int64(50), ts.tokenCalls[0].TokensOut)
	})

	t.Run("multiple usage events are summed", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 4)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Hi"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 50, OutputTokens: 20}}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 30, OutputTokens: 10}}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.tokenCalls, 1)
		require.Equal(t, int64(80), ts.tokenCalls[0].TokensIn)
		require.Equal(t, int64(30), ts.tokenCalls[0].TokensOut)
	})

	t.Run("error persists partial content", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 3)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Partial "}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "text"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeError, Value: fmt.Errorf("upstream failure")}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.updateCalls, 1)
		blocks := parseContentBlocks(t, ts.updateCalls[0].Content)
		require.Len(t, blocks, 1)
		require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
		require.Equal(t, "Partial text", blocks[0].Text)
	})

	t.Run("cancellation persists partial content", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		ctx, cancel := context.WithCancel(context.Background())

		// Use an unbuffered channel so the goroutine below blocks until
		// StreamToPost consumes the text event, guaranteeing accumulation
		// happens before the context is canceled.
		streamChannel := make(chan llm.TextStreamEvent)

		go func() {
			streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Before cancel"}
			// Now that the text has been consumed, cancel the context.
			cancel()
		}()

		service.StreamToPost(ctx, &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.updateCalls, 1)
		blocks := parseContentBlocks(t, ts.updateCalls[0].Content)
		require.Len(t, blocks, 1)
		require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
		require.Equal(t, "Before cancel", blocks[0].Text)
	})

	t.Run("error persists partial reasoning without signature", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 3)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeReasoning, Value: "Partial reasoning"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Some text"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeError, Value: fmt.Errorf("crash")}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.updateCalls, 1)
		blocks := parseContentBlocks(t, ts.updateCalls[0].Content)
		// Expect a thinking block (partial, no signature) and a text block.
		var thinkingBlock *conversation.ContentBlock
		for i := range blocks {
			if blocks[i].Type == conversation.BlockTypeThinking {
				thinkingBlock = &blocks[i]
				break
			}
		}
		require.NotNil(t, thinkingBlock, "expected a thinking block for partial reasoning")
		require.Equal(t, "Partial reasoning", thinkingBlock.Text)
		require.Empty(t, thinkingBlock.Signature)
	})

	t.Run("no turn store skips persistence without panic", func(t *testing.T) {
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		// No SetTurnStore call — turnStore is nil.

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Hello"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		// Should not panic.
		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		require.GreaterOrEqual(t, len(client.updatedPosts), 1)
		require.Equal(t, "Hello", client.updatedPosts[len(client.updatedPosts)-1].Message)
	})

	t.Run("no conversation_id skips persistence", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		// No conversation_id prop.

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Hello"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Empty(t, ts.turns)
		require.Empty(t, ts.updateCalls)
	})

	t.Run("create turn failure does not break stream", func(t *testing.T) {
		ts := &fakeTurnStore{createErr: fmt.Errorf("db error")}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Hello"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		require.GreaterOrEqual(t, len(client.updatedPosts), 1)
		require.Equal(t, "Hello", client.updatedPosts[len(client.updatedPosts)-1].Message)
		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Empty(t, ts.updateCalls)
	})

	t.Run("update content failure is logged but stream completes", func(t *testing.T) {
		ts := &fakeTurnStore{updateErr: fmt.Errorf("update error")}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Hello"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		require.GreaterOrEqual(t, len(client.updatedPosts), 1)
		require.Equal(t, "Hello", client.updatedPosts[len(client.updatedPosts)-1].Message)
		// Placeholder was still created.
		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.turns, 1)
	})

	t.Run("DM tool calls accumulate in turn content", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		toolCalls := []llm.ToolCall{
			{ID: "tc-1", Name: "search", ServerOrigin: "https://mcp.example.com", Arguments: json.RawMessage(`{"q":"test"}`), Status: llm.ToolCallStatusSuccess},
		}

		streamChannel := make(chan llm.TextStreamEvent, 4)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Searching"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: " done"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.updateCalls, 1)
		blocks := parseContentBlocks(t, ts.updateCalls[0].Content)
		var foundToolUse bool
		for _, b := range blocks {
			if b.Type == conversation.BlockTypeToolUse {
				foundToolUse = true
				require.Equal(t, "tc-1", b.ID)
				require.Equal(t, "search", b.Name)
			}
		}
		require.True(t, foundToolUse, "expected a tool_use block in finalized DM turn")
	})

	t.Run("channel tool call persists via defer before return", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeOpen},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		toolCalls := []llm.ToolCall{
			{ID: "tc-1", Name: "read_file", ServerOrigin: "https://mcp.example.com", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
		}

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Checking"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		// Turn was created and finalized (via defer) even though the channel tool call path returns early.
		require.Len(t, ts.turns, 1)
		require.Len(t, ts.updateCalls, 1)
		blocks := parseContentBlocks(t, ts.updateCalls[0].Content)
		// Should have at least a text block for "Checking" and a tool_use block.
		var hasText, hasToolUse bool
		for _, b := range blocks {
			if b.Type == conversation.BlockTypeText {
				hasText = true
			}
			if b.Type == conversation.BlockTypeToolUse {
				hasToolUse = true
			}
		}
		require.True(t, hasText, "expected text block for partial text before tool call")
		require.True(t, hasToolUse, "expected tool_use block from channel tool call")
	})

	t.Run("conversation_id prop remains on post after streaming", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Hello"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		require.Equal(t, conversationID, post.GetProp(ConversationIDProp))
	})

	t.Run("annotations with map event accumulate web search context", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		annotations := []llm.Annotation{
			{Type: llm.AnnotationTypeURLCitation, URL: "https://example.com", Title: "Example", Index: 1},
		}
		annotationEvent := map[string]interface{}{
			"annotations":    annotations,
			"cleanedMessage": "Cleaned text",
		}

		streamChannel := make(chan llm.TextStreamEvent, 3)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Original text [1]"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeAnnotations, Value: annotationEvent}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.updateCalls, 1)
		blocks := parseContentBlocks(t, ts.updateCalls[0].Content)

		// Verify the text block uses the cleaned message, not the original with citation markers.
		var textBlock *conversation.ContentBlock
		var annotationsBlock *conversation.ContentBlock
		for i := range blocks {
			switch blocks[i].Type {
			case conversation.BlockTypeText:
				textBlock = &blocks[i]
			case conversation.BlockTypeAnnotations:
				annotationsBlock = &blocks[i]
			}
		}
		require.NotNil(t, textBlock, "expected text block")
		require.Equal(t, "Cleaned text", textBlock.Text, "text block should use the cleaned message")

		require.NotNil(t, annotationsBlock, "expected annotations block from map event")
		require.NotNil(t, annotationsBlock.WebSearchContext, "annotations block should have WebSearchContext")
		require.Equal(t, 1, annotationsBlock.WebSearchContext.Count)
		var parsedAnnotations []llm.Annotation
		require.NoError(t, json.Unmarshal(annotationsBlock.WebSearchContext.Results, &parsedAnnotations))
		require.Len(t, parsedAnnotations, 1)
		require.Equal(t, "https://example.com", parsedAnnotations[0].URL)
	})
}
