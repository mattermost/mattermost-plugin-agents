// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// TokenTrackingWrapper wraps a LanguageModel to track token usage in the database
type TokenTrackingWrapper struct {
	wrapped     LanguageModel
	botUsername string
	tokenLogger *mlog.Logger
}

// NewTokenLoggingWrapper creates a new wrapper that tracks token usage
func NewTokenLoggingWrapper(wrapped LanguageModel, botUsername string) (*TokenTrackingWrapper, error) {
	tokenLogger, err := createTokenLogger()
	if err != nil {
		return nil, fmt.Errorf("failed to create token tracking wrapper: %w", err)
	}
	return &TokenTrackingWrapper{
		wrapped:     wrapped,
		botUsername: botUsername,
		tokenLogger: tokenLogger,
	}, nil
}

// createTokenLogger creates a dedicated logger for token usage metrics
func createTokenLogger() (*mlog.Logger, error) {
	logger, err := mlog.NewLogger()
	if err != nil {
		return nil, fmt.Errorf("failed to create token logger: %w", err)
	}

	jsonTargetCfg := mlog.TargetCfg{
		Type:   "file",
		Format: "json",
		Levels: []mlog.Level{mlog.LvlInfo, mlog.LvlDebug},
	}
	jsonFileOptions := map[string]interface{}{
		"filename": "token_usage.log",
	}
	jsonOptions, err := json.Marshal(jsonFileOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal json file options: %w", err)
	}
	jsonTargetCfg.Options = json.RawMessage(jsonOptions)

	err = logger.ConfigureTargets(map[string]mlog.TargetCfg{
		"token_usage": jsonTargetCfg,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to configure token logger targets: %w", err)
	}

	return logger, nil
}

// ChatCompletion intercepts the streaming response to extract and track token usage
func (w *TokenTrackingWrapper) ChatCompletion(request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	result, err := w.wrapped.ChatCompletion(request, opts...)
	if err != nil {
		return nil, err
	}

	if w.tokenLogger == nil {
		return nil, errors.New("token logger is nil")
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

			w.tokenLogger.Info("Token Usage",
				mlog.String("user_id", userID),
				mlog.String("team_id", teamID),
				mlog.String("bot_username", w.botUsername),
				mlog.Int("input_tokens", usage.InputTokens),
				mlog.Int("output_tokens", usage.OutputTokens),
				mlog.Int("total_tokens", usage.InputTokens+usage.OutputTokens),
			)
		}
	}()

	return &TextStreamResult{Stream: interceptedStream}, nil
}

// ChatCompletionNoStream uses the streaming method internally, so token tracking
// happens automatically when ReadAll() processes the intercepted stream
func (w *TokenTrackingWrapper) ChatCompletionNoStream(request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	result, err := w.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

// CountTokens delegates to the wrapped model
func (w *TokenTrackingWrapper) CountTokens(text string) int {
	return w.wrapped.CountTokens(text)
}

// InputTokenLimit delegates to the wrapped model
func (w *TokenTrackingWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}
