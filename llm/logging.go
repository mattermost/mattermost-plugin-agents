// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"context"
	"fmt"
	"testing"
)

type LanguageModelTestLogWrapper struct {
	t       *testing.T
	wrapped LanguageModel
}

func NewLanguageModelTestLogWrapper(t *testing.T, wrapped LanguageModel) *LanguageModelTestLogWrapper {
	return &LanguageModelTestLogWrapper{
		t:       t,
		wrapped: wrapped,
	}
}

func (w *LanguageModelTestLogWrapper) logInput(request CompletionRequest, opts ...LanguageModelOption) {
	prompt := fmt.Sprintf("\n%v", request)
	w.t.Log(prompt)
}

func (w *LanguageModelTestLogWrapper) ChatCompletion(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	w.logInput(request, opts...)
	return w.wrapped.ChatCompletion(ctx, request, opts...)
}

func (w *LanguageModelTestLogWrapper) ChatCompletionNoStream(ctx context.Context, request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	w.logInput(request, opts...)
	return w.wrapped.ChatCompletionNoStream(ctx, request, opts...)
}

func (w *LanguageModelTestLogWrapper) CountTokens(text string) int {
	return w.wrapped.CountTokens(text)
}

func (w *LanguageModelTestLogWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}
