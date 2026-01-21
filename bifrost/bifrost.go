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

	bifrostcore "github.com/maximhq/bifrost/core"
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
	client           *bifrostcore.Bifrost
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
	provider  schemas.ModelProvider
	apiKey    string
	apiURL    string
	orgID     string
	region    string
	awsKeyID  string
	awsSecret string
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

	// Handle Azure config
	if a.provider == schemas.Azure && a.apiURL != "" {
		key.AzureKeyConfig = &schemas.AzureKeyConfig{
			Endpoint: a.apiURL,
		}
	}

	// Handle Bedrock config
	if a.provider == schemas.Bedrock {
		key.BedrockKeyConfig = &schemas.BedrockKeyConfig{
			AccessKey: a.awsKeyID,
			SecretKey: a.awsSecret,
			Region:    &a.region,
		}
	}

	return []schemas.Key{key}, nil
}

func (a *providerAccount) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	if provider != a.provider {
		return nil, fmt.Errorf("provider %s not supported", provider)
	}

	networkConfig := schemas.DefaultNetworkConfig

	// Use BaseURL for providers that support custom endpoints (not Azure, which uses AzureKeyConfig)
	if a.apiURL != "" && a.provider != schemas.Azure {
		networkConfig.BaseURL = a.apiURL
	}

	// Pass OrgID via ExtraHeaders for OpenAI
	if a.orgID != "" && a.provider == schemas.OpenAI {
		networkConfig.ExtraHeaders = map[string]string{
			"OpenAI-Organization": a.orgID,
		}
	}

	// Configure retry logic with sensible defaults
	networkConfig.MaxRetries = 2
	networkConfig.RetryBackoffInitial = 1 * time.Second
	networkConfig.RetryBackoffMax = 10 * time.Second

	config := &schemas.ProviderConfig{
		NetworkConfig:            networkConfig,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
	}

	return config, nil
}

