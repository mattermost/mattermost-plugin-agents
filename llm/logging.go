// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"testing"

	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type LanguageModelLogWrapper struct {
	log     pluginapi.LogService
	wrapped LanguageModel
}

func NewLanguageModelLogWrapper(log pluginapi.LogService, wrapped LanguageModel) *LanguageModelLogWrapper {
	return &LanguageModelLogWrapper{
		log:     log,
		wrapped: wrapped,
	}
}

func (w *LanguageModelLogWrapper) logInput(request CompletionRequest, opts ...LanguageModelOption) {
	prompt := fmt.Sprintf("\n%v", request)
	toolCount := 0
	if request.Context != nil && request.Context.Tools != nil {
		toolCount = len(request.Context.Tools.GetToolsInfo())
	}
	w.log.Info("LLM Call",
		"prompt", prompt,
		"operation", request.Operation,
		"subtype", request.OperationSubType,
		"num_posts", len(request.Posts),
		"num_tools", toolCount,
	)
}

func (w *LanguageModelLogWrapper) ChatCompletion(request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	w.logInput(request, opts...)
	return w.wrapped.ChatCompletion(request, opts...)
}

func (w *LanguageModelLogWrapper) ChatCompletionNoStream(request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	w.logInput(request, opts...)
	return w.wrapped.ChatCompletionNoStream(request, opts...)
}

func (w *LanguageModelLogWrapper) CountTokens(text string) int {
	return w.wrapped.CountTokens(text)
}

func (w *LanguageModelLogWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}

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

func (w *LanguageModelTestLogWrapper) ChatCompletion(request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	w.logInput(request, opts...)
	return w.wrapped.ChatCompletion(request, opts...)
}

func (w *LanguageModelTestLogWrapper) ChatCompletionNoStream(request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	w.logInput(request, opts...)
	return w.wrapped.ChatCompletionNoStream(request, opts...)
}

func (w *LanguageModelTestLogWrapper) CountTokens(text string) int {
	return w.wrapped.CountTokens(text)
}

func (w *LanguageModelTestLogWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}
