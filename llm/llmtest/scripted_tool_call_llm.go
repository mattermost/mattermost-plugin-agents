// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmtest

import (
	"context"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// ScriptedToolCallLLM answers each request with the next scripted tool call
// batch, then with plain text once the script runs out.
type ScriptedToolCallLLM struct {
	mu       sync.Mutex
	script   [][]llm.ToolCall
	requests []llm.CompletionRequest
}

// SetScript replaces the remaining tool call batches.
func (l *ScriptedToolCallLLM) SetScript(script [][]llm.ToolCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.script = script
}

func (l *ScriptedToolCallLLM) ChatCompletion(_ context.Context, request llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests = append(l.requests, request)
	if len(l.script) == 0 {
		return streamOf(llm.TextStreamEvent{Type: llm.EventTypeText, Value: "Opened the pull request."}), nil
	}
	calls := l.script[0]
	l.script = l.script[1:]
	return streamOf(llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: calls}), nil
}

func (l *ScriptedToolCallLLM) ChatCompletionNoStream(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (string, error) {
	return "title", nil
}

func (l *ScriptedToolCallLLM) CountTokens(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}

func (l *ScriptedToolCallLLM) InputTokenLimit() int  { return 100000 }
func (l *ScriptedToolCallLLM) OutputTokenLimit() int { return 8192 }

// Recorded returns the completion requests received so far.
func (l *ScriptedToolCallLLM) Recorded() []llm.CompletionRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]llm.CompletionRequest(nil), l.requests...)
}

func streamOf(events ...llm.TextStreamEvent) *llm.TextStreamResult {
	stream := make(chan llm.TextStreamEvent, len(events)+1)
	for _, event := range events {
		stream <- event
	}
	stream <- llm.TextStreamEvent{Type: llm.EventTypeEnd}
	close(stream)
	return &llm.TextStreamResult{Stream: stream}
}