// New creates a new BifrostLLM instance with the given configuration.
// Note: httpClient is kept for API compatibility but Bifrost manages its own HTTP client.
func New(cfg Config, httpClient *http.Client) (*BifrostLLM, error) {
	account := &providerAccount{
		provider:  cfg.Provider,
		apiKey:    cfg.APIKey,
		apiURL:    cfg.APIURL,
		orgID:     cfg.OrgID,
		region:    cfg.Region,
		awsKeyID:  cfg.AWSAccessKeyID,
		awsSecret: cfg.AWSSecretAccessKey,
	}

	bifrostConfig := schemas.BifrostConfig{
		Account: account,
	}

	client, err := bifrostcore.Init(context.Background(), bifrostConfig)
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

	// Convert to Bifrost request
	bifrostReq := b.convertToBifrostRequest(request, cfg)

	// Make streaming request
	streamChan, bifrostErr := b.client.ChatCompletionStreamRequest(ctx, bifrostReq)
	if bifrostErr != nil {
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: fmt.Errorf("bifrost error: %s", bifrostErr.Error.Message),
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
				Value: fmt.Errorf("stream error: %s", chunk.BifrostError.Error.Message),
			}
			return
		}

		// Process response chunk
		if chunk.BifrostResponse != nil {
			resp := chunk.BifrostResponse
			if resp.Choices != nil && len(resp.Choices) > 0 {
				choice := resp.Choices[0]

				// Handle text content from delta (streaming)
				if choice.BifrostStreamResponseChoice != nil && choice.Delta.Content != nil {
					content := *choice.Delta.Content
					if content != "" {
						output <- llm.TextStreamEvent{
							Type:  llm.EventTypeText,
							Value: content,
						}
					}
				}

				// Handle tool calls (streaming)
				if choice.BifrostStreamResponseChoice != nil && len(choice.Delta.ToolCalls) > 0 {
					if toolCallsBuffer == nil {
						toolCallsBuffer = make(map[int]*toolCallBuffer)
					}
					for idx, tc := range choice.Delta.ToolCalls {
						if toolCallsBuffer[idx] == nil {
							toolCallsBuffer[idx] = &toolCallBuffer{}
						}
						if tc.ID != nil {
							toolCallsBuffer[idx].id = *tc.ID
						}
						if tc.Function.Name != nil {
							toolCallsBuffer[idx].name = *tc.Function.Name
						}
						toolCallsBuffer[idx].arguments.WriteString(tc.Function.Arguments)
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
			if resp.Usage != nil {
				usage := llm.TokenUsage{
					InputTokens:  int64(resp.Usage.PromptTokens),
					OutputTokens: int64(resp.Usage.CompletionTokens),
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
		if bifrostcore.IsFinalChunk(&ctx) {
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
func (b *BifrostLLM) convertToBifrostRequest(request llm.CompletionRequest, cfg llm.LanguageModelConfig) *schemas.BifrostRequest {
	messages := b.convertMessages(request.Posts)
	tools := b.convertTools(request, cfg)

	req := &schemas.BifrostRequest{
		Provider: b.provider,
		Model:    cfg.Model,
		Input: schemas.RequestInput{
			ChatCompletionInput: &messages,
		},
	}

	// Set parameters
	params := &schemas.ModelParameters{}
	if cfg.MaxGeneratedTokens > 0 {
		params.MaxTokens = Ptr(cfg.MaxGeneratedTokens)
	}
	if len(tools) > 0 {
		params.Tools = &tools
	}
	req.Params = params

	return req
}

// convertMessages converts llm.Post messages to Bifrost BifrostMessage format.
func (b *BifrostLLM) convertMessages(posts []llm.Post) []schemas.BifrostMessage {
	messages := make([]schemas.BifrostMessage, 0, len(posts))

	for _, post := range posts {
		var msg schemas.BifrostMessage

		switch post.Role {
		case llm.PostRoleSystem:
			msg = schemas.BifrostMessage{
				Role: schemas.ModelChatMessageRoleSystem,
				Content: schemas.MessageContent{
					ContentStr: Ptr(post.Message),
				},
			}

		case llm.PostRoleUser:
			if len(post.Files) > 0 {
				// Multimodal message with images
				parts := b.createMultimodalContent(post)
				msg = schemas.BifrostMessage{
					Role: schemas.ModelChatMessageRoleUser,
					Content: schemas.MessageContent{
						ContentBlocks: &parts,
					},
				}
			} else {
				msg = schemas.BifrostMessage{
					Role: schemas.ModelChatMessageRoleUser,
					Content: schemas.MessageContent{
						ContentStr: Ptr(post.Message),
					},
				}
			}

		case llm.PostRoleBot:
			msg = schemas.BifrostMessage{
				Role: schemas.ModelChatMessageRoleAssistant,
				Content: schemas.MessageContent{
					ContentStr: Ptr(post.Message),
				},
			}

			// Handle tool calls in assistant messages
			if len(post.ToolUse) > 0 {
				toolCalls := make([]schemas.ToolCall, 0, len(post.ToolUse))
				for _, tc := range post.ToolUse {
					toolCalls = append(toolCalls, schemas.ToolCall{
						ID:   Ptr(tc.ID),
						Type: Ptr("function"),
						Function: schemas.FunctionCall{
							Name:      Ptr(tc.Name),
							Arguments: string(tc.Arguments),
						},
					})
				}
				msg.AssistantMessage = &schemas.AssistantMessage{
					ToolCalls: &toolCalls,
				}

				// Add the assistant message with tool calls
				messages = append(messages, msg)

				// Add tool result messages
				for _, tc := range post.ToolUse {
					toolResultMsg := schemas.BifrostMessage{
						Role: schemas.ModelChatMessageRoleTool,
						Content: schemas.MessageContent{
							ContentStr: Ptr(tc.Result),
						},
						ToolMessage: &schemas.ToolMessage{
							ToolCallID: Ptr(tc.ID),
						},
					}
					messages = append(messages, toolResultMsg)
				}
				continue // Skip adding msg again
			}
		}

		messages = append(messages, msg)
	}

	return messages
}

// createMultimodalContent creates content blocks for messages with images.
func (b *BifrostLLM) createMultimodalContent(post llm.Post) []schemas.ContentBlock {
	parts := make([]schemas.ContentBlock, 0, len(post.Files)+1)

	if post.Message != "" {
		parts = append(parts, schemas.ContentBlock{
			Type: "text",
			Text: Ptr(post.Message),
		})
	}

	for _, file := range post.Files {
		if !isValidImageType(file.MimeType) {
			parts = append(parts, schemas.ContentBlock{
				Type: "text",
				Text: Ptr(fmt.Sprintf("[Unsupported image type: %s]", file.MimeType)),
			})
			continue
		}

		data, err := io.ReadAll(file.Reader)
		if err != nil {
			parts = append(parts, schemas.ContentBlock{
				Type: "text",
				Text: Ptr("[Error reading image data]"),
			})
			continue
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		dataURL := fmt.Sprintf("data:%s;base64,%s", file.MimeType, encoded)

		parts = append(parts, schemas.ContentBlock{
			Type: "image_url",
			ImageURL: &schemas.ImageURLStruct{
				URL: dataURL,
			},
		})
	}

	return parts
}

// convertTools converts llm.Tool to Bifrost Tool format.
func (b *BifrostLLM) convertTools(request llm.CompletionRequest, cfg llm.LanguageModelConfig) []schemas.Tool {
	if cfg.ToolsDisabled || request.Context == nil || request.Context.Tools == nil {
		return nil
	}

	tools := request.Context.Tools.GetTools()
	result := make([]schemas.Tool, 0, len(tools))

	for _, tool := range tools {
		// Convert schema to FunctionParameters
		var params schemas.FunctionParameters
		if tool.Schema != nil {
			switch s := tool.Schema.(type) {
			case map[string]interface{}:
				params = schemaMapToFunctionParams(s)
			default:
				// Marshal and unmarshal to convert to map
				data, err := json.Marshal(tool.Schema)
				if err == nil {
					var schemaMap map[string]interface{}
					if json.Unmarshal(data, &schemaMap) == nil {
						params = schemaMapToFunctionParams(schemaMap)
					}
				}
			}
		}

		// Ensure params has default values
		if params.Type == "" {
			params.Type = "object"
		}
		if params.Properties == nil {
			params.Properties = map[string]interface{}{}
		}

		bifrostTool := schemas.Tool{
			Type: "function",
			Function: schemas.Function{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  params,
			},
		}
		result = append(result, bifrostTool)
	}

	return result
}

// schemaMapToFunctionParams converts a schema map to FunctionParameters
func schemaMapToFunctionParams(schemaMap map[string]interface{}) schemas.FunctionParameters {
	params := schemas.FunctionParameters{
		Type: "object",
	}

	if t, ok := schemaMap["type"].(string); ok {
		params.Type = t
	}
	if desc, ok := schemaMap["description"].(string); ok {
		params.Description = &desc
	}
	if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
		params.Properties = props
	}
	if req, ok := schemaMap["required"].([]interface{}); ok {
		required := make([]string, 0, len(req))
		for _, r := range req {
			if s, ok := r.(string); ok {
				required = append(required, s)
			}
		}
		params.Required = required
	}

	return params
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

// Ptr is a helper function to create a pointer to a value.
func Ptr[T any](v T) *T {
	return &v
}
