// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package llmtest provides test and benchmark helpers for the llm package.
// It exists so that production code never links against the testing package.
package llmtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

type LanguageModelTestLogWrapper struct {
	t       *testing.T
	wrapped llm.LanguageModel
}

func NewLanguageModelTestLogWrapper(t *testing.T, wrapped llm.LanguageModel) *LanguageModelTestLogWrapper {
	return &LanguageModelTestLogWrapper{
		t:       t,
		wrapped: wrapped,
	}
}

func (w *LanguageModelTestLogWrapper) logInput(request llm.CompletionRequest, opts ...llm.LanguageModelOption) {
	prompt := fmt.Sprintf("\n%v", request)
	w.t.Log(prompt)
}

func (w *LanguageModelTestLogWrapper) ChatCompletion(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	w.logInput(request, opts...)
	return w.wrapped.ChatCompletion(ctx, request, opts...)
}

func (w *LanguageModelTestLogWrapper) ChatCompletionNoStream(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	w.logInput(request, opts...)
	return w.wrapped.ChatCompletionNoStream(ctx, request, opts...)
}

func (w *LanguageModelTestLogWrapper) CountTokens(ctx context.Context, request llm.CompletionRequest, opts ...llm.LanguageModelOption) (int, error) {
	return w.wrapped.CountTokens(ctx, request, opts...)
}

func (w *LanguageModelTestLogWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}

func (w *LanguageModelTestLogWrapper) OutputTokenLimit() int {
	return w.wrapped.OutputTokenLimit()
}
