// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// MaxToolResolutionDepth is the maximum number of times the auto-run tools
// wrapper will re-invoke the LLM to resolve tool calls before stopping.
const MaxToolResolutionDepth = 10

// AutoRunToolsWrapper wraps a LanguageModel to automatically execute tool calls
// that are configured for auto-run, feeding their results back into the conversation
// until the model produces a final text response or non-auto-runnable tool calls.
type AutoRunToolsWrapper struct {
	inner LanguageModel
}

// NewAutoRunToolsWrapper creates a new AutoRunToolsWrapper around the given LanguageModel.
func NewAutoRunToolsWrapper(inner LanguageModel) LanguageModel {
	return &AutoRunToolsWrapper{inner: inner}
}

// ChatCompletion implements LanguageModel. When AutoRunTools is configured and tools are
// available, it will automatically execute matching tool calls and re-invoke the LLM
// up to MaxToolResolutionDepth times.
func (w *AutoRunToolsWrapper) ChatCompletion(request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	// Build config from opts to check if auto-run is enabled
	var cfg LanguageModelConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	// If auto-run is not configured or no tools context, delegate directly
	if len(cfg.AutoRunTools) == 0 || request.Context == nil || request.Context.Tools == nil {
		return w.inner.ChatCompletion(request, opts...)
	}

	output := make(chan TextStreamEvent)

	go func() {
		defer close(output)
		w.runToolLoop(request, opts, cfg.AutoRunTools, output)
	}()

	return &TextStreamResult{Stream: output}, nil
}

// runToolLoop runs the tool resolution loop, forwarding events and re-invoking
// the LLM when auto-runnable tool calls are received.
func (w *AutoRunToolsWrapper) runToolLoop(request CompletionRequest, opts []LanguageModelOption, autoRunTools []string, output chan<- TextStreamEvent) {
	for i := 0; i < MaxToolResolutionDepth; i++ {
		result, err := w.inner.ChatCompletion(request, opts...)
		if err != nil {
			output <- TextStreamEvent{Type: EventTypeError, Value: err}
			return
		}

		var accumulatedText string
		var toolCalls []ToolCall
		var receivedToolCalls bool

		for event := range result.Stream {
			switch event.Type {
			case EventTypeToolCalls:
				if tc, ok := event.Value.([]ToolCall); ok {
					toolCalls = append(toolCalls, tc...)
					receivedToolCalls = true
				}
			case EventTypeEnd:
				// Handle end below after consuming the stream
			default:
				// Forward all other events (text, reasoning, annotations, usage, etc.)
				if event.Type == EventTypeText {
					if text, ok := event.Value.(string); ok {
						accumulatedText += text
					}
				}
				output <- event
			}
		}

		if !receivedToolCalls {
			// No tool calls: send end event and return
			output <- TextStreamEvent{Type: EventTypeEnd}
			return
		}

		if !ShouldAutoRunTools(toolCalls, autoRunTools) {
			// Tool calls are not all auto-runnable: forward them and return
			output <- TextStreamEvent{Type: EventTypeToolCalls, Value: toolCalls}
			return
		}

		// Execute auto-run tools
		results := ExecuteAutoRunTools(toolCalls, request.Context.Tools.ResolveTool, request.Context)

		// Build resolved tool call entries
		resolvedToolCalls := make([]ToolCall, len(results))
		for j, r := range results {
			status := ToolCallStatusSuccess
			if r.IsError {
				status = ToolCallStatusError
			}
			resolvedToolCalls[j] = ToolCall{
				ID:        toolCalls[j].ID,
				Name:      toolCalls[j].Name,
				Arguments: toolCalls[j].Arguments,
				Result:    r.Result,
				Status:    status,
			}
		}

		// Append a bot post with accumulated text and tool results for re-submission
		request.Posts = append(request.Posts, Post{
			Role:    PostRoleBot,
			Message: accumulatedText,
			ToolUse: resolvedToolCalls,
		})
	}

	// If we've exhausted MaxToolResolutionDepth, send end event
	output <- TextStreamEvent{Type: EventTypeEnd}
}

// ChatCompletionNoStream implements LanguageModel by using ChatCompletion and reading all results.
func (w *AutoRunToolsWrapper) ChatCompletionNoStream(request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	result, err := w.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

// CountTokens delegates to the inner model.
func (w *AutoRunToolsWrapper) CountTokens(text string) int {
	return w.inner.CountTokens(text)
}

// InputTokenLimit delegates to the inner model.
func (w *AutoRunToolsWrapper) InputTokenLimit() int {
	return w.inner.InputTokenLimit()
}
