// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/llm"
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
}

// ChatCompletion implements streaming completion
func (f *FakeLLM) ChatCompletion(conversation llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	if f.Error != nil {
		return nil, f.Error
	}

	stream := make(chan llm.TextStreamEvent)

	go func() {
		defer close(stream)

		if len(f.StreamEvents) > 0 {
			// Send configured events
			for _, event := range f.StreamEvents {
				stream <- event
			}
		} else {
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
