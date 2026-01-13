// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package bifrost provides a unified LLM interface using the Bifrost gateway library.
// This package wraps Bifrost to implement the llm.LanguageModel interface, allowing
// the plugin to use multiple LLM providers through a single, consistent API.
package bifrost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

const (
	DefaultMaxTokens        = 8192
	MaxToolResolutionDepth  = 10
	DefaultStreamingTimeout = 30 * time.Second
)

// BifrostLLM implements the llm.LanguageModel interface using the Bifrost gateway.
type BifrostLLM struct {
	client           *core.Bifrost
	provider         schemas.ModelProvider
	defaultModel     string
	inputTokenLimit  int
	outputTokenLimit int
	streamingTimeout time.Duration
	sendUserID       bool

	// Native tools and reasoning configuration
	enabledNativeTools []string
	reasoningEnabled   bool
	reasoningEffort    string
	thinkingBudget     int
}

// Config holds the configuration for creating a BifrostLLM instance.
type Config struct {
	Provider           schemas.ModelProvider
	APIKey             string
	APIURL             string // Custom base URL (for Azure, OpenAI Compatible, etc.)
	OrgID              string
	Region             string // For AWS Bedrock
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	DefaultModel       string
	InputTokenLimit    int
	OutputTokenLimit   int
	StreamingTimeout   time.Duration
	SendUserID         bool

	// Native tools and reasoning configuration
	EnabledNativeTools []string
	ReasoningEnabled   bool
	ReasoningEffort    string
	ThinkingBudget     int
}

// providerAccount implements the Bifrost Account interface for a single provider.
type providerAccount struct {
	provider   schemas.ModelProvider
	apiKey     string
	apiURL     string
	orgID      string
	region     string
	awsKeyID   string
	awsSecret  string
	httpClient *http.Client
}

func (a *providerAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return []schemas.ModelProvider{a.provider}, nil
}

func (a *providerAccount) GetKeysForProvider(ctx *context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	if provider != a.provider {
		return nil, fmt.Errorf("provider %s not supported", provider)
	}

	key := schemas.Key{
		Value:  a.apiKey,
		Weight: 1.0,
	}

	return []schemas.Key{key}, nil
}

func (a *providerAccount) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	if provider != a.provider {
		return nil, fmt.Errorf("provider %s not supported", provider)
	}

	config := &schemas.ProviderConfig{
		NetworkConfig:            schemas.DefaultNetworkConfig,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
	}

	// Set custom base URL if provided
	if a.apiURL != "" {
		config.BaseURL = &a.apiURL
	}

	return config, nil
}

// New creates a new BifrostLLM instance with the given configuration.
func New(cfg Config, httpClient *http.Client) (*BifrostLLM, error) {
	account := &providerAccount{
		provider:   cfg.Provider,
		apiKey:     cfg.APIKey,
		apiURL:     cfg.APIURL,
		orgID:      cfg.OrgID,
		region:     cfg.Region,
		awsKeyID:   cfg.AWSAccessKeyID,
		awsSecret:  cfg.AWSSecretAccessKey,
		httpClient: httpClient,
	}

	bifrostConfig := schemas.BifrostConfig{
		Account: account,
	}

	client, err := core.Init(context.Background(), bifrostConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Bifrost client: %w", err)
	}

	streamingTimeout := cfg.StreamingTimeout
	if streamingTimeout == 0 {
		streamingTimeout = DefaultStreamingTimeout
	}

	outputLimit := cfg.OutputTokenLimit
	if outputLimit == 0 {
		outputLimit = DefaultMaxTokens
	}

	return &BifrostLLM{
		client:             client,
		provider:           cfg.Provider,
		defaultModel:       cfg.DefaultModel,
		inputTokenLimit:    cfg.InputTokenLimit,
		outputTokenLimit:   outputLimit,
		streamingTimeout:   streamingTimeout,
		sendUserID:         cfg.SendUserID,
		enabledNativeTools: cfg.EnabledNativeTools,
		reasoningEnabled:   cfg.ReasoningEnabled,
		reasoningEffort:    cfg.ReasoningEffort,
		thinkingBudget:     cfg.ThinkingBudget,
	}, nil
}

// Shutdown gracefully shuts down the Bifrost client.
func (b *BifrostLLM) Shutdown() {
	if b.client != nil {
		b.client.Shutdown()
	}
}

