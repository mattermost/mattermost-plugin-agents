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

// nearBudgetMessage returns a string whose EstimateTokens lands at roughly 300
// tokens — using two of these posts puts the heuristic above 0.8 * budget
// (576 for limit=1000) but below the budget itself, so the safety check runs
// without Truncate partial-trimming.
func nearBudgetMessage() string { return strings.Repeat("a ", 327) }

func TestTruncationWrapperSkipsWhenLimitIsZero(t *testing.T) {
	longMessage := strings.Repeat("x", 4000)
	request := CompletionRequest{
		Posts: []Post{{Role: PostRoleUser, Message: longMessage}},
	}

	inner := &MockLanguageModel{}
	inner.On("InputTokenLimit").Return(0)
	// CountTokens must not be consulted when the limit is zero.
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

// Budget for limit=1000 is floor((1000 - FunctionsTokenBudget=200) * 0.9) = 720.
// Safety threshold is 0.8 * 720 = 576.

func TestTruncationWrapperSkipsSafetyCheckFarFromBudget(t *testing.T) {
	// EstimateTokens("hi") ≈ 0. Heuristic total stays well below the safety
	// threshold so CountTokens must not be called.
	inner := &MockLanguageModel{}
	inner.On("InputTokenLimit").Return(1000)
	inner.On(
		"ChatCompletion", mock.Anything, mock.Anything, mock.Anything,
	).Return(&TextStreamResult{}, nil).Once()

	wrapper := NewLLMTruncationWrapper(inner)
	_, err := wrapper.ChatCompletion(context.Background(), CompletionRequest{
		Posts: []Post{{Role: PostRoleUser, Message: "hi"}},
	})
	require.NoError(t, err)
	inner.AssertNotCalled(t, "CountTokens", mock.Anything, mock.Anything, mock.Anything)
}

func TestTruncationWrapperCallsSafetyCheckNearBudget(t *testing.T) {
	// Two near-budget posts push the heuristic above 576 but under budget=720,
	// so Truncate keeps both and the safety check fires. Provider returns a
	// count under the raw limit → request goes through unchanged.
	msg := nearBudgetMessage()
	inner := &MockLanguageModel{}
	inner.On("InputTokenLimit").Return(1000)
	inner.On("CountTokens", mock.Anything, mock.Anything, mock.Anything).Return(900, nil).Once()
	inner.On(
		"ChatCompletion",
		mock.Anything,
		mock.MatchedBy(func(r CompletionRequest) bool { return len(r.Posts) == 2 }),
		mock.Anything,
	).Return(&TextStreamResult{}, nil).Once()

	wrapper := NewLLMTruncationWrapper(inner)
	_, err := wrapper.ChatCompletion(context.Background(), CompletionRequest{
		Posts: []Post{
			{Role: PostRoleUser, Message: msg},
			{Role: PostRoleUser, Message: msg},
		},
	})
	require.NoError(t, err)
	inner.AssertExpectations(t)
}

func TestTruncationWrapperDropsOldestWhenProviderCountExceedsLimit(t *testing.T) {
	// First CountTokens returns 1100 (> 1000 raw limit) → drop oldest, retry.
	// Second call returns 800 (under limit) → send the trimmed request.
	// Both posts must be near-budget so the heuristic clears the safety threshold.
	olderMsg := "older-" + nearBudgetMessage()
	newerMsg := "newer-" + nearBudgetMessage()
	inner := &MockLanguageModel{}
	inner.On("InputTokenLimit").Return(1000)
	inner.On(
		"CountTokens", mock.Anything, mock.Anything, mock.Anything,
	).Return(1100, nil).Once()
	inner.On(
		"CountTokens", mock.Anything, mock.Anything, mock.Anything,
	).Return(800, nil).Once()
	inner.On(
		"ChatCompletion",
		mock.Anything,
		mock.MatchedBy(func(r CompletionRequest) bool {
			// Oldest post dropped; only the newest remains.
			return len(r.Posts) == 1 && strings.HasPrefix(r.Posts[0].Message, "newer-")
		}),
		mock.Anything,
	).Return(&TextStreamResult{}, nil).Once()

	wrapper := NewLLMTruncationWrapper(inner)
	_, err := wrapper.ChatCompletion(context.Background(), CompletionRequest{
		Posts: []Post{
			{Role: PostRoleUser, Message: olderMsg},
			{Role: PostRoleUser, Message: newerMsg},
		},
	})
	require.NoError(t, err)
	inner.AssertExpectations(t)
}

func TestTruncationWrapperSkipsSafetyCheckWhenUnsupported(t *testing.T) {
	// Heuristic near budget, but provider returns ErrUnsupportedTokenCount.
	// The wrapper must not retry and must not drop messages.
	msg := nearBudgetMessage()
	inner := &MockLanguageModel{}
	inner.On("InputTokenLimit").Return(1000)
	inner.On(
		"CountTokens", mock.Anything, mock.Anything, mock.Anything,
	).Return(0, ErrUnsupportedTokenCount).Once()
	inner.On(
		"ChatCompletion",
		mock.Anything,
		mock.MatchedBy(func(r CompletionRequest) bool { return len(r.Posts) == 2 }),
		mock.Anything,
	).Return(&TextStreamResult{}, nil).Once()

	wrapper := NewLLMTruncationWrapper(inner)
	_, err := wrapper.ChatCompletion(context.Background(), CompletionRequest{
		Posts: []Post{
			{Role: PostRoleUser, Message: msg},
			{Role: PostRoleUser, Message: msg},
		},
	})
	require.NoError(t, err)
	inner.AssertExpectations(t)
}
