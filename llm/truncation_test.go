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
	longSystemPrompt := strings.Repeat("s ", 600)
	// oversizedSystemPrompt alone estimates above the 720-token budget.
	oversizedSystemPrompt := strings.Repeat("s ", 1000)
	note := Post{Role: PostRoleUser, Message: TruncatedHistoryNote}
	// cut matches any message cut by Truncate; its exact length is covered by
	// TestCompletionRequestTruncate.
	cut := func(role PostRole) Post { return Post{Role: role, Message: TruncatedMessagePrefix} }
	toolRound := Post{Role: PostRoleBot, ToolUse: []ToolCall{{
		ID: "tc1", Name: "search", Arguments: []byte(`{}`), Result: strings.Repeat("y ", 1000),
	}}}

	tests := []struct {
		name string
		// posts is the initial request payload.
		posts []Post
		// inputTokenLimit drives the wrapper's truncation decision.
		inputTokenLimit int
		// countTokensReturns is the scripted sequence of CountTokens returns.
		// Empty means the wrapper must not call CountTokens at all.
		countTokensReturns []countResult
		// expectedPosts is the role and message of each post in the final
		// downstream call to the wrapped model.
		expectedPosts []Post
		// useNoStream exercises ChatCompletionNoStream instead of ChatCompletion.
		useNoStream bool
	}{
		{
			name:               "skips truncation when limit is zero",
			posts:              []Post{{Role: PostRoleUser, Message: longMessage}},
			inputTokenLimit:    0,
			countTokensReturns: nil, // CountTokens must not be consulted
			expectedPosts:      []Post{{Role: PostRoleUser, Message: longMessage}},
		},
		{
			name:               "skips safety check when heuristic is far from budget",
			posts:              []Post{{Role: PostRoleUser, Message: "hi"}},
			inputTokenLimit:    1000,
			countTokensReturns: nil,
			expectedPosts:      []Post{{Role: PostRoleUser, Message: "hi"}},
		},
		{
			name: "runs safety check near budget and sends when count is under limit",
			posts: []Post{
				{Role: PostRoleUser, Message: nearBudget},
				{Role: PostRoleUser, Message: nearBudget},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{count: 900}},
			expectedPosts: []Post{
				{Role: PostRoleUser, Message: nearBudget},
				{Role: PostRoleUser, Message: nearBudget},
			},
		},
		{
			name: "drops oldest when provider count exceeds limit and notes it (NoStream)",
			posts: []Post{
				{Role: PostRoleUser, Message: "older-" + nearBudget},
				{Role: PostRoleUser, Message: "newer-" + nearBudget},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{count: 1100}, {count: 800}},
			expectedPosts:      []Post{note, {Role: PostRoleUser, Message: "newer-" + nearBudget}},
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
			expectedPosts: []Post{
				{Role: PostRoleSystem, Message: systemPrompt},
				note,
				{Role: PostRoleUser, Message: "newer-" + nearBudget},
			},
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
			expectedPosts:      []Post{note, {Role: PostRoleUser, Message: "newest-" + midBudget}},
		},
		{
			name: "never drops the newest post while the provider count stays over the limit",
			posts: []Post{
				{Role: PostRoleSystem, Message: systemPrompt},
				{Role: PostRoleUser, Message: "older-" + nearBudget},
				{Role: PostRoleUser, Message: "newer-" + nearBudget},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{count: 1100}, {count: 1050}},
			expectedPosts: []Post{
				{Role: PostRoleSystem, Message: systemPrompt},
				note,
				{Role: PostRoleUser, Message: "newer-" + nearBudget},
			},
		},
		{
			name: "skips safety check when provider returns unsupported error",
			posts: []Post{
				{Role: PostRoleUser, Message: nearBudget},
				{Role: PostRoleUser, Message: nearBudget},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{err: ErrUnsupportedTokenCount}},
			expectedPosts: []Post{
				{Role: PostRoleUser, Message: nearBudget},
				{Role: PostRoleUser, Message: nearBudget},
			},
		},
		{
			name: "heuristic truncation keeps a large system prompt whole",
			posts: []Post{
				{Role: PostRoleSystem, Message: longSystemPrompt},
				{Role: PostRoleUser, Message: longMessage},
				{Role: PostRoleUser, Message: "question?"},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{count: 900}},
			expectedPosts: []Post{
				{Role: PostRoleSystem, Message: longSystemPrompt},
				cut(PostRoleUser),
				{Role: PostRoleUser, Message: "question?"},
			},
		},
		{
			name: "system prompt over budget keeps the newest message whole",
			posts: []Post{
				{Role: PostRoleSystem, Message: oversizedSystemPrompt},
				{Role: PostRoleUser, Message: "older"},
				{Role: PostRoleUser, Message: "question?"},
			},
			inputTokenLimit:    1000,
			countTokensReturns: []countResult{{count: 990}},
			expectedPosts: []Post{
				{Role: PostRoleSystem, Message: oversizedSystemPrompt},
				note,
				{Role: PostRoleUser, Message: "question?"},
			},
		},
		{
			name: "heuristic counts tool results and drops the tool round whole",
			posts: []Post{
				{Role: PostRoleSystem, Message: systemPrompt},
				{Role: PostRoleUser, Message: "find it"},
				toolRound,
				{Role: PostRoleUser, Message: "what now?"},
			},
			inputTokenLimit: 1000,
			expectedPosts: []Post{
				{Role: PostRoleSystem, Message: systemPrompt},
				note,
				{Role: PostRoleUser, Message: "what now?"},
			},
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

			var sent CompletionRequest
			capture := func(args mock.Arguments) { sent = args.Get(1).(CompletionRequest) }

			wrapper := NewLLMTruncationWrapper(inner)
			req := CompletionRequest{Posts: tt.posts}
			if tt.useNoStream {
				inner.On("ChatCompletionNoStream", mock.Anything, mock.Anything, mock.Anything).
					Run(capture).Return("ok", nil).Once()
				result, err := wrapper.ChatCompletionNoStream(context.Background(), req)
				require.NoError(t, err)
				assert.Equal(t, "ok", result)
			} else {
				inner.On("ChatCompletion", mock.Anything, mock.Anything, mock.Anything).
					Run(capture).Return(&TextStreamResult{}, nil).Once()
				_, err := wrapper.ChatCompletion(context.Background(), req)
				require.NoError(t, err)
			}
			inner.AssertExpectations(t)
			if tt.countTokensReturns == nil {
				inner.AssertNotCalled(t, "CountTokens", mock.Anything, mock.Anything, mock.Anything)
			}

			require.Len(t, sent.Posts, len(tt.expectedPosts))
			for i, want := range tt.expectedPosts {
				got := sent.Posts[i]
				assert.Equal(t, want.Role, got.Role, "post %d role", i)
				if want.Message == TruncatedMessagePrefix {
					assert.True(t, strings.HasPrefix(got.Message, TruncatedMessagePrefix), "post %d must be marked as cut", i)
					continue
				}
				assert.Equal(t, want.Message, got.Message, "post %d message", i)
			}
		})
	}
}
