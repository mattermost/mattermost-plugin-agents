// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "fmt"

// EventType represents the type of event in the text stream
type EventType int

const (
	// EventTypeText represents a text chunk event
	EventTypeText EventType = iota
	// EventTypeEnd represents the end of the stream
	EventTypeEnd
	// EventTypeError represents an error event
	EventTypeError
	// EventTypeToolCalls represents a tool call event
	EventTypeToolCalls
	// EventTypeUsage represents token usage data
	EventTypeUsage
)

// TokenUsage represents token usage statistics for an LLM request
type TokenUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// TextStreamEvent represents an event in the text stream
type TextStreamEvent struct {
	Type  EventType
	Value any
}

// TextStreamResult represents a stream of text events
type TextStreamResult struct {
	Stream <-chan TextStreamEvent
}

func NewStreamFromString(text string) *TextStreamResult {
	stream := make(chan TextStreamEvent)

	go func() {
		// Send the text as a text event
		stream <- TextStreamEvent{
			Type:  EventTypeText,
			Value: text,
		}

		// Send end event
		stream <- TextStreamEvent{
			Type:  EventTypeEnd,
			Value: nil,
		}

		close(stream)
	}()

	return &TextStreamResult{
		Stream: stream,
	}
}

func (t *TextStreamResult) ReadAll() (string, error) {
	result := ""
	for event := range t.Stream {
		switch event.Type {
		case EventTypeText:
			if textChunk, ok := event.Value.(string); ok {
				result += textChunk
			}
		case EventTypeError:
			if err, ok := event.Value.(error); ok {
				return "", err
			}
		case EventTypeEnd:
			break
		case EventTypeToolCalls:
			return result, fmt.Errorf("Tool calls are not supported for read all")
		}
	}

	return result, nil
}
