// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Budget computed for limit=1000 is floor((1000 - FunctionsTokenBudget=200) * 0.9) = 720.
// Safety threshold is 0.8 * 720 = 576. Two near-budget posts (~299 tokens each)
// land between the threshold and the budget so the heuristic accepts both but
// the safety check still runs.
func nearBudgetMessage() string { return strings.Repeat("a ", 327) }

// midBudgetMessage estimates to ~210 tokens, so three of them (~630) survive the
// heuristic budget (720) but together clear the safety threshold (576). This lets
// a case exercise repeated provider-count drops without the heuristic pre-trimming.
func midBudgetMessage() string { return strings.Repeat("a ", 229) }

// countResult lets each case script the sequence of CountTokens returns.
type countResult struct {
	count int
	err   error
}

func TestTruncationWrapper(t *testing.T) {
	longMessage := strings.Repeat("x", 4000)
	nearBudget := nearBudgetMessage()
	midBudget := midBudgetMessage()
	systemPrompt := "you are a helpful assistant"

	tests := []struct {
		name string
		// posts is the initial request payload.
		posts []Post
		// inputTokenLimit drives the wrapper's truncation decision.
		inputTokenLimit int
		// countTokensReturns is the scripted sequence of CountTokens returns.
		// Empty means the wrapper must not call CountTokens at all.
		countTokensReturns []countResult
		// expectedPostCount is the post count expected in the final downstream
		// call to the wrapped model.
		expectedPostCount int
		// expectedFirstPost optionally asserts a property of Posts[0] (used to
		// check system-prompt preservation).
		expectedFirstPost *Post
		// expectedSecondPost optionally asserts a property of Posts[1].
		expectedSecondPost *Post
		// useNoStream exercises ChatCompletionNoStream instead of ChatCompletion.
		useNoStream bool
	}{
		{
			name:               "skips truncation when limit is zero",
			posts:              []Post{{Role: PostRoleUser, Message: longMessage}},
			inputTokenLimit:    0,
			countTokensReturns: nil, // CountTokens must not be consulted
			expectedPostCount:  1,
		},
		{
			name:               "skips safety check when heuristic is far from budget",
			posts:              []Post{{Role: PostRoleUser, Message: "hi"}},
			inputTokenLimit:    1000,
			countTokensReturns: nil,
			expectedPostCount:  1,
		},
		{
			name: "runs safety check near budget and sends when count is under limit",
			posts: []Post{
				{Role: PostRoleUser, Message: nearBudget},
				{Role: PostRoleUser, Message: nearBudget},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{count: 900}},
			expectedPostCount:  2,
		},
		{
			name: "drops oldest when provider count exceeds limit and retries once (NoStream)",
			posts: []Post{
				{Role: PostRoleUser, Message: "older-" + nearBudget},
				{Role: PostRoleUser, Message: "newer-" + nearBudget},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{count: 1100}, {count: 800}},
			expectedPostCount:  1,
			expectedFirstPost:  &Post{Role: PostRoleUser, Message: "newer-" + nearBudget},
			useNoStream:        true,
		},
		{
			name: "preserves system prompt on overflow drop",
			posts: []Post{
				{Role: PostRoleSystem, Message: systemPrompt},
				{Role: PostRoleUser, Message: "older-" + nearBudget},
				{Role: PostRoleUser, Message: "newer-" + nearBudget},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{count: 1100}, {count: 800}},
			expectedPostCount:  2,
			expectedFirstPost:  &Post{Role: PostRoleSystem, Message: systemPrompt},
			expectedSecondPost: &Post{Role: PostRoleUser, Message: "newer-" + nearBudget},
		},
		{
			name: "keeps dropping oldest until provider count is under limit",
			posts: []Post{
				{Role: PostRoleUser, Message: "oldest-" + midBudget},
				{Role: PostRoleUser, Message: "middle-" + midBudget},
				{Role: PostRoleUser, Message: "newest-" + midBudget},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{count: 1100}, {count: 1050}, {count: 800}},
			expectedPostCount:  1,
			expectedFirstPost:  &Post{Role: PostRoleUser, Message: "newest-" + midBudget},
		},
		{
			name: "skips safety check when provider returns unsupported error",
			posts: []Post{
				{Role: PostRoleUser, Message: nearBudget},
				{Role: PostRoleUser, Message: nearBudget},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{err: ErrUnsupportedTokenCount}},
			expectedPostCount:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &MockLanguageModel{}
			inner.On("InputTokenLimit").Return(tt.inputTokenLimit)
			for _, r := range tt.countTokensReturns {
				inner.On(
					"CountTokens", mock.Anything, mock.Anything, mock.Anything,
				).Return(r.count, r.err).Once()
			}

			matcher := mock.MatchedBy(func(r CompletionRequest) bool {
				if len(r.Posts) != tt.expectedPostCount {
					return false
				}
				if tt.expectedFirstPost != nil {
					if r.Posts[0].Role != tt.expectedFirstPost.Role ||
						r.Posts[0].Message != tt.expectedFirstPost.Message {
						return false
					}
				}
				if tt.expectedSecondPost != nil {
					if len(r.Posts) < 2 ||
						r.Posts[1].Role != tt.expectedSecondPost.Role ||
						r.Posts[1].Message != tt.expectedSecondPost.Message {
						return false
					}
				}
				return true
			})

			wrapper := NewLLMTruncationWrapper(inner)
			req := CompletionRequest{Posts: tt.posts}
			if tt.useNoStream {
				inner.On("ChatCompletionNoStream", mock.Anything, matcher, mock.Anything).
					Return("ok", nil).Once()
				result, err := wrapper.ChatCompletionNoStream(context.Background(), req)
				require.NoError(t, err)
				assert.Equal(t, "ok", result)
			} else {
				inner.On("ChatCompletion", mock.Anything, matcher, mock.Anything).
					Return(&TextStreamResult{}, nil).Once()
				_, err := wrapper.ChatCompletion(context.Background(), req)
				require.NoError(t, err)
			}
			inner.AssertExpectations(t)
			if tt.countTokensReturns == nil {
				inner.AssertNotCalled(t, "CountTokens", mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}
