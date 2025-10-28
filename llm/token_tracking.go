// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// MetricsObserver defines the interface for observing token usage metrics
type MetricsObserver interface {
	ObserveTokenUsage(botName, teamID, userID string, inputTokens, outputTokens int)
}

// TokenUsageLoggingWrapper wraps a LanguageModel to log token usage
type TokenUsageLoggingWrapper struct {
	wrapped     LanguageModel
	botUsername string
	tokenLogger *mlog.Logger
	metrics     MetricsObserver
}

// NewTokenUsageLoggingWrapper creates a new wrapper that logs token usage
func NewTokenUsageLoggingWrapper(wrapped LanguageModel, botUsername string, tokenLogger *mlog.Logger, metrics MetricsObserver) *TokenUsageLoggingWrapper {
	return &TokenUsageLoggingWrapper{
		wrapped:     wrapped,
		botUsername: botUsername,
		tokenLogger: tokenLogger,
		metrics:     metrics,
	}
}

// CreateTokenLogger creates a dedicated logger for token usage metrics
func CreateTokenLogger() (*mlog.Logger, error) {
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
		"filename": "logs/agents/token_usage.log",
		"max_size": 100,  // MB
		"compress": true, // compress rotated files
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

// ChatCompletion intercepts the streaming response to extract and log token usage
func (w *TokenUsageLoggingWrapper) ChatCompletion(request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
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
				} else if request.Context.Channel != nil {
					// For DM and Group channels, use a special identifier
					// instead of "unknown" to distinguish them in metrics
					switch request.Context.Channel.Type {
					case model.ChannelTypeDirect:
						teamID = "dm"
					case model.ChannelTypeGroup:
						teamID = "group"
					default:
						teamID = "unknown"
					}
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

			// Emit metrics if available (user_id not included in metrics)
			if w.metrics != nil {
				w.metrics.ObserveTokenUsage(
					w.botUsername,
					teamID,
					"",
					int(usage.InputTokens),
					int(usage.OutputTokens),
				)
			}
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
