// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package search

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/require"
)

func TestBuildSearchAnnotationsAndCleanText(t *testing.T) {
	results := []RAGResult{
		{
			Index:       1,
			PostID:      "post1",
			ChannelID:   "channel1",
			ChannelName: "General",
			Username:    "john",
			Content:     "This is the content from post 1",
		},
		{
			Index:       2,
			PostID:      "post2",
			ChannelID:   "channel2",
			ChannelName: "Engineering",
			Username:    "jane",
			Content:     "This is the content from post 2",
		},
		{
			Index:       3,
			PostID:      "post3",
			ChannelID:   "channel3",
			ChannelName: "Design",
			Username:    "bob",
			Content:     "This is the content from post 3",
		},
	}

	tests := []struct {
		name                   string
		message                string
		results                []RAGResult
		expectedAnnotations    int
		expectedCleanedMessage string
		verifyAnnotations      func(t *testing.T, annotations []llm.Annotation)
	}{
		{
			name:                   "empty message",
			message:                "",
			results:                results,
			expectedAnnotations:    0,
			expectedCleanedMessage: "",
		},
		{
			name:                   "empty results",
			message:                "Some text !!CITE1!! here",
			results:                []RAGResult{},
			expectedAnnotations:    0,
			expectedCleanedMessage: "Some text !!CITE1!! here",
		},
		{
			name:                   "no markers in message",
			message:                "This is plain text without any citations.",
			results:                results,
			expectedAnnotations:    0,
			expectedCleanedMessage: "This is plain text without any citations.",
		},
		{
			name:                   "single citation",
			message:                "Here is some text !!CITE1!! and more text.",
			results:                results,
			expectedAnnotations:    1,
			expectedCleanedMessage: "Here is some text  and more text.",
			verifyAnnotations: func(t *testing.T, annotations []llm.Annotation) {
				require.Equal(t, llm.AnnotationTypePostCitation, annotations[0].Type)
				require.Equal(t, 1, annotations[0].Index)
				require.Equal(t, "post1", annotations[0].PostID)
				require.Equal(t, "channel1", annotations[0].ChannelID)
				require.Equal(t, "General", annotations[0].ChannelName)
				require.Equal(t, "john", annotations[0].Username)
				require.Equal(t, annotations[0].StartIndex, annotations[0].EndIndex, "should be zero-width")
			},
		},
		{
			name:                   "multiple citations",
			message:                "First !!CITE1!! second !!CITE2!! third !!CITE3!! end.",
			results:                results,
			expectedAnnotations:    3,
			expectedCleanedMessage: "First  second  third  end.",
			verifyAnnotations: func(t *testing.T, annotations []llm.Annotation) {
				require.Equal(t, 1, annotations[0].Index)
				require.Equal(t, 2, annotations[1].Index)
				require.Equal(t, 3, annotations[2].Index)
				// Indices should be increasing
				require.Less(t, annotations[0].StartIndex, annotations[1].StartIndex)
				require.Less(t, annotations[1].StartIndex, annotations[2].StartIndex)
			},
		},
		{
			name:                   "duplicate citations",
			message:                "First mention !!CITE1!! and second mention !!CITE1!! again.",
			results:                results,
			expectedAnnotations:    2,
			expectedCleanedMessage: "First mention  and second mention  again.",
			verifyAnnotations: func(t *testing.T, annotations []llm.Annotation) {
				require.Equal(t, 1, annotations[0].Index)
				require.Equal(t, 1, annotations[1].Index)
			},
		},
		{
			name:                   "citation at start",
			message:                "!!CITE1!! starts with citation.",
			results:                results,
			expectedAnnotations:    1,
			expectedCleanedMessage: " starts with citation.",
			verifyAnnotations: func(t *testing.T, annotations []llm.Annotation) {
				require.Equal(t, 0, annotations[0].StartIndex)
			},
		},
		{
			name:                   "citation at end",
			message:                "Ends with citation !!CITE2!!",
			results:                results,
			expectedAnnotations:    1,
			expectedCleanedMessage: "Ends with citation ",
		},
		{
			name:                   "invalid index ignored",
			message:                "Valid !!CITE1!! and invalid !!CITE99!! here.",
			results:                results,
			expectedAnnotations:    1,
			expectedCleanedMessage: "Valid  and invalid !!CITE99!! here.",
		},
		{
			name:                   "malformed marker - no number",
			message:                "This has !!CITE!! without number.",
			results:                results,
			expectedAnnotations:    0,
			expectedCleanedMessage: "This has !!CITE!! without number.",
		},
		{
			name:                   "malformed marker - no closing",
			message:                "This has !!CITE1 without closing.",
			results:                results,
			expectedAnnotations:    0,
			expectedCleanedMessage: "This has !!CITE1 without closing.",
		},
		{
			name:                   "UTF-8 characters",
			message:                "Unicode text 你好 !!CITE1!! más text emoji here !!CITE2!! end.",
			results:                results,
			expectedAnnotations:    2,
			expectedCleanedMessage: "Unicode text 你好  más text emoji here  end.",
			verifyAnnotations: func(t *testing.T, annotations []llm.Annotation) {
				// First annotation should be after "Unicode text 你好 " (16 runes: 13 + 2 + 1)
				require.Equal(t, 16, annotations[0].StartIndex)
				require.Greater(t, annotations[1].StartIndex, annotations[0].EndIndex)
			},
		},
		{
			name:                   "consecutive citations",
			message:                "Text !!CITE1!!!!CITE2!! more.",
			results:                results,
			expectedAnnotations:    2,
			expectedCleanedMessage: "Text  more.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			annotations, cleanedMessage := buildSearchAnnotationsAndCleanText(tc.message, tc.results)

			require.Len(t, annotations, tc.expectedAnnotations)
			require.Equal(t, tc.expectedCleanedMessage, cleanedMessage)

			if tc.verifyAnnotations != nil {
				tc.verifyAnnotations(t, annotations)
			}

			// Verify all annotations are zero-width
			for _, ann := range annotations {
				require.Equal(t, ann.StartIndex, ann.EndIndex, "annotations should be zero-width")
				require.Equal(t, llm.AnnotationTypePostCitation, ann.Type)
			}
		})
	}
}

