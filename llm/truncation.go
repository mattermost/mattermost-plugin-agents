// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"context"
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

// maybeTruncate applies the heuristic truncation and, when supported and the
// heuristic estimate is near the budget, asks the provider for an exact count
// to verify. If the provider count exceeds the raw input limit, the oldest
// post is dropped and the provider is asked once more (bounded retry).
func (w *TruncationWrapper) maybeTruncate(ctx context.Context, request *CompletionRequest, opts []LanguageModelOption) {
	limit := w.wrapped.InputTokenLimit()
	if limit <= 0 {
		return
	}
	budget := int(math.Max(math.Floor(float64(limit-FunctionsTokenBudget)*TokenLimitBufferSize), MinTokens))
	request.Truncate(budget, w.wrapped.CountTokens)

	counter, ok := w.wrapped.(TokenCounter)
	if !ok {
		return
	}
	heuristicEstimate := 0
	for _, post := range request.Posts {
		heuristicEstimate += w.wrapped.CountTokens(post.Message)
	}
	if heuristicEstimate < int(SafetyCheckThreshold*float64(budget)) {
		return
	}

	count, err := counter.CountRequestTokens(ctx, *request, opts...)
	if err != nil || count <= limit {
		return
	}
	if len(request.Posts) <= 1 {
		return
	}
	request.Posts = request.Posts[1:] // drop oldest
	_, _ = counter.CountRequestTokens(ctx, *request, opts...)
}

func (w *TruncationWrapper) CountTokens(text string) int {
	return w.wrapped.CountTokens(text)
}

func (w *TruncationWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}

func (w *TruncationWrapper) OutputTokenLimit() int {
	return w.wrapped.OutputTokenLimit()
}
