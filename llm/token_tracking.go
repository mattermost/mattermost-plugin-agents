// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"encoding/json"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// MetricsObserver defines the interface for observing token usage metrics
type MetricsObserver interface {
	ObserveTokenUsage(botName, teamID, userID string, inputTokens, outputTokens int)
}

// TokenUsagePluginLogger is the logger interface used for plugin JSON logs.
type TokenUsagePluginLogger interface {
	Info(message string, keyValuePairs ...any)
}

// TokenUsageLoggingWrapper wraps a LanguageModel to log token usage
type TokenUsageLoggingWrapper struct {
	wrapped     LanguageModel
	botUsername string
	sinks       *TokenUsageSinks
	metrics     MetricsObserver
}

// NewTokenUsageLoggingWrapper creates a wrapper using a shared sink controller.
func NewTokenUsageLoggingWrapper(wrapped LanguageModel, botUsername string, sinks *TokenUsageSinks, metrics MetricsObserver) *TokenUsageLoggingWrapper {
	return &TokenUsageLoggingWrapper{
		wrapped:     wrapped,
		botUsername: botUsername,
		sinks:       sinks,
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
	if !w.shouldTrackTokenUsage() {
		return w.wrapped.ChatCompletion(request, opts...)
	}
	if request.OperationSubType == "" {
		request.OperationSubType = SubTypeStreaming
	}

	result, err := w.wrapped.ChatCompletion(request, opts...)
	if err != nil {
		return nil, err
	}

	interceptedStream := make(chan TextStreamEvent)
	effectiveModel := extractRequestedModel(opts...)
	dimensions := extractTokenUsageDimensions(request, w.botUsername, effectiveModel)

	go func() {
		defer close(interceptedStream)

		var aggregateUsage TokenUsage
		hasUsage := false

		for event := range result.Stream {
			switch event.Type {
			case EventTypeUsage:
				usage, ok := event.Value.(TokenUsage)
				if !ok {
					continue
				}
				hasUsage = true
				aggregateUsage.InputTokens += usage.InputTokens
				aggregateUsage.OutputTokens += usage.OutputTokens
				continue
			default:
				interceptedStream <- event
			}
		}

		if hasUsage {
			w.emitTokenUsage(dimensions, aggregateUsage)
		}
	}()

	return &TextStreamResult{Stream: interceptedStream}, nil
}

type tokenUsageDimensions struct {
	userID           string
	teamID           string
	channelID        string
	channelType      string
	botName          string
	botUsername      string
	botUserID        string
	model            string
	serviceType      string
	operation        string
	operationSubType string
}

func (w *TokenUsageLoggingWrapper) emitTokenUsage(dimensions tokenUsageDimensions, usage TokenUsage) {
	fields := buildTokenUsageLogKeyValuePairs(dimensions, usage)

	if pluginLogger := w.sinks.PluginLogger(); pluginLogger != nil {
		pluginLogger.Info("LLM token usage", fields...)
	}

	if tokenLogger := w.sinks.FileLogger(); tokenLogger != nil {
		tokenLogger.Info("Token Usage", tokenUsageKeyValuePairsToMlogFields(fields)...)
	}

	if w.metrics != nil {
		w.metrics.ObserveTokenUsage(
			dimensions.botUsername,
			dimensions.teamID,
			dimensions.userID,
			int64ToInt(usage.InputTokens),
			int64ToInt(usage.OutputTokens),
		)
	}
}

func buildTokenUsageLogKeyValuePairs(dimensions tokenUsageDimensions, usage TokenUsage) []any {
	totalTokens := usage.InputTokens + usage.OutputTokens
	return []any{
		"event", TokenUsageLogEvent,
		"schema_version", TokenUsageLogSchemaVersion,
		"user_id", dimensions.userID,
		"team_id", dimensions.teamID,
		"channel_id", dimensions.channelID,
		"channel_type", dimensions.channelType,
		"agent_name", dimensions.botName,
		"agent_username", dimensions.botUsername,
		// Temporary compatibility alias for existing dashboards/queries.
		"bot_username", dimensions.botUsername,
		"agent_user_id", dimensions.botUserID,
		"model", dimensions.model,
		"service_type", dimensions.serviceType,
		"operation", dimensions.operation,
		"operation_subtype", dimensions.operationSubType,
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"total_tokens", totalTokens,
	}
}

func tokenUsageKeyValuePairsToMlogFields(keyValuePairs []any) []mlog.Field {
	fields := make([]mlog.Field, 0, len(keyValuePairs)/2)
	for i := 0; i+1 < len(keyValuePairs); i += 2 {
		key, ok := keyValuePairs[i].(string)
		if !ok {
			continue
		}
		fields = append(fields, mlog.Any(key, keyValuePairs[i+1]))
	}
	return fields
}

func extractTokenUsageDimensions(request CompletionRequest, fallbackBotUsername, optionModel string) tokenUsageDimensions {
	dimensions := tokenUsageDimensions{
		userID:           TokenUsageUnknown,
		teamID:           TokenUsageUnknown,
		channelID:        TokenUsageUnknown,
		channelType:      TokenUsageUnknown,
		botName:          TokenUsageUnknown,
		botUsername:      fallbackBotUsername,
		botUserID:        TokenUsageUnknown,
		model:            TokenUsageUnknown,
		serviceType:      TokenUsageUnknown,
		operation:        request.Operation,
		operationSubType: request.OperationSubType,
	}

	if dimensions.botUsername == "" {
		dimensions.botUsername = TokenUsageUnknown
	}
	if dimensions.operation == "" {
		dimensions.operation = TokenUsageUnknown
	}
	if dimensions.operationSubType == "" {
		dimensions.operationSubType = TokenUsageUnknown
	}
	if request.Context != nil {
		if request.Context.RequestingUser != nil && request.Context.RequestingUser.Id != "" {
			dimensions.userID = request.Context.RequestingUser.Id
		}

		if request.Context.Team != nil && request.Context.Team.Id != "" {
			dimensions.teamID = request.Context.Team.Id
		} else if request.Context.Channel != nil {
			switch request.Context.Channel.Type {
			case model.ChannelTypeDirect:
				dimensions.teamID = "dm"
			case model.ChannelTypeGroup:
				dimensions.teamID = "group"
			}
		}

		if request.Context.Channel != nil {
			if request.Context.Channel.Id != "" {
				dimensions.channelID = request.Context.Channel.Id
			}
			dimensions.channelType = normalizeChannelType(request.Context.Channel.Type)
		}

		if request.Context.BotName != "" {
			dimensions.botName = request.Context.BotName
		}
		if request.Context.BotUsername != "" {
			dimensions.botUsername = request.Context.BotUsername
		}
		if request.Context.BotUserID != "" {
			dimensions.botUserID = request.Context.BotUserID
		}
		if request.Context.BotModel != "" {
			dimensions.model = request.Context.BotModel
		}
		if request.Context.BotServiceType != "" {
			dimensions.serviceType = request.Context.BotServiceType
		}
	}

	if optionModel != "" {
		dimensions.model = optionModel
	}
	if dimensions.botName == TokenUsageUnknown {
		dimensions.botName = dimensions.botUsername
	}

	return dimensions
}

func extractRequestedModel(opts ...LanguageModelOption) string {
	cfg := &LanguageModelConfig{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(cfg)
	}
	return cfg.Model
}

func normalizeChannelType(channelType model.ChannelType) string {
	switch channelType {
	case model.ChannelTypeOpen:
		return "open"
	case model.ChannelTypePrivate:
		return "private"
	case model.ChannelTypeDirect:
		return "direct"
	case model.ChannelTypeGroup:
		return "group"
	default:
		return TokenUsageUnknown
	}
}

func int64ToInt(value int64) int {
	maxInt := int64(^uint(0) >> 1)
	minInt := -maxInt - 1
	if value > maxInt {
		return int(maxInt)
	}
	if value < minInt {
		return int(minInt)
	}
	return int(value)
}

// ChatCompletionNoStream uses the streaming method internally, so token usage
// logging happens automatically when ReadAll() processes the intercepted stream
func (w *TokenUsageLoggingWrapper) ChatCompletionNoStream(request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	if request.OperationSubType == "" {
		request.OperationSubType = SubTypeNoStream
	}

	result, err := w.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (w *TokenUsageLoggingWrapper) shouldTrackTokenUsage() bool {
	return w != nil && w.sinks != nil && w.sinks.LoggingEnabled()
}

// CountTokens delegates to the wrapped model
func (w *TokenUsageLoggingWrapper) CountTokens(text string) int {
	return w.wrapped.CountTokens(text)
}

// InputTokenLimit delegates to the wrapped model
func (w *TokenUsageLoggingWrapper) InputTokenLimit() int {
	return w.wrapped.InputTokenLimit()
}