func TestDecorateSearchStreamWithAnnotations(t *testing.T) {
	t.Run("nil result returns nil", func(t *testing.T) {
		result := DecorateSearchStreamWithAnnotations(nil, []RAGResult{{Index: 1}})
		require.Nil(t, result)
	})

	t.Run("empty results returns original stream", func(t *testing.T) {
		inputStream := make(chan llm.TextStreamEvent, 1)
		inputStream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "test"}
		close(inputStream)

		result := DecorateSearchStreamWithAnnotations(&llm.TextStreamResult{Stream: inputStream}, []RAGResult{})
		require.NotNil(t, result)
		// The function returns the original stream when results are empty
	})

	t.Run("decorates stream with annotations", func(t *testing.T) {
		results := []RAGResult{
			{Index: 1, PostID: "post1", ChannelID: "ch1", ChannelName: "General", Username: "user1"},
		}

		inputStream := make(chan llm.TextStreamEvent, 3)
		inputStream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Here is text "}
		inputStream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "!!CITE1!! more."}
		inputStream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(inputStream)

		decorated := DecorateSearchStreamWithAnnotations(&llm.TextStreamResult{Stream: inputStream}, results)

		var events []llm.TextStreamEvent
		for event := range decorated.Stream {
			events = append(events, event)
		}

		// Should have: text, text, annotations, end
		require.Len(t, events, 4)
		require.Equal(t, llm.EventTypeText, events[0].Type)
		require.Equal(t, llm.EventTypeText, events[1].Type)
		require.Equal(t, llm.EventTypeAnnotations, events[2].Type)
		require.Equal(t, llm.EventTypeEnd, events[3].Type)

		// Verify annotation event content
		annotationData := events[2].Value.(map[string]interface{})
		annotations := annotationData["annotations"].([]llm.Annotation)
		cleanedMessage := annotationData["cleanedMessage"].(string)

		require.Len(t, annotations, 1)
		require.Equal(t, "post1", annotations[0].PostID)
		require.Equal(t, "Here is text  more.", cleanedMessage)
	})

	t.Run("no annotations when no markers", func(t *testing.T) {
		results := []RAGResult{
			{Index: 1, PostID: "post1"},
		}

		inputStream := make(chan llm.TextStreamEvent, 2)
		inputStream <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Plain text without markers"}
		inputStream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
		close(inputStream)

		decorated := DecorateSearchStreamWithAnnotations(&llm.TextStreamResult{Stream: inputStream}, results)

		var events []llm.TextStreamEvent
		for event := range decorated.Stream {
			events = append(events, event)
		}

		// Should have: text, end (no annotations event)
		require.Len(t, events, 2)
		require.Equal(t, llm.EventTypeText, events[0].Type)
		require.Equal(t, llm.EventTypeEnd, events[1].Type)
	})
}
