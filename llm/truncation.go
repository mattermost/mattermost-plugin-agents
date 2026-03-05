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

type TruncationWrapper struct {
	wrapped LanguageModel
}

func NewLLMTruncationWrapper(llm LanguageModel) *TruncationWrapper {
	return &TruncationWrapper{
		wrapped: llm,
	}
}

func (w *TruncationWrapper) ChatCompletion(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	tokenLimit := int(math.Max(math.Floor(float64(w.wrapped.InputTokenLimit()-FunctionsTokenBudget)*TokenLimitBufferSize), MinTokens))
	request.Truncate(tokenLimit, w.wrapped.CountTokens)
	return w.wrapped.ChatCompletion(ctx, request, opts...)
}

func (w *TruncationWrapper) ChatCompletionNoStream(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	tokenLimit := int(math.Max(math.Floor(float64(w.wrapped.InputTokenLimit()-FunctionsTokenBudget)*TokenLimitBufferSize), MinTokens))
	request.Truncate(tokenLimit, w.wrapped.CountTokens)
	return w.wrapped.ChatCompletionNoStream(ctx, request, opts...)
}

func (w *TruncationWrapper) CountTokens(text string) int {
	return w.wrapped.CountTokens(text)
}

func (w *TruncationWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}