// GetDefaultConfig returns the default language model configuration.
func (b *BifrostLLM) GetDefaultConfig() llm.LanguageModelConfig {
	return llm.LanguageModelConfig{
		Model:              b.defaultModel,
		MaxGeneratedTokens: b.outputTokenLimit,
	}
}

func (b *BifrostLLM) createConfig(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := b.GetDefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// ChatCompletion performs a streaming chat completion request.
func (b *BifrostLLM) ChatCompletion(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	cfg := b.createConfig(opts)
	eventStream := make(chan llm.TextStreamEvent)

	go func() {
		defer close(eventStream)
		b.streamChat(request, cfg, eventStream)
	}()

	return &llm.TextStreamResult{Stream: eventStream}, nil
}

// ChatCompletionNoStream performs a non-streaming chat completion request.
func (b *BifrostLLM) ChatCompletionNoStream(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	result, err := b.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

// CountTokens estimates the token count for the given text.
func (b *BifrostLLM) CountTokens(text string) int {
	// Approximation based on character and word counts
	charCount := float64(len(text)) / 4.0
	wordCount := float64(len(strings.Fields(text))) / 0.75
	return int((charCount + wordCount) / 2.0)
}

// InputTokenLimit returns the maximum number of input tokens supported.
func (b *BifrostLLM) InputTokenLimit() int {
	if b.inputTokenLimit > 0 {
		return b.inputTokenLimit
	}

	// Default limits based on provider
	switch b.provider {
	case schemas.OpenAI, schemas.Anthropic:
		return 128000
	case schemas.Bedrock:
		return 200000
	default:
		return 128000
	}
}

// streamChat handles the streaming chat completion.
func (b *BifrostLLM) streamChat(request llm.CompletionRequest, cfg llm.LanguageModelConfig, output chan<- llm.TextStreamEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), b.streamingTimeout*10)
	defer cancel()

	// Create Bifrost context
	bifrostCtx := schemas.NewBifrostContext(ctx, time.Now().Add(b.streamingTimeout*10))

	// Convert to Bifrost chat request
	chatReq := b.convertToBifrostRequest(request, cfg)

	// Make streaming request
	streamChan, bifrostErr := b.client.ChatCompletionStreamRequest(bifrostCtx, chatReq)
	if bifrostErr != nil {
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: fmt.Errorf("bifrost error: %s", core.GetErrorMessage(bifrostErr)),
		}
		return
	}

	// Process stream
	var toolCalls []llm.ToolCall
	var toolCallsBuffer map[int]*toolCallBuffer

	// Watchdog timer for streaming timeout
	watchdog := make(chan struct{})
	var watchdogMu sync.Mutex

	go func() {
		timer := time.NewTimer(b.streamingTimeout)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				cancel()
				return
			case <-ctx.Done():
				return
			case <-watchdog:
				watchdogMu.Lock()
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(b.streamingTimeout)
				watchdogMu.Unlock()
			}
		}
	}()

	for chunk := range streamChan {
		// Ping watchdog
		select {
		case watchdog <- struct{}{}:
		default:
		}

		if chunk.BifrostError != nil {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: fmt.Errorf("stream error: %s", core.GetErrorMessage(chunk.BifrostError)),
			}
			return
		}

		// Process chat completion chunk
		if chunk.ChatCompletionChunk != nil {
			cc := chunk.ChatCompletionChunk
			if cc.Choices != nil && len(*cc.Choices) > 0 {
				choice := (*cc.Choices)[0]

				// Handle text content
				if choice.Delta != nil && choice.Delta.Content != nil && choice.Delta.Content.ContentStr != nil {
					content := *choice.Delta.Content.ContentStr
					if content != "" {
						output <- llm.TextStreamEvent{
							Type:  llm.EventTypeText,
							Value: content,
						}
					}
				}

				// Handle tool calls
				if choice.Delta != nil && choice.Delta.ToolCalls != nil {
					if toolCallsBuffer == nil {
						toolCallsBuffer = make(map[int]*toolCallBuffer)
					}
					for _, tc := range *choice.Delta.ToolCalls {
						idx := 0
						if tc.Index != nil {
							idx = *tc.Index
						}
						if toolCallsBuffer[idx] == nil {
							toolCallsBuffer[idx] = &toolCallBuffer{}
						}
						if tc.ID != nil {
							toolCallsBuffer[idx].id = *tc.ID
						}
						if tc.Function != nil {
							if tc.Function.Name != nil {
								toolCallsBuffer[idx].name = *tc.Function.Name
							}
							if tc.Function.Arguments != nil {
								toolCallsBuffer[idx].arguments.WriteString(*tc.Function.Arguments)
							}
						}
					}
				}

				// Check finish reason
				if choice.FinishReason != nil {
					switch *choice.FinishReason {
					case "tool_calls":
						// Convert buffered tool calls
						for _, buf := range toolCallsBuffer {
							toolCalls = append(toolCalls, llm.ToolCall{
								ID:        buf.id,
								Name:      buf.name,
								Arguments: []byte(buf.arguments.String()),
							})
						}
						if len(toolCalls) > 0 {
							output <- llm.TextStreamEvent{
								Type:  llm.EventTypeToolCalls,
								Value: toolCalls,
							}
							return
						}
					case "stop":
						// Normal completion
					}
				}
			}

			// Handle usage data
			if cc.Usage != nil {
				usage := llm.TokenUsage{}
				if cc.Usage.PromptTokens != nil {
					usage.InputTokens = int64(*cc.Usage.PromptTokens)
				}
				if cc.Usage.CompletionTokens != nil {
					usage.OutputTokens = int64(*cc.Usage.CompletionTokens)
				}
				if usage.InputTokens > 0 || usage.OutputTokens > 0 {
					output <- llm.TextStreamEvent{
						Type:  llm.EventTypeUsage,
						Value: usage,
					}
				}
			}
		}

		// Check if this is the final chunk
		if core.IsFinalChunk(bifrostCtx) {
			break
		}
	}

	// If we have pending tool calls, emit them
	if len(toolCallsBuffer) > 0 && len(toolCalls) == 0 {
		for _, buf := range toolCallsBuffer {
			if buf.name != "" {
				toolCalls = append(toolCalls, llm.ToolCall{
					ID:        buf.id,
					Name:      buf.name,
					Arguments: []byte(buf.arguments.String()),
				})
			}
		}
		if len(toolCalls) > 0 {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeToolCalls,
				Value: toolCalls,
			}
			return
		}
	}

	output <- llm.TextStreamEvent{
		Type:  llm.EventTypeEnd,
		Value: nil,
	}
}

