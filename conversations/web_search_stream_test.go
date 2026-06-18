// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmtools"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func drainTextStreamEvents(t *testing.T, stream *llm.TextStreamResult) []llm.TextStreamEvent {
	t.Helper()
	require.NotNil(t, stream)

	var events []llm.TextStreamEvent
	for event := range stream.Stream {
		events = append(events, event)
	}
	return events
}

func testWebSearchApprovalContext() *llm.Context {
	return &llm.Context{
		Parameters: map[string]interface{}{
			mmtools.WebSearchContextKey: []mmtools.WebSearchContextValue{{
				Query: "typescript tutorial",
				Results: []mmtools.WebSearchResult{{
					Index:   1,
					Title:   "TypeScript Handbook",
					URL:     "https://www.typescriptlang.org/docs/",
					Snippet: "Learn TypeScript",
				}},
			}},
		},
	}
}

func TestDecorateStreamWithWebSearchAnnotations(t *testing.T) {
	t.Run("returns original stream when ctx is nil", func(t *testing.T) {
		stream := llm.NewStreamFromString("plain answer")
		assert.Same(t, stream, decorateStreamWithWebSearchAnnotations(stream, nil))
	})

	t.Run("returns original stream when ctx has no web search data", func(t *testing.T) {
		stream := llm.NewStreamFromString("plain answer")
		ctx := &llm.Context{Parameters: map[string]interface{}{}}
		assert.Same(t, stream, decorateStreamWithWebSearchAnnotations(stream, ctx))
	})

	t.Run("emits annotation events for citation markers", func(t *testing.T) {
		stream := llm.NewStreamFromString("Answer from !!CITE1!! source.")
		decorated := decorateStreamWithWebSearchAnnotations(stream, testWebSearchApprovalContext())
		require.NotSame(t, stream, decorated)

		events := drainTextStreamEvents(t, decorated)
		require.NotEmpty(t, events)
		require.Equal(t, llm.EventTypeEnd, events[len(events)-1].Type)

		var annotationEvent *llm.TextStreamEvent
		for i := range events {
			if events[i].Type == llm.EventTypeAnnotations {
				annotationEvent = &events[i]
				break
			}
		}
		require.NotNil(t, annotationEvent, "expected annotation event before stream end")

		payload, ok := annotationEvent.Value.(map[string]interface{})
		require.True(t, ok)
		annotations, ok := payload["annotations"].([]llm.Annotation)
		require.True(t, ok)
		require.Len(t, annotations, 1)
		assert.Equal(t, llm.AnnotationTypeURLCitation, annotations[0].Type)
		assert.Equal(t, "https://www.typescriptlang.org/docs/", annotations[0].URL)
	})
}

type citationFollowUpLLM struct {
	response string
}

func (l *citationFollowUpLLM) ChatCompletion(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	return llm.NewStreamFromString(l.response), nil
}

func (l *citationFollowUpLLM) ChatCompletionNoStream(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (string, error) {
	return "title", nil
}

func (l *citationFollowUpLLM) CountTokens(_ context.Context, _ llm.CompletionRequest, _ ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}

func (l *citationFollowUpLLM) InputTokenLimit() int  { return 100000 }
func (l *citationFollowUpLLM) OutputTokenLimit() int { return 8192 }

type streamCaptureStreamingService struct {
	loadedStateStreamingService
	mu     sync.Mutex
	events []llm.TextStreamEvent
}

func (s *streamCaptureStreamingService) StreamContinuationToPost(_ context.Context, stream *llm.TextStreamResult, _ *model.Post, _, _ string) {
	defer s.wg.Done()
	for event := range stream.Stream {
		s.mu.Lock()
		s.events = append(s.events, event)
		s.mu.Unlock()
	}
}

func (s *streamCaptureStreamingService) capturedEvents() []llm.TextStreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]llm.TextStreamEvent, len(s.events))
	copy(out, s.events)
	return out
}

func TestStreamToolFollowUpDecoratesWebSearchAnnotations(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	nextSeq := 1
	seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)

	lm := &citationFollowUpLLM{response: "Summary with !!CITE1!! citation."}
	bot := loadedStateBot(lm)
	streamingService := &streamCaptureStreamingService{}
	c := &Conversations{
		contextBuilder:   loadedStateBuilder(t),
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
		testWebSearchApprovalContext(),
	)
	require.NoError(t, err)
	streamingService.waitForStreaming()

	var sawAnnotations bool
	for _, event := range streamingService.capturedEvents() {
		if event.Type == llm.EventTypeAnnotations {
			sawAnnotations = true
			break
		}
	}
	assert.True(t, sawAnnotations, "approval follow-up stream should include citation annotations")
}

func TestStreamToolFollowUpSkipsAnnotationDecorationWithoutApprovalContext(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	nextSeq := 1
	seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)

	lm := &citationFollowUpLLM{response: "Summary with !!CITE1!! citation."}
	bot := loadedStateBot(lm)
	streamingService := &streamCaptureStreamingService{}
	c := &Conversations{
		contextBuilder:   loadedStateBuilder(t),
		convService:      conversation.NewService(convStore, nil, nil, nil),
		streamingService: streamingService,
	}

	err := c.streamToolFollowUp(
		context.Background(),
		bot,
		&model.User{Id: "user-id", Username: "user"},
		&model.Channel{Id: "channel-id", Type: model.ChannelTypeOpen, TeamId: "team-id"},
		&model.Post{Id: "root-post-id"},
		conv,
		false,
		nil,
	)
	require.NoError(t, err)
	streamingService.waitForStreaming()

	for _, event := range streamingService.capturedEvents() {
		assert.NotEqual(t, llm.EventTypeAnnotations, event.Type,
			"HandleToolResult path passes nil approvalContext, so citations are not decorated")
	}
}
