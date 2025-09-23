// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"errors"
)

// TokenUsageLoggingWrapper wraps a LanguageModel to log token usage
type TokenUsageLoggingWrapper struct {
	wrapped     LanguageModel
	botUsername string
	logger      LoggerInterface
}

// LoggerInterface defines the logging contract
type LoggerInterface interface {
	Info(string, ...any)
}

// NewTokenUsageLoggingWrapper creates a new wrapper that logs token usage
func NewTokenUsageLoggingWrapper(wrapped LanguageModel, botUsername string, logger LoggerInterface) *TokenUsageLoggingWrapper {
	return &TokenUsageLoggingWrapper{
		wrapped:     wrapped,
		botUsername: botUsername,
		logger:      logger,
	}
}


// ChatCompletion intercepts the streaming response to extract and log token usage
func (w *TokenUsageLoggingWrapper) ChatCompletion(request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	result, err := w.wrapped.ChatCompletion(request, opts...)
	if err != nil {
		return nil, err
	}

	if w.logger == nil {
		return nil, errors.New("logger is nil")
	}

	interceptedStream := make(chan TextStreamEvent)

	go func() {
		defer close(interceptedStream)

		for event := range result.Stream {
			if event.Type != EventTypeUsage {
				interceptedStream <- event
				continue
			}

			usage, ok := event.Value.(TokenUsage)
			if !ok {
				continue
			}

			userID := "unknown"
			teamID := "unknown"
			if request.Context != nil {
				if request.Context.RequestingUser != nil {
					userID = request.Context.RequestingUser.Id
				}
				if request.Context.Team != nil {
					teamID = request.Context.Team.Id
				}
			}

			w.logger.Info("Token Usage",
				"log_type", "token_usage",
				"user_id", userID,
				"team_id", teamID,
				"bot_username", w.botUsername,
				"input_tokens", usage.InputTokens,
				"output_tokens", usage.OutputTokens,
				"total_tokens", usage.InputTokens+usage.OutputTokens,
			)
		}
	}()

	return &TextStreamResult{Stream: interceptedStream}, nil
}

// ChatCompletionNoStream uses the streaming method internally, so token usage
// logging happens automatically when ReadAll() processes the intercepted stream
func (w *TokenUsageLoggingWrapper) ChatCompletionNoStream(request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	result, err := w.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

// CountTokens delegates to the wrapped model
func (w *TokenUsageLoggingWrapper) CountTokens(text string) int {
	return w.wrapped.CountTokens(text)
}

// InputTokenLimit delegates to the wrapped model
func (w *TokenUsageLoggingWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}