type toolCallBuffer struct {
	id        string
	name      string
	arguments strings.Builder
}

// convertToBifrostRequest converts our CompletionRequest to Bifrost's format.
func (b *BifrostLLM) convertToBifrostRequest(request llm.CompletionRequest, cfg llm.LanguageModelConfig) *schemas.BifrostChatRequest {
	messages := b.convertMessages(request.Posts)
	tools := b.convertTools(request, cfg)

	req := &schemas.BifrostChatRequest{
		Provider: b.provider,
		Model:    cfg.Model,
		Input:    messages,
	}

	// Set parameters
	params := &schemas.ChatParameters{}
	if cfg.MaxGeneratedTokens > 0 {
		params.MaxTokens = core.Ptr(cfg.MaxGeneratedTokens)
	}
	if len(tools) > 0 {
		params.Tools = tools
	}
	req.Params = params

	return req
}

// convertMessages converts llm.Post messages to Bifrost ChatMessage format.
func (b *BifrostLLM) convertMessages(posts []llm.Post) []schemas.ChatMessage {
	messages := make([]schemas.ChatMessage, 0, len(posts))

	for _, post := range posts {
		var msg schemas.ChatMessage

		switch post.Role {
		case llm.PostRoleSystem:
			msg = schemas.ChatMessage{
				Role: schemas.ChatMessageRoleSystem,
				Content: &schemas.ChatMessageContent{
					ContentStr: core.Ptr(post.Message),
				},
			}

		case llm.PostRoleUser:
			if len(post.Files) > 0 {
				// Multimodal message with images
				parts := b.createMultimodalContent(post)
				msg = schemas.ChatMessage{
					Role: schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{
						ContentParts: &parts,
					},
				}
			} else {
				msg = schemas.ChatMessage{
					Role: schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{
						ContentStr: core.Ptr(post.Message),
					},
				}
			}

		case llm.PostRoleBot:
			msg = schemas.ChatMessage{
				Role: schemas.ChatMessageRoleAssistant,
				Content: &schemas.ChatMessageContent{
					ContentStr: core.Ptr(post.Message),
				},
			}

			// Handle tool calls in assistant messages
			if len(post.ToolUse) > 0 {
				toolCalls := make([]schemas.ChatAssistantMessageToolCall, 0, len(post.ToolUse))
				for _, tc := range post.ToolUse {
					toolCalls = append(toolCalls, schemas.ChatAssistantMessageToolCall{
						ID:   tc.ID,
						Type: "function",
						Function: schemas.ChatAssistantMessageToolCallFunction{
							Name:      tc.Name,
							Arguments: string(tc.Arguments),
						},
					})
				}
				msg.ToolCalls = &toolCalls

				// Add tool result messages
				for _, tc := range post.ToolUse {
					toolResultMsg := schemas.ChatMessage{
						Role:       schemas.ChatMessageRoleTool,
						ToolCallID: core.Ptr(tc.ID),
						Content: &schemas.ChatMessageContent{
							ContentStr: core.Ptr(tc.Result),
						},
					}
					messages = append(messages, msg, toolResultMsg)
				}
				continue // Skip adding msg again
			}
		}

		messages = append(messages, msg)
	}

	return messages
}

