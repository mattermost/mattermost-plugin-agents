// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
// Streaming creates its assistant turn at finalize; for continuation streams
// it also looks up and demotes the prior anchor turn for the same post.
type fakeTurnStore struct {
	mu        sync.Mutex
	turns     []*store.Turn
	createErr error
	lookupErr error
	demoteErr error
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

func (f *fakeTurnStore) GetTurnByPostID(postID string) (*store.Turn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	for _, t := range f.turns {
		if t.PostID != nil && *t.PostID == postID {
			return t, nil
		}
	}
	return nil, nil
}

func (f *fakeTurnStore) UpdateTurnPostID(id string, postID *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.demoteErr != nil {
		return f.demoteErr
	}
	for _, t := range f.turns {
		if t.ID == id {
			t.PostID = postID
			return nil
		}
	}
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

	t.Run("error persists partial content followed by the error fallback", func(t *testing.T) {
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
		// Partial text is preserved AND the error fallback is appended so
		// the persisted turn carries the user-visible message — without
		// this the round renders empty after the post-end refetch.
		require.Contains(t, blocks[0].Text, "Partial text")
		require.Contains(t, blocks[0].Text, "An error occurred")
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

	// Reproducer: a stream that produces no text/reasoning/tool_calls and
	// no tool_use should finalize with the empty-result fallback text in
	// the turn so the webapp's per-round renderer has something to show
	// instead of an empty round, and the persisted content is always a
	// JSON array (never null — webapp crashes on turn.content.filter).
	t.Run("empty stream finalizes with the LLM-no-result fallback text", func(t *testing.T) {
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
		require.NotEqual(t, "null", string(streamTurn.Content),
			"persisted content must be a JSON array; webapp crashes on turn.content.filter")
		blocks := parseContentBlocks(t, streamTurn.Content)
		require.Len(t, blocks, 1)
		require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
		require.Contains(t, blocks[0].Text, "did not return a result")
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

	// Continuation: when StreamToPost runs against a post that already has an
	// assistant turn anchored to it (the user just approved tool calls and we
	// re-stream onto the SAME post), the prior anchor must be demoted at
	// finalize so the webapp's post→anchor lookup still resolves to exactly
	// one match.
	t.Run("continuation stream demotes the prior anchor and emits continue control", func(t *testing.T) {
		ts := &fakeTurnStore{}

		// Seed a prior anchor turn — analogous to one written by the first
		// stream's finalize when it ended on pending tool_use blocks.
		priorPostIDCopy := postID
		priorContent, mErr := json.Marshal([]conversation.ContentBlock{
			{Type: conversation.BlockTypeText, Text: "Let me search for that."},
			{Type: conversation.BlockTypeToolUse, ID: "tc1", Name: "search", Status: conversation.StatusSuccess},
		})
		require.NoError(t, mErr)
		ts.turns = append(ts.turns, &store.Turn{
			ID:             "prior-anchor",
			ConversationID: conversationID,
			PostID:         &priorPostIDCopy,
			Role:           "assistant",
			Sequence:       2,
			Content:        priorContent,
		})

		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		post := &model.Post{Id: postID, ChannelId: channelID, UserId: botID, Message: "Let me search for that."}
		post.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Found 5 channels."}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamContinuationToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)

		ts.mu.Lock()
		defer ts.mu.Unlock()

		// Prior anchor lost its post link.
		var prior *store.Turn
		for _, t := range ts.turns {
			if t.ID == "prior-anchor" {
				prior = t
				break
			}
		}
		require.NotNil(t, prior)
		require.Nil(t, prior.PostID, "prior anchor must be demoted on continuation")

		// New anchor turn carries the post link and the new round's content.
		require.Len(t, ts.turns, 2)
		var newAnchor *store.Turn
		for _, t := range ts.turns {
			if t.ID != "prior-anchor" {
				newAnchor = t
				break
			}
		}
		require.NotNil(t, newAnchor)
		require.NotNil(t, newAnchor.PostID)
		require.Equal(t, postID, *newAnchor.PostID)
		blocks := parseContentBlocks(t, newAnchor.Content)
		require.Len(t, blocks, 1)
		require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
		require.Equal(t, "Found 5 channels.", blocks[0].Text)

		// Continue control event was sent instead of start.
		var sawContinue, sawStart bool
		for _, ev := range client.events {
			if ev.event != "postupdate" {
				continue
			}
			if ctrl, ok := ev.payload["control"].(string); ok {
				switch ctrl {
				case PostStreamingControlContinue:
					sawContinue = true
				case PostStreamingControlStart:
					sawStart = true
				}
			}
		}
		require.True(t, sawContinue, "continuation must emit a continue control event")
		require.False(t, sawStart, "continuation must not emit start (the webapp would clear tool cards)")
	})

	// Continuation detection must only fire on a prior ASSISTANT turn for
	// THIS post. Each of these table cases would silently regress the
	// detection guard if the role/post-id checks were removed.
	t.Run("continuation detection guards", func(t *testing.T) {
		userPostIDCopy := "user-post-id"
		unrelatedPostIDCopy := "other-post-id"
		ourPostCopy := postID

		cases := []struct {
			name string
			seed []*store.Turn
			want string
		}{
			{
				name: "empty turn store",
				seed: nil,
				want: PostStreamingControlStart,
			},
			{
				name: "user turn with the same post_id (must NOT trigger continue)",
				seed: []*store.Turn{{
					ID:             "u1",
					ConversationID: conversationID,
					PostID:         &ourPostCopy,
					Role:           "user",
					Sequence:       1,
					Content:        json.RawMessage(`[]`),
				}},
				want: PostStreamingControlStart,
			},
			{
				name: "assistant turn for an unrelated post (must NOT trigger continue)",
				seed: []*store.Turn{{
					ID:             "a1",
					ConversationID: conversationID,
					PostID:         &unrelatedPostIDCopy,
					Role:           "assistant",
					Sequence:       1,
					Content:        json.RawMessage(`[]`),
				}},
				want: PostStreamingControlStart,
			},
			{
				name: "user turn for a different post (must NOT trigger continue)",
				seed: []*store.Turn{{
					ID:             "u1",
					ConversationID: conversationID,
					PostID:         &userPostIDCopy,
					Role:           "user",
					Sequence:       1,
					Content:        json.RawMessage(`[]`),
				}},
				want: PostStreamingControlStart,
			},
			{
				name: "assistant turn anchored to this post (the only case that triggers continue)",
				seed: []*store.Turn{{
					ID:             "a1",
					ConversationID: conversationID,
					PostID:         &ourPostCopy,
					Role:           "assistant",
					Sequence:       1,
					Content:        json.RawMessage(`[]`),
				}},
				want: PostStreamingControlContinue,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ts := &fakeTurnStore{}
				ts.turns = append(ts.turns, tc.seed...)

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

				service.StreamContinuationToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)

				var control string
				for _, ev := range client.events {
					if ctrl, ok := ev.payload["control"].(string); ok {
						switch ctrl {
						case PostStreamingControlStart, PostStreamingControlContinue:
							control = ctrl
						}
					}
				}
				require.Equal(t, tc.want, control)
			})
		}
	})

	// A lookup error must not crash StreamToPost or hang the stream — it
	// should fall through to a normal start. Without this guard, a transient
	// DB blip would corrupt every concurrent stream on the node.
	t.Run("lookup error falls through to start without crashing", func(t *testing.T) {
		ts := &fakeTurnStore{lookupErr: fmt.Errorf("transient db error")}
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

		service.StreamContinuationToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)

		var sawStart bool
		for _, ev := range client.events {
			if ctrl, ok := ev.payload["control"].(string); ok && ctrl == PostStreamingControlStart {
				sawStart = true
			}
		}
		require.True(t, sawStart, "a lookup error must not silence the start control event — the webapp depends on it")
	})

	// On resolved tool calls the WS event ordering must let the webapp
	// snapshot the round's preamble text BEFORE seeing the resolved
	// tool_call event. Concretely: between the round's text events and the
	// resolved tool_call event, no event may clear the message (next: "").
	// The webapp's live round snapshot reads messageRef.current at the
	// moment the resolved event arrives — so anything that mutates that
	// state in between would drop the round's preamble.
	t.Run("text events for a round are not erased before the resolved tool_call event", func(t *testing.T) {
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
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Let me search."}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
			{ID: "tc1", Name: "search", Status: llm.ToolCallStatusPending},
		}}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: []llm.ToolCall{
			{ID: "tc1", Name: "search", Status: llm.ToolCallStatusAutoApproved},
		}}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Found 5."}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)

		// Walk the broadcast events and find the resolved tool_call event.
		// Verify (a) no `next: ""` is broadcast at any point in the stream,
		// (b) the LAST `next` value before the resolved tool_call carries
		// the round's preamble — proving the webapp's resolved-event
		// snapshot would observe it.
		resolvedIdx := -1
		var lastNextBeforeResolved string
		resolvedJSONNeedle := fmt.Sprintf(`"status":%d`, llm.ToolCallStatusAutoApproved)
		for i, ev := range client.events {
			if ev.event != "postupdate" {
				continue
			}
			if next, ok := ev.payload["next"].(string); ok {
				require.NotEqual(t, "", next,
					"no postupdate may carry next: \"\" — that erases the round's text before the webapp can snapshot it on the resolved tool_call event")
				if resolvedIdx == -1 {
					lastNextBeforeResolved = next
				}
			}
			if ctrl, ok := ev.payload["control"].(string); ok && ctrl == "tool_call" {
				if tcJSON, ok := ev.payload["tool_call"].(string); ok && resolvedIdx == -1 {
					if strings.Contains(tcJSON, resolvedJSONNeedle) {
						resolvedIdx = i
					}
				}
			}
		}
		require.NotEqual(t, -1, resolvedIdx, "expected a resolved tool_call event in the broadcast stream")
		require.Equal(t, "Let me search.", lastNextBeforeResolved,
			"the round's preamble must still be the broadcast post message at the moment the resolved tool_call event arrives — otherwise the webapp's snapshot has no text to attribute to this round")
	})

	// Cross-post isolation: a finalize against post B must not touch post A's
	// anchor turn even though they share a conversation. The demote logic
	// keys on post.Id, but a subtle regression (e.g. demoting the first
	// assistant turn rather than the matching one) would silently break
	// multi-post threads where each post is the anchor of its own response.
	t.Run("finalize on one post leaves other posts' anchors intact", func(t *testing.T) {
		const postA = "post-a"
		const postB = "post-b"

		ts := &fakeTurnStore{}

		// Seed post A with its own anchor turn — analogous to a prior
		// response in the same conversation thread.
		postACopy := postA
		anchorAContent, mErr := json.Marshal([]conversation.ContentBlock{
			{Type: conversation.BlockTypeText, Text: "Post A's response."},
		})
		require.NoError(t, mErr)
		ts.turns = append(ts.turns, &store.Turn{
			ID:             "anchor-a",
			ConversationID: conversationID,
			PostID:         &postACopy,
			Role:           "assistant",
			Sequence:       2,
			Content:        anchorAContent,
		})

		client := &fakeStreamingClient{
			channels: map[string]*model.Channel{
				channelID: {Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID},
			},
		}
		service := NewMMPostStreamService(client, i18n.Init())
		service.SetTurnStore(ts)

		// Stream a response to post B. The streaming layer's lookup keys on
		// post.Id, so finalize must demote nothing for post B (no prior
		// anchor exists for B) and leave post A's anchor untouched.
		postBPost := &model.Post{Id: postB, ChannelId: channelID, UserId: botID}
		postBPost.AddProp(ConversationIDProp, conversationID)

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Post B's response."}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, postBPost, "en", requesterID)

		ts.mu.Lock()
		defer ts.mu.Unlock()
		require.Len(t, ts.turns, 2, "expected post A's anchor + post B's new anchor — nothing else demoted")

		// Post A's anchor must still carry its post link.
		var aAfter, bAfter *store.Turn
		for _, tr := range ts.turns {
			if tr.ID == "anchor-a" {
				aAfter = tr
				continue
			}
			bAfter = tr
		}
		require.NotNil(t, aAfter)
		require.NotNil(t, aAfter.PostID, "post A's anchor must NOT be demoted by an unrelated post's finalize")
		require.Equal(t, postA, *aAfter.PostID)

		require.NotNil(t, bAfter)
		require.NotNil(t, bAfter.PostID)
		require.Equal(t, postB, *bAfter.PostID, "post B's new anchor carries its own post link")
	})

	// Operation order: finalize must DEMOTE the prior anchor before creating
	// the new one. If the order is wrong, two turns with the same post_id
	// briefly exist; the webapp's findIndex(post_id == postId) lookup
	// nondeterministically returns one of them, hiding either the new
	// content or the resolved tool_use blocks. Catching this requires
	// recording the call sequence into the store, not just the terminal
	// state.
	t.Run("finalize demotes the prior anchor before creating the new one", func(t *testing.T) {
		ts := &fakeOrderingTurnStore{
			fakeTurnStore: fakeTurnStore{},
		}

		// Seed a prior anchor.
		priorPostIDCopy := postID
		ts.turns = append(ts.turns, &store.Turn{
			ID:             "prior",
			ConversationID: conversationID,
			PostID:         &priorPostIDCopy,
			Role:           "assistant",
			Sequence:       2,
			Content:        json.RawMessage(`[]`),
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

		streamChannel := make(chan llm.TextStreamEvent, 2)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Continuation."}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamContinuationToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)

		ts.mu.Lock()
		defer ts.mu.Unlock()

		// At minimum: lookup, then demote (UpdateTurnPostID on prior), then
		// create (CreateTurnAutoSequence) — in that order. The lookup at
		// stream START is acceptable; what matters is that demote of the
		// existing turn precedes the create of the new one.
		demoteIdx, createIdx := -1, -1
		for i, op := range ts.ops {
			if op == "demote:prior" && demoteIdx == -1 {
				demoteIdx = i
			}
			if op == "create" && createIdx == -1 {
				createIdx = i
			}
		}
		require.NotEqual(t, -1, demoteIdx, "expected a demote (UpdateTurnPostID) on prior turn")
		require.NotEqual(t, -1, createIdx, "expected a create (CreateTurnAutoSequence) of the new anchor")
		require.Less(t, demoteIdx, createIdx, "demote of prior anchor must run BEFORE creating the new anchor")
	})

	// After the regen-unification: regen first scrubs the prior anchor and
	// intermediates via DeleteResponseTurns at the caller, then runs
	// StreamToPost identically to a first stream. Verify finalize creates a
	// fresh anchor and never demotes (demote is the continuation flow's job
	// — taking it on plain regen would resurface the prior response as a
	// rendered "round" alongside the new one).
	t.Run("regen via StreamToPost (with no prior anchor present) creates a fresh anchor and does not demote", func(t *testing.T) {
		ts := &fakeOrderingTurnStore{
			fakeTurnStore: fakeTurnStore{},
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

		streamChannel := make(chan llm.TextStreamEvent, 3)
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Regenerated answer."}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeUsage, Value: llm.TokenUsage{InputTokens: 5, OutputTokens: 10}}
		streamChannel <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(streamChannel)

		service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en", requesterID)

		ts.mu.Lock()
		defer ts.mu.Unlock()

		// Exactly one assistant turn for this post — freshly created.
		var anchors []*store.Turn
		for _, tr := range ts.turns {
			if tr.PostID != nil && *tr.PostID == postID && tr.Role == "assistant" {
				anchors = append(anchors, tr)
			}
		}
		require.Len(t, anchors, 1, "regen must produce exactly one anchor for the post")
		blocks := parseContentBlocks(t, anchors[0].Content)
		require.Len(t, blocks, 1)
		require.Equal(t, "Regenerated answer.", blocks[0].Text)
		require.Equal(t, int64(5), anchors[0].TokensIn)
		require.Equal(t, int64(10), anchors[0].TokensOut)

		require.Contains(t, ts.ops, "create", "regen must create a fresh anchor")
		for _, op := range ts.ops {
			require.False(t, strings.HasPrefix(op, "demote:"), "regen via StreamToPost must not demote anything")
		}
	})
}

// fakeOrderingTurnStore extends fakeTurnStore to record the sequence of
// operations performed against it, so tests can assert on call ordering
// (e.g. demote-before-create) rather than only terminal state.
type fakeOrderingTurnStore struct {
	fakeTurnStore
	ops []string
}

func (f *fakeOrderingTurnStore) CreateTurnAutoSequence(turn *store.Turn) error {
	if err := f.fakeTurnStore.CreateTurnAutoSequence(turn); err != nil {
		return err
	}
	f.mu.Lock()
	f.ops = append(f.ops, "create")
	f.mu.Unlock()
	return nil
}

func (f *fakeOrderingTurnStore) UpdateTurnPostID(id string, postID *string) error {
	if err := f.fakeTurnStore.UpdateTurnPostID(id, postID); err != nil {
		return err
	}
	f.mu.Lock()
	f.ops = append(f.ops, "demote:"+id)
	f.mu.Unlock()
	return nil
}

func (f *fakeOrderingTurnStore) GetTurnByPostID(postID string) (*store.Turn, error) {
	turn, err := f.fakeTurnStore.GetTurnByPostID(postID)
	f.mu.Lock()
	f.ops = append(f.ops, "lookup")
	f.mu.Unlock()
	return turn, err
}
