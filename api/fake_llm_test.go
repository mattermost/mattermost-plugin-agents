// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// FakeLLM is a test implementation of llm.LanguageModel that returns configurable responses
// without making real API calls. This is not a mock - it's a real implementation of the
// interface designed for testing.
type FakeLLM struct {
	// Response is the text to return for non-streaming calls
	Response string
	// Error to return instead of a response
	Error error
	// StreamEvents are the events to send for streaming calls (if nil, uses Response)
	StreamEvents []llm.TextStreamEvent
	// TokenCount to return from CountTokens
	TokenCount int
	// TokenLimit to return from InputTokenLimit
	TokenLimit int

	// LastConversation holds the last completion request for assertions.
	LastConversation llm.CompletionRequest
	// LastConfig holds the resolved config from the last call's options.
	LastConfig llm.LanguageModelConfig

	// StreamEventSequence, when non-empty, supplies a different set of stream
	// events for each successive call. The first call returns the first group,
	// the second call returns the second group, and so on. Once exhausted, the
	// last group is reused for any further calls. Useful for testing tool
	// runners that re-invoke the LLM after executing tool calls.
	StreamEventSequence [][]llm.TextStreamEvent

	// AllRequests records every CompletionRequest received by either
	// ChatCompletion or ChatCompletionNoStream, in order. Tests can inspect
	// this to assert on multi-call behavior such as tool-runner round trips.
	AllRequests []llm.CompletionRequest

	mu          sync.RWMutex
	lastRequest llm.CompletionRequest
	callCount   int
}

// ChatCompletion implements streaming completion
func (f *FakeLLM) ChatCompletion(conversation llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	var cfg llm.LanguageModelConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	f.mu.Lock()
	f.lastRequest = conversation
	f.LastConversation = conversation
	f.LastConfig = cfg
	f.AllRequests = append(f.AllRequests, conversation)
	callIdx := f.callCount
	f.callCount++
	f.mu.Unlock()

	if f.Error != nil {
		return nil, f.Error
	}

	var sequencedEvents []llm.TextStreamEvent
	if len(f.StreamEventSequence) > 0 {
		if callIdx >= len(f.StreamEventSequence) {
			callIdx = len(f.StreamEventSequence) - 1
		}
		sequencedEvents = f.StreamEventSequence[callIdx]
	}

	stream := make(chan llm.TextStreamEvent)

	go func() {
		defer close(stream)

		switch {
		case len(sequencedEvents) > 0:
			for _, event := range sequencedEvents {
				stream <- event
			}
		case len(f.StreamEvents) > 0:
			for _, event := range f.StreamEvents {
				stream <- event
			}
		default:
			// Default behavior: send response as single text event followed by end
			if f.Response != "" {
				stream <- llm.TextStreamEvent{
					Type:  llm.EventTypeText,
					Value: f.Response,
				}
			}
			stream <- llm.TextStreamEvent{
				Type:  llm.EventTypeEnd,
				Value: nil,
			}
		}
	}()

	return &llm.TextStreamResult{
		Stream: stream,
	}, nil
}

// ChatCompletionNoStream implements non-streaming completion
func (f *FakeLLM) ChatCompletionNoStream(conversation llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	var cfg llm.LanguageModelConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	f.mu.Lock()
	f.lastRequest = conversation
	f.LastConversation = conversation
	f.LastConfig = cfg
	f.AllRequests = append(f.AllRequests, conversation)
	f.callCount++
	f.mu.Unlock()

	if f.Error != nil {
		return "", f.Error
	}
	return f.Response, nil
}

// CountTokens implements token counting (returns configured value or basic estimate)
func (f *FakeLLM) CountTokens(text string) int {
	if f.TokenCount > 0 {
		return f.TokenCount
	}
	// Simple estimate: ~4 characters per token
	return len(text) / 4
}

// InputTokenLimit implements token limit getter
func (f *FakeLLM) InputTokenLimit() int {
	if f.TokenLimit > 0 {
		return f.TokenLimit
	}
	return 100000 // Default reasonable limit
}

func (f *FakeLLM) LastRequest() llm.CompletionRequest {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.lastRequest
}

// NewFakeLLM creates a FakeLLM with a simple text response
func NewFakeLLM(response string) *FakeLLM {
	return &FakeLLM{
		Response:   response,
		TokenLimit: 100000,
	}
}

// NewFakeLLMWithError creates a FakeLLM that returns an error
func NewFakeLLMWithError(err error) *FakeLLM {
	return &FakeLLM{
		Error:      err,
		TokenLimit: 100000,
	}
}

// NewFakeLLMWithStreamEvents creates a FakeLLM with custom stream events
func NewFakeLLMWithStreamEvents(events []llm.TextStreamEvent) *FakeLLM {
	return &FakeLLM{
		StreamEvents: events,
		TokenLimit:   100000,
	}
}

// StreamingLLMError creates a FakeLLM that sends an error event in the stream
func StreamingLLMError(errMsg string) *FakeLLM {
	return &FakeLLM{
		StreamEvents: []llm.TextStreamEvent{
			{
				Type:  llm.EventTypeError,
				Value: fmt.Errorf("%s", errMsg),
			},
		},
		TokenLimit: 100000,
	}
}