// createMultimodalContent creates content parts for messages with images.
func (b *BifrostLLM) createMultimodalContent(post llm.Post) []schemas.ChatMessageContentPart {
	parts := make([]schemas.ChatMessageContentPart, 0, len(post.Files)+1)

	if post.Message != "" {
		parts = append(parts, schemas.ChatMessageContentPart{
			Type: "text",
			Text: core.Ptr(post.Message),
		})
	}

	for _, file := range post.Files {
		if !isValidImageType(file.MimeType) {
			parts = append(parts, schemas.ChatMessageContentPart{
				Type: "text",
				Text: core.Ptr(fmt.Sprintf("[Unsupported image type: %s]", file.MimeType)),
			})
			continue
		}

		data, err := io.ReadAll(file.Reader)
		if err != nil {
			parts = append(parts, schemas.ChatMessageContentPart{
				Type: "text",
				Text: core.Ptr("[Error reading image data]"),
			})
			continue
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		dataURL := fmt.Sprintf("data:%s;base64,%s", file.MimeType, encoded)

		parts = append(parts, schemas.ChatMessageContentPart{
			Type: "image_url",
			ImageURL: &schemas.ChatMessageContentPartImageURL{
				URL: dataURL,
			},
		})
	}

	return parts
}

// convertTools converts llm.Tool to Bifrost ChatTool format.
func (b *BifrostLLM) convertTools(request llm.CompletionRequest, cfg llm.LanguageModelConfig) []schemas.ChatTool {
	if cfg.ToolsDisabled || request.Context == nil || request.Context.Tools == nil {
		return nil
	}

	tools := request.Context.Tools.GetTools()
	result := make([]schemas.ChatTool, 0, len(tools))

	for _, tool := range tools {
		// Convert schema to map
		var schemaMap map[string]interface{}
		if tool.Schema != nil {
			// Handle different schema types
			switch s := tool.Schema.(type) {
			case map[string]interface{}:
				schemaMap = s
			default:
				// Marshal and unmarshal to convert to map
				data, err := json.Marshal(tool.Schema)
				if err == nil {
					json.Unmarshal(data, &schemaMap)
				}
			}
		}

		// Ensure schema has required fields
		if schemaMap == nil {
			schemaMap = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}
		if _, ok := schemaMap["type"]; !ok {
			schemaMap["type"] = "object"
		}
		if _, ok := schemaMap["properties"]; !ok {
			schemaMap["properties"] = map[string]interface{}{}
		}

		chatTool := schemas.ChatTool{
			Type: schemas.ChatToolTypeFunction,
			Function: &schemas.ChatToolFunction{
				Name:        tool.Name,
				Description: core.Ptr(tool.Description),
				Parameters:  schemaMap,
			},
		}
		result = append(result, chatTool)
	}

	return result
}

// isValidImageType checks if the MIME type is supported.
func isValidImageType(mimeType string) bool {
	validTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/gif":  true,
		"image/webp": true,
	}
	return validTypes[mimeType]
}
