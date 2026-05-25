// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"context"
	"errors"
	"math"
)

const FunctionsTokenBudget = 200
const TokenLimitBufferSize = 0.9
const MinTokens = 100

// SafetyCheckThreshold gates the post-truncation provider-side count call. We
// only ask the provider for an exact count when the heuristic estimate is at
// or above this fraction of the truncation budget; otherwise the heuristic is
// confident enough.
const SafetyCheckThreshold = 0.8

type TruncationWrapper struct {
	wrapped LanguageModel
}

func NewLLMTruncationWrapper(llm LanguageModel) *TruncationWrapper {
	return &TruncationWrapper{
		wrapped: llm,
	}
}

func (w *TruncationWrapper) ChatCompletion(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	w.maybeTruncate(ctx, &request, opts)
	return w.wrapped.ChatCompletion(ctx, request, opts...)
}

func (w *TruncationWrapper) ChatCompletionNoStream(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	w.maybeTruncate(ctx, &request, opts)
	return w.wrapped.ChatCompletionNoStream(ctx, request, opts...)
}

// maybeTruncate applies the heuristic truncation and, when the wrapped model
// can give us an exact count and our estimate is near the budget, asks the
// provider to verify. If the provider count exceeds the raw input limit, the
// oldest post is dropped and the provider is asked once more (bounded retry).
func (w *TruncationWrapper) maybeTruncate(ctx context.Context, request *CompletionRequest, opts []LanguageModelOption) {
	limit := w.wrapped.InputTokenLimit()
	if limit <= 0 {
		return
	}
	budget := int(math.Max(math.Floor(float64(limit-FunctionsTokenBudget)*TokenLimitBufferSize), MinTokens))
	request.Truncate(budget, EstimateTokens)

	heuristicEstimate := 0
	for _, post := range request.Posts {
		heuristicEstimate += EstimateTokens(post.Message)
	}
	if heuristicEstimate < int(SafetyCheckThreshold*float64(budget)) {
		return
	}

	count, err := w.wrapped.CountTokens(ctx, *request, opts...)
	if errors.Is(err, ErrUnsupportedTokenCount) || err != nil || count <= limit {
		return
	}
	// Drop the oldest non-system post. System prompts carry behavioral
	// instructions and must not silently disappear.
	if !dropOldestNonSystemPost(request) {
		return
	}
	_, _ = w.wrapped.CountTokens(ctx, *request, opts...)
}

// dropOldestNonSystemPost removes the first non-system post from the request.
// Returns true if a post was dropped, false if none was eligible.
func dropOldestNonSystemPost(request *CompletionRequest) bool {
	for i, post := range request.Posts {
		if post.Role == PostRoleSystem {
			continue
		}
		request.Posts = append(request.Posts[:i], request.Posts[i+1:]...)
		return true
	}
	return false
}

func (w *TruncationWrapper) CountTokens(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (int, error) {
	return w.wrapped.CountTokens(ctx, request, opts...)
}

func (w *TruncationWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}

func (w *TruncationWrapper) OutputTokenLimit() int {
	return w.wrapped.OutputTokenLimit()
}
