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

// fakeTurnStore implements TurnStore and records every created turn.
// Streaming now creates its assistant turn exactly once, at finalize, so
// there's no separate update path to mock.
type fakeTurnStore struct {
	mu        sync.Mutex
	turns     []*store.Turn
	createErr error
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

// parseContentBlocks is a test helper that unmarshals content JSON into content blocks.
func parseContentBlocks(t *testing.T, raw json.RawMessage) []conversation.ContentBlock {
	t.Helper()
	var blocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(raw, &blocks))
	return blocks
}

// findStreamTurn returns the assistant turn for the given streaming post, or
// nil if finalize did not persist one (e.g. turnStore was nil).
func findStreamTurn(turns []*store.Turn, postID string) *store.Turn {
	for _, tr := range turns {
		if tr.PostID != nil && *tr.PostID == postID && tr.Role == "assistant" {
			return tr
		}
	}
	return nil
}

func TestStreamToPostTurnPersistence(t *testing.T) {
	const (
		postID         = "post-id"
		channelID      = "channel-id"
		botID          = "bot-id"
		requesterID    = "requester-id"
		conversationID = "conv-id"
	)

	t.Run("creates assistant turn at stream end with accumulated content", func(t *testing.T) {
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.turns, 1)
		turn := ts.turns[0]
		require.Equal(t, "assistant", turn.Role)
		require.Equal(t, conversationID, turn.ConversationID)
		require.NotNil(t, turn.PostID)
		require.Equal(t, postID, *turn.PostID)
		// Content is the accumulated state — the turn is not created empty.
		blocks := parseContentBlocks(t, turn.Content)
		require.Len(t, blocks, 1)
		require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
		require.Equal(t, "Hello", blocks[0].Text)
		require.Equal(t, 1, turn.Sequence)
	})

	// Regression: a stream that produced tool_use (pending approval) and no
	// text must not overwrite the post with "Sorry! The LLM did not return a
	// result." — the tool cards are the result, rendered via the turn.
	t.Run("stream ending with only tool calls does not overwrite post with the empty-result message", func(t *testing.T) {
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
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
			{ID: "tc1", Name: "search", Status: llm.ToolCallStatusPending},
		}}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)

		for _, updated := range client.updatedPosts {
			require.NotContains(t, updated.Message, "did not return a result",
				"post text must stay empty when the stream produced tool calls; the tool UI renders via the turn instead")
		}
		require.Empty(t, post.Message, "post.Message should remain empty when only tool calls were produced")
	})

	// Regression: the streaming assistant turn must land AFTER the tool-round
	// turns that WriteToolTurns persists during the stream. If the turn is
	// created at stream START, it gets the low sequence and the final answer
	// appears before the rounds that produced it when history is replayed
	// on the next user message.
	t.Run("final assistant turn is sequenced AFTER tool-round turns written during stream", func(t *testing.T) {
		ts := &fakeTurnStore{}

		userPost := "user-post-id"
		ts.turns = append(ts.turns, &store.Turn{
			ID:             "u1",
			ConversationID: conversationID,
			PostID:         &userPost,
			Role:           "user",
			Sequence:       1,
			Content:        json.RawMessage("[]"),
		})

		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID}
		post.AddProp(ConversationIDProp, conversationID)

		// Unbuffered channel so every send blocks until StreamToPost reads it,
		// giving us a happens-before edge with the service goroutine.
		streamChannel := make(chan llm.TextStreamEvent)
		done := make(chan struct{})
		go func() {
			defer close(done)
			service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)
		}()

		// After the first send completes, StreamToPost has already passed
		// createPlaceholderTurn (if any) and entered its event loop.
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "ok "}

		// Simulate WriteToolTurns persisting a round mid-stream.
		require.NoError(t, ts.CreateTurnAutoSequence(&store.Turn{
			ID:             "a1",
			ConversationID: conversationID,
			Role:           "assistant",
			Content:        json.RawMessage(`[{"type":"tool_use","id":"tc1","name":"search"}]`),
		}))
		require.NoError(t, ts.CreateTurnAutoSequence(&store.Turn{
			ID:             "tr1",
			ConversationID: conversationID,
			Role:           "tool_result",
			Content:        json.RawMessage(`[{"type":"tool_result","tool_use_id":"tc1","content":"r1"}]`),
		}))

		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "final answer"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)
		<-done

		ts.mu.Lock()
		defer ts.mu.Unlock()

		var streamTurn *store.Turn
		for _, tr := range ts.turns {
			if tr.PostID != nil && *tr.PostID == postID {
				streamTurn = tr
			}
		}
		require.NotNil(t, streamTurn, "expected an assistant turn linked to the streaming post")

		for _, tr := range ts.turns {
			if tr == streamTurn {
				continue
			}
			require.Less(t, tr.Sequence, streamTurn.Sequence,
				"turn id=%s role=%s seq=%d should have a lower sequence than the final streaming turn seq=%d",
				tr.ID, tr.Role, tr.Sequence, streamTurn.Sequence,
			)
		}
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		require.Equal(t, int64(100), streamTurn.TokensIn)
		require.Equal(t, int64(50), streamTurn.TokensOut)
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		require.Equal(t, int64(80), streamTurn.TokensIn)
		require.Equal(t, int64(30), streamTurn.TokensOut)
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)
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

		service.StreamToPost(ctx, &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)
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
		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Empty(t, ts.turns)
	})

	t.Run("create turn failure is logged but stream completes", func(t *testing.T) {
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		require.GreaterOrEqual(t, len(client.updatedPosts), 1)
		require.Equal(t, "Hello", client.updatedPosts[len(client.updatedPosts)-1].Message)
		// Create errored, so no turn was persisted. The streaming post still
		// updates so the user sees the text — the DB write failure is logged
		// but non-fatal.
		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Empty(t, ts.turns)
	})

	t.Run("resolved tool call in DM does not land on placeholder turn", func(t *testing.T) {
		// Tool rounds are persisted as their own turns by
		// conversation.Service.WriteToolTurns; the placeholder turn represents
		// only the final bot-response post. On the post-execution "resolved"
		// event the accumulator must drop the round's text, reasoning and
		// tool calls so none of it leaks onto the placeholder.
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

		pending := []llm.ToolCall{
			{ID: "tc-1", Name: "search", ServerOrigin: "https://mcp.example.com", Arguments: json.RawMessage(`{"q":"test"}`), Status: llm.ToolCallStatusPending},
		}
		resolved := []llm.ToolCall{
			{ID: "tc-1", Name: "search", ServerOrigin: "https://mcp.example.com", Arguments: json.RawMessage(`{"q":"test"}`), Status: llm.ToolCallStatusAutoApproved},
		}

		streamChannel := make(chan llm.TextStreamEvent, 5)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Searching"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: pending}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: resolved}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Done"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)

		for _, b := range blocks {
			require.NotEqual(t, conversation.BlockTypeToolUse, b.Type,
				"resolved tool_use leaked onto the final placeholder turn")
		}

		var textBlock *conversation.ContentBlock
		for i := range blocks {
			if blocks[i].Type == conversation.BlockTypeText {
				textBlock = &blocks[i]
				break
			}
		}
		require.NotNil(t, textBlock, "expected a text block with the final round's text")
		require.Equal(t, "Done", textBlock.Text, "placeholder must hold only the final round's text")
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		// Turn was created and finalized (via defer) even though the channel tool call path returns early.
		require.Len(t, ts.turns, 1)
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)
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

	// Regression: group DMs follow the channel share-flow (the rest of the
	// codebase treats them as non-DM via mmapi.IsDMWith). The final assistant
	// turn's tool_use blocks must not be marked shared=true in a group DM,
	// otherwise their inputs become visible to other members without the
	// requester approving a Share.
	t.Run("tool_use in group DM is not marked shared", func(t *testing.T) {
		ts := &fakeTurnStore{}
		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeGroup, Name: "group-channel"},
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
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: toolCalls}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)

		var toolUseBlock *conversation.ContentBlock
		for i := range blocks {
			if blocks[i].Type == conversation.BlockTypeToolUse {
				toolUseBlock = &blocks[i]
				break
			}
		}
		require.NotNil(t, toolUseBlock, "expected a tool_use block")
		require.NotNil(t, toolUseBlock.Shared, "Shared flag should be set on tool_use block")
		require.False(t, *toolUseBlock.Shared,
			"group-channel tool_use must not be marked shared — group DMs follow the channel share flow")
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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

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

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)

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

	// Reproducer: a stream that produces no text/reasoning/tool_calls should
	// finalize to "[]" so the webapp can safely iterate `turn.content`. The
	// current implementation marshals a nil slice to "null" instead, which
	// crashes the webapp with "turn.content is null".
	t.Run("empty stream finalizes to empty array not null", func(t *testing.T) {
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

		streamChannel := make(chan llm.TextStreamEvent, 1)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		// The persisted content must be a JSON array so the webapp can iterate it.
		require.NotEqual(t, "null", string(streamTurn.Content),
			"empty stream must not persist literal null; webapp crashes on turn.content.filter")
		blocks := parseContentBlocks(t, streamTurn.Content)
		require.Empty(t, blocks)
	})

	// Reproducer: when the ToolRunner emits multiple rounds of tool calls on
	// the same stream (round 1: text + tool_calls, round 2: text), the
	// placeholder turn (which is bound to the final bot-response post) must
	// end up with only the FINAL round's text/reasoning/tool_calls. Otherwise
	// intermediate round state leaks onto the final post — the UI shows
	// concatenated text and only the last round's tool call.
	t.Run("multi-round tool calls do not leak into placeholder turn", func(t *testing.T) {
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

		// ToolRunner emits each round as (pending event → execute → resolved
		// event). The pending event carries whatever status the LLM stream
		// produced (Pending); buildResolvedToolCalls re-tags successful
		// auto-run calls as AutoApproved and errored ones as Error.
		round1Pending := []llm.ToolCall{
			{ID: "tc-round-1", Name: "search", Arguments: json.RawMessage(`{"q":"AOC alerts"}`), Status: llm.ToolCallStatusPending},
		}
		round1Resolved := []llm.ToolCall{
			{ID: "tc-round-1", Name: "search", Arguments: json.RawMessage(`{"q":"AOC alerts"}`), Status: llm.ToolCallStatusAutoApproved},
		}
		round2Pending := []llm.ToolCall{
			{ID: "tc-round-2", Name: "search", Arguments: json.RawMessage(`{"q":"alerts"}`), Status: llm.ToolCallStatusPending},
		}
		round2Resolved := []llm.ToolCall{
			{ID: "tc-round-2", Name: "search", Arguments: json.RawMessage(`{"q":"alerts"}`), Status: llm.ToolCallStatusAutoApproved},
		}

		// Two rounds of (text + tool_calls) followed by a final round with text only.
		streamChannel := make(chan llm.TextStreamEvent, 8)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Let me look that up for you!"}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: round1Pending}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: round1Resolved}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: round2Pending}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: round2Resolved}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "I wasn't able to find that channel."}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", "test-user-id")

		ts.mu.Lock()
		defer ts.mu.Unlock()
		streamTurn := findStreamTurn(ts.turns, postID)
		require.NotNil(t, streamTurn)
		blocks := parseContentBlocks(t, streamTurn.Content)

		// The placeholder turn represents only the final bot-response post.
		// Intermediate-round tool calls are persisted separately by
		// conversation.Service.WriteToolTurns, so they must NOT appear here.
		for _, b := range blocks {
			require.NotEqual(t, conversation.BlockTypeToolUse, b.Type,
				"intermediate-round tool_use block leaked onto final placeholder turn")
		}

		// Final text should only reflect the final round, not a concatenation
		// of all rounds' text. "Let me look that up for you!" came from round
		// 1 and should have been reset once round 1 was persisted via
		// WriteToolTurns.
		var textBlock *conversation.ContentBlock
		for i := range blocks {
			if blocks[i].Type == conversation.BlockTypeText {
				textBlock = &blocks[i]
				break
			}
		}
		require.NotNil(t, textBlock, "expected final text block")
		require.Equal(t, "I wasn't able to find that channel.", textBlock.Text,
			"final placeholder turn must contain only the final round's text")
	})
}
