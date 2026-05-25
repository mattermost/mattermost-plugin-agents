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

func TestTruncationWrapperSkipsWhenLimitIsZero(t *testing.T) {
	longMessage := strings.Repeat("x", 4000)
	request := CompletionRequest{
		Posts: []Post{{Role: PostRoleUser, Message: longMessage}},
	}

	inner := &MockLanguageModel{}
	inner.On("InputTokenLimit").Return(0)
	// CountTokens must not be consulted when the limit is zero. If the wrapper
	// truncates anyway, mock.Called will fail because the call is unexpected.
	inner.On(
		"ChatCompletion",
		mock.Anything,
		mock.MatchedBy(func(r CompletionRequest) bool {
			return len(r.Posts) == 1 && r.Posts[0].Message == longMessage
		}),
		mock.Anything,
	).Return(&TextStreamResult{}, nil).Once()

	wrapper := NewLLMTruncationWrapper(inner)
	_, err := wrapper.ChatCompletion(context.Background(), request)
	require.NoError(t, err)
	inner.AssertExpectations(t)
}

func TestTruncationWrapperSkipsWhenLimitIsZeroNoStream(t *testing.T) {
	longMessage := strings.Repeat("x", 4000)
	request := CompletionRequest{
		Posts: []Post{{Role: PostRoleUser, Message: longMessage}},
	}

	inner := &MockLanguageModel{}
	inner.On("InputTokenLimit").Return(0)
	inner.On(
		"ChatCompletionNoStream",
		mock.Anything,
		mock.MatchedBy(func(r CompletionRequest) bool {
			return len(r.Posts) == 1 && r.Posts[0].Message == longMessage
		}),
		mock.Anything,
	).Return("ok", nil).Once()

	wrapper := NewLLMTruncationWrapper(inner)
	result, err := wrapper.ChatCompletionNoStream(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, "ok", result)
	inner.AssertExpectations(t)
}

// budget computed inside TruncationWrapper for limit=1000 is
// floor((1000 - FunctionsTokenBudget=200) * TokenLimitBufferSize=0.9) = 720.
// The safety check threshold is 0.8 * budget = 576. We control the heuristic
// by returning a fixed CountTokens per post.

func TestTruncationWrapperSkipsSafetyCheckFarFromBudget(t *testing.T) {
	// Heuristic estimates 200 tokens (well below 0.8 * 720 = 576) — no
	// CountRequestTokens call should be made. The mock would panic if called.
	inner := &MockLanguageModel{}
	inner.On("InputTokenLimit").Return(1000)
	inner.On("CountTokens", mock.Anything).Return(200)
	inner.On(
		"ChatCompletion", mock.Anything, mock.Anything, mock.Anything,
	).Return(&TextStreamResult{}, nil).Once()

	wrapper := NewLLMTruncationWrapper(inner)
	_, err := wrapper.ChatCompletion(context.Background(), CompletionRequest{
		Posts: []Post{{Role: PostRoleUser, Message: "short"}},
	})
	require.NoError(t, err)
	inner.AssertNotCalled(t, "CountRequestTokens", mock.Anything, mock.Anything, mock.Anything)
}

func TestTruncationWrapperCallsSafetyCheckNearBudget(t *testing.T) {
	// 2 posts at 350 tokens each fit under budget=720 without partial trimming,
	// and the total 700 exceeds the 0.8 * 720 = 576 safety threshold.
	// CountRequestTokens returns a count under the raw limit → request unchanged.
	inner := &MockLanguageModel{}
	inner.On("InputTokenLimit").Return(1000)
	inner.On("CountTokens", mock.Anything).Return(350)
	inner.On("CountRequestTokens", mock.Anything, mock.Anything, mock.Anything).Return(900, nil).Once()
	inner.On(
		"ChatCompletion",
		mock.Anything,
		mock.MatchedBy(func(r CompletionRequest) bool { return len(r.Posts) == 2 }),
		mock.Anything,
	).Return(&TextStreamResult{}, nil).Once()

	wrapper := NewLLMTruncationWrapper(inner)
	_, err := wrapper.ChatCompletion(context.Background(), CompletionRequest{
		Posts: []Post{
			{Role: PostRoleUser, Message: "older"},
			{Role: PostRoleUser, Message: "newer"},
		},
	})
	require.NoError(t, err)
	inner.AssertExpectations(t)
}

func TestTruncationWrapperDropsOldestWhenProviderCountExceedsLimit(t *testing.T) {
	// 2 posts at 350 tokens each = 700 heuristic → safety check runs.
	// First CountRequestTokens returns 1100 (> 1000 raw limit) → drop oldest and retry once.
	// Second call returns 800 (under limit) → send the trimmed request.
	inner := &MockLanguageModel{}
	inner.On("InputTokenLimit").Return(1000)
	inner.On("CountTokens", mock.Anything).Return(350)
	inner.On(
		"CountRequestTokens", mock.Anything, mock.Anything, mock.Anything,
	).Return(1100, nil).Once()
	inner.On(
		"CountRequestTokens", mock.Anything, mock.Anything, mock.Anything,
	).Return(800, nil).Once()
	inner.On(
		"ChatCompletion",
		mock.Anything,
		mock.MatchedBy(func(r CompletionRequest) bool {
			return len(r.Posts) == 1 && r.Posts[0].Message == "newer"
		}),
		mock.Anything,
	).Return(&TextStreamResult{}, nil).Once()

	wrapper := NewLLMTruncationWrapper(inner)
	_, err := wrapper.ChatCompletion(context.Background(), CompletionRequest{
		Posts: []Post{
			{Role: PostRoleUser, Message: "older"},
			{Role: PostRoleUser, Message: "newer"},
		},
	})
	require.NoError(t, err)
	inner.AssertExpectations(t)
}
