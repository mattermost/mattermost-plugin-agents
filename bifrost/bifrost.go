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

// LLM implements the llm.LanguageModel interface using the Bifrost gateway.
type LLM struct {
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

	// UseResponsesAPI enables OpenAI Responses API for native tools support
	useResponsesAPI bool
}

// Config holds the configuration for creating a LLM instance.
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

	// UseResponsesAPI enables OpenAI Responses API for native tools support
	UseResponsesAPI bool
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

func (a *providerAccount) GetKeysForProvider(ctx context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	if provider != a.provider {
		return nil, fmt.Errorf("provider %s not supported", provider)
	}

	key := schemas.Key{
		Value:  schemas.EnvVar{Val: a.apiKey},
		Weight: 1.0,
	}

	// Handle Azure config
	if a.provider == schemas.Azure && a.apiURL != "" {
		key.AzureKeyConfig = &schemas.AzureKeyConfig{
			Endpoint: schemas.EnvVar{Val: a.apiURL},
		}
	}

	// Handle Bedrock config
	if a.provider == schemas.Bedrock {
		region := schemas.EnvVar{Val: a.region}
		key.BedrockKeyConfig = &schemas.BedrockKeyConfig{
			AccessKey: schemas.EnvVar{Val: a.awsKeyID},
			SecretKey: schemas.EnvVar{Val: a.awsSecret},
			Region:    &region,
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

// New creates a new LLM instance with the given configuration.
// Note: httpClient is kept for API compatibility but Bifrost manages its own HTTP client.
func New(cfg Config, httpClient *http.Client) (*LLM, error) {
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

	return &LLM{
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
		useResponsesAPI:    cfg.UseResponsesAPI,
	}, nil
}

// Shutdown gracefully shuts down the Bifrost client.
func (b *LLM) Shutdown() {
	if b.client != nil {
		b.client.Shutdown()
	}
}

// GetDefaultConfig returns the default language model configuration.
func (b *LLM) GetDefaultConfig() llm.LanguageModelConfig {
	return llm.LanguageModelConfig{
		Model:              b.defaultModel,
		MaxGeneratedTokens: b.outputTokenLimit,
	}
}

func (b *LLM) createConfig(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := b.GetDefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// ChatCompletion performs a streaming chat completion request.
func (b *LLM) ChatCompletion(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	cfg := b.createConfig(opts)
	eventStream := make(chan llm.TextStreamEvent)

	go func() {
		defer close(eventStream)
		if b.shouldUseResponsesAPI() {
			b.streamResponses(request, cfg, eventStream)
		} else {
			b.streamChat(request, cfg, eventStream)
		}
	}()

	return &llm.TextStreamResult{Stream: eventStream}, nil
}

// ChatCompletionNoStream performs a non-streaming chat completion request.
func (b *LLM) ChatCompletionNoStream(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	result, err := b.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

// CountTokens estimates the token count for the given text.
func (b *LLM) CountTokens(text string) int {
	// Approximation based on character and word counts
	charCount := float64(len(text)) / 4.0
	wordCount := float64(len(strings.Fields(text))) / 0.75
	return int((charCount + wordCount) / 2.0)
}

// InputTokenLimit returns the maximum number of input tokens supported.
func (b *LLM) InputTokenLimit() int {
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
func (b *LLM) streamChat(request llm.CompletionRequest, cfg llm.LanguageModelConfig, output chan<- llm.TextStreamEvent) {
	bifrostCtx, cancel := schemas.NewBifrostContextWithTimeout(context.Background(), b.streamingTimeout*10)
	defer cancel()

	// Convert to Bifrost request
	bifrostReq := b.convertToBifrostRequest(request, cfg)

	// Make streaming request
	streamChan, bifrostErr := b.client.ChatCompletionStreamRequest(bifrostCtx, bifrostReq)
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
			case <-bifrostCtx.Done():
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
		if chunk.BifrostChatResponse != nil {
			resp := chunk.BifrostChatResponse
			if len(resp.Choices) > 0 {
				choice := resp.Choices[0]

				// Handle text content from delta (streaming)
				if choice.ChatStreamResponseChoice != nil && choice.Delta != nil && choice.Delta.Content != nil {
					content := *choice.Delta.Content
					if content != "" {
						output <- llm.TextStreamEvent{
							Type:  llm.EventTypeText,
							Value: content,
						}
					}
				}

				// Handle tool calls (streaming)
				if choice.ChatStreamResponseChoice != nil && choice.Delta != nil && len(choice.Delta.ToolCalls) > 0 {
					if toolCallsBuffer == nil {
						toolCallsBuffer = make(map[int]*toolCallBuffer)
					}
					for _, tc := range choice.Delta.ToolCalls {
						idx := int(tc.Index)
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
		if bifrostcore.IsFinalChunk(bifrostCtx) {
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

// buildChatReasoning creates a ChatReasoning configuration if reasoning is enabled.
func (b *LLM) buildChatReasoning() *schemas.ChatReasoning {
	if !b.reasoningEnabled {
		return nil
	}
	reasoning := &schemas.ChatReasoning{}
	effort := b.reasoningEffort
	if effort == "" {
		effort = "medium"
	}
	reasoning.Effort = Ptr(effort)
	// Anthropic requires MaxTokens for thinking
	if b.provider == schemas.Anthropic && b.thinkingBudget > 0 {
		reasoning.MaxTokens = Ptr(b.thinkingBudget)
	}
	return reasoning
}

// convertToBifrostRequest converts our CompletionRequest to Bifrost's format.
func (b *LLM) convertToBifrostRequest(request llm.CompletionRequest, cfg llm.LanguageModelConfig) *schemas.BifrostChatRequest {
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
		params.MaxCompletionTokens = Ptr(cfg.MaxGeneratedTokens)
	}
	if len(tools) > 0 {
		params.Tools = tools
	}
	// Apply reasoning configuration
	if b.reasoningEnabled {
		params.Reasoning = b.buildChatReasoning()
	}
	req.Params = params

	return req
}

// convertMessages converts llm.Post messages to Bifrost ChatMessage format.
func (b *LLM) convertMessages(posts []llm.Post) []schemas.ChatMessage {
	messages := make([]schemas.ChatMessage, 0, len(posts))

	for _, post := range posts {
		var msg schemas.ChatMessage

		switch post.Role {
		case llm.PostRoleSystem:
			msg = schemas.ChatMessage{
				Role: schemas.ChatMessageRoleSystem,
				Content: &schemas.ChatMessageContent{
					ContentStr: Ptr(post.Message),
				},
			}

		case llm.PostRoleUser:
			if len(post.Files) > 0 {
				// Multimodal message with images
				parts := b.createMultimodalContent(post)
				msg = schemas.ChatMessage{
					Role: schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{
						ContentBlocks: parts,
					},
				}
			} else {
				msg = schemas.ChatMessage{
					Role: schemas.ChatMessageRoleUser,
					Content: &schemas.ChatMessageContent{
						ContentStr: Ptr(post.Message),
					},
				}
			}

		case llm.PostRoleBot:
			msg = schemas.ChatMessage{
				Role: schemas.ChatMessageRoleAssistant,
				Content: &schemas.ChatMessageContent{
					ContentStr: Ptr(post.Message),
				},
			}

			// Handle tool calls in assistant messages
			if len(post.ToolUse) > 0 {
				toolCalls := make([]schemas.ChatAssistantMessageToolCall, 0, len(post.ToolUse))
				for i, tc := range post.ToolUse {
					toolCalls = append(toolCalls, schemas.ChatAssistantMessageToolCall{
						Index: uint16(i % 65536), //nolint:gosec // index will never exceed uint16 max in practice
						ID:    Ptr(tc.ID),
						Type:  Ptr("function"),
						Function: schemas.ChatAssistantMessageToolCallFunction{
							Name:      Ptr(tc.Name),
							Arguments: string(tc.Arguments),
						},
					})
				}
				msg.ChatAssistantMessage = &schemas.ChatAssistantMessage{
					ToolCalls: toolCalls,
				}

				// Add the assistant message with tool calls
				messages = append(messages, msg)

				// Add tool result messages
				for _, tc := range post.ToolUse {
					toolResultMsg := schemas.ChatMessage{
						Role: schemas.ChatMessageRoleTool,
						Content: &schemas.ChatMessageContent{
							ContentStr: Ptr(tc.Result),
						},
						ChatToolMessage: &schemas.ChatToolMessage{
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
func (b *LLM) createMultimodalContent(post llm.Post) []schemas.ChatContentBlock {
	parts := make([]schemas.ChatContentBlock, 0, len(post.Files)+1)

	if post.Message != "" {
		parts = append(parts, schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeText,
			Text: Ptr(post.Message),
		})
	}

	for _, file := range post.Files {
		if !isValidImageType(file.MimeType) {
			parts = append(parts, schemas.ChatContentBlock{
				Type: schemas.ChatContentBlockTypeText,
				Text: Ptr(fmt.Sprintf("[Unsupported image type: %s]", file.MimeType)),
			})
			continue
		}

		data, err := io.ReadAll(file.Reader)
		if err != nil {
			parts = append(parts, schemas.ChatContentBlock{
				Type: schemas.ChatContentBlockTypeText,
				Text: Ptr("[Error reading image data]"),
			})
			continue
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		dataURL := fmt.Sprintf("data:%s;base64,%s", file.MimeType, encoded)

		parts = append(parts, schemas.ChatContentBlock{
			Type: "image_url",
			ImageURLStruct: &schemas.ChatInputImage{
				URL: dataURL,
			},
		})
	}

	return parts
}

// convertTools converts llm.Tool to Bifrost ChatTool format.
func (b *LLM) convertTools(request llm.CompletionRequest, cfg llm.LanguageModelConfig) []schemas.ChatTool {
	if cfg.ToolsDisabled || request.Context == nil || request.Context.Tools == nil {
		return nil
	}

	tools := request.Context.Tools.GetTools()
	result := make([]schemas.ChatTool, 0, len(tools))

	for _, tool := range tools {
		// Convert schema to ToolFunctionParameters
		var params *schemas.ToolFunctionParameters
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
		if params == nil {
			params = &schemas.ToolFunctionParameters{
				Type: "object",
			}
		}
		if params.Type == "" {
			params.Type = "object"
		}

		bifrostTool := schemas.ChatTool{
			Type: schemas.ChatToolTypeFunction,
			Function: &schemas.ChatToolFunction{
				Name:        tool.Name,
				Description: Ptr(tool.Description),
				Parameters:  params,
			},
		}
		result = append(result, bifrostTool)
	}

	return result
}

// schemaMapToFunctionParams converts a schema map to ToolFunctionParameters
func schemaMapToFunctionParams(schemaMap map[string]interface{}) *schemas.ToolFunctionParameters {
	params := &schemas.ToolFunctionParameters{
		Type: "object",
	}

	if t, ok := schemaMap["type"].(string); ok {
		params.Type = t
	}
	if desc, ok := schemaMap["description"].(string); ok {
		params.Description = &desc
	}
	if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
		params.Properties = (*schemas.OrderedMap)(&props)
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

// shouldUseResponsesAPI determines if the Responses API should be used for this request.
func (b *LLM) shouldUseResponsesAPI() bool {
	return b.useResponsesAPI && len(b.enabledNativeTools) > 0
}

// convertToResponsesMessages converts llm.Post messages to Bifrost ResponsesMessage format.
func (b *LLM) convertToResponsesMessages(posts []llm.Post) []schemas.ResponsesMessage {
	messages := make([]schemas.ResponsesMessage, 0, len(posts))

	for _, post := range posts {
		switch post.Role {
		case llm.PostRoleSystem:
			msg := schemas.ResponsesMessage{
				Role: Ptr(schemas.ResponsesInputMessageRoleSystem),
				Content: &schemas.ResponsesMessageContent{
					ContentStr: Ptr(post.Message),
				},
			}
			messages = append(messages, msg)

		case llm.PostRoleUser:
			if len(post.Files) > 0 {
				// Multimodal message with images
				parts := b.createResponsesMultimodalContent(post)
				msg := schemas.ResponsesMessage{
					Role: Ptr(schemas.ResponsesInputMessageRoleUser),
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: parts,
					},
				}
				messages = append(messages, msg)
			} else {
				msg := schemas.ResponsesMessage{
					Role: Ptr(schemas.ResponsesInputMessageRoleUser),
					Content: &schemas.ResponsesMessageContent{
						ContentStr: Ptr(post.Message),
					},
				}
				messages = append(messages, msg)
			}

		case llm.PostRoleBot:
			msg := schemas.ResponsesMessage{
				Role: Ptr(schemas.ResponsesInputMessageRoleAssistant),
				Content: &schemas.ResponsesMessageContent{
					ContentStr: Ptr(post.Message),
				},
			}
			messages = append(messages, msg)

			// Handle tool calls in assistant messages
			if len(post.ToolUse) > 0 {
				for _, tc := range post.ToolUse {
					// Add function call message
					funcCallMsg := schemas.ResponsesMessage{
						Type: Ptr(schemas.ResponsesMessageTypeFunctionCall),
						ResponsesToolMessage: &schemas.ResponsesToolMessage{
							CallID:    Ptr(tc.ID),
							Name:      Ptr(tc.Name),
							Arguments: Ptr(string(tc.Arguments)),
						},
					}
					messages = append(messages, funcCallMsg)

					// Add function call output message
					funcOutputMsg := schemas.ResponsesMessage{
						Type: Ptr(schemas.ResponsesMessageTypeFunctionCallOutput),
						ResponsesToolMessage: &schemas.ResponsesToolMessage{
							CallID: Ptr(tc.ID),
							Output: &schemas.ResponsesToolMessageOutputStruct{
								ResponsesToolCallOutputStr: Ptr(tc.Result),
							},
						},
					}
					messages = append(messages, funcOutputMsg)
				}
				continue // Skip adding msg again since we handled tool calls
			}
		}
	}

	return messages
}

// createResponsesMultimodalContent creates content blocks for Responses API messages with images.
func (b *LLM) createResponsesMultimodalContent(post llm.Post) []schemas.ResponsesMessageContentBlock {
	parts := make([]schemas.ResponsesMessageContentBlock, 0, len(post.Files)+1)

	if post.Message != "" {
		parts = append(parts, schemas.ResponsesMessageContentBlock{
			Type: schemas.ResponsesInputMessageContentBlockTypeText,
			Text: Ptr(post.Message),
		})
	}

	for _, file := range post.Files {
		if !isValidImageType(file.MimeType) {
			parts = append(parts, schemas.ResponsesMessageContentBlock{
				Type: schemas.ResponsesInputMessageContentBlockTypeText,
				Text: Ptr(fmt.Sprintf("[Unsupported image type: %s]", file.MimeType)),
			})
			continue
		}

		data, err := io.ReadAll(file.Reader)
		if err != nil {
			parts = append(parts, schemas.ResponsesMessageContentBlock{
				Type: schemas.ResponsesInputMessageContentBlockTypeText,
				Text: Ptr("[Error reading image data]"),
			})
			continue
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		dataURL := fmt.Sprintf("data:%s;base64,%s", file.MimeType, encoded)

		parts = append(parts, schemas.ResponsesMessageContentBlock{
			Type: schemas.ResponsesInputMessageContentBlockTypeImage,
			ResponsesInputMessageContentBlockImage: &schemas.ResponsesInputMessageContentBlockImage{
				ImageURL: Ptr(dataURL),
			},
		})
	}

	return parts
}

// convertToResponsesTools creates Responses API tools including native tools and function tools.
func (b *LLM) convertToResponsesTools(request llm.CompletionRequest, cfg llm.LanguageModelConfig) []schemas.ResponsesTool {
	var result []schemas.ResponsesTool

	// Add native tools
	for _, nativeTool := range b.enabledNativeTools {
		switch nativeTool {
		case "web_search":
			result = append(result, schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeWebSearch,
			})
		case "file_search":
			result = append(result, schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeFileSearch,
			})
		case "code_interpreter":
			result = append(result, schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeCodeInterpreter,
			})
		}
	}

	// Add custom function tools if available
	if !cfg.ToolsDisabled && request.Context != nil && request.Context.Tools != nil {
		tools := request.Context.Tools.GetTools()
		for _, tool := range tools {
			var params *schemas.ToolFunctionParameters
			if tool.Schema != nil {
				switch s := tool.Schema.(type) {
				case map[string]interface{}:
					params = schemaMapToFunctionParams(s)
				default:
					data, err := json.Marshal(tool.Schema)
					if err == nil {
						var schemaMap map[string]interface{}
						if json.Unmarshal(data, &schemaMap) == nil {
							params = schemaMapToFunctionParams(schemaMap)
						}
					}
				}
			}
			if params == nil {
				params = &schemas.ToolFunctionParameters{Type: "object"}
			}
			if params.Type == "" {
				params.Type = "object"
			}

			responsesTool := schemas.ResponsesTool{
				Type:        schemas.ResponsesToolTypeFunction,
				Name:        Ptr(tool.Name),
				Description: Ptr(tool.Description),
				ResponsesToolFunction: &schemas.ResponsesToolFunction{
					Parameters: params,
				},
			}
			result = append(result, responsesTool)
		}
	}

	return result
}

// buildResponsesReasoning creates a ResponsesParametersReasoning configuration if reasoning is enabled.
func (b *LLM) buildResponsesReasoning() *schemas.ResponsesParametersReasoning {
	if !b.reasoningEnabled {
		return nil
	}
	reasoning := &schemas.ResponsesParametersReasoning{}
	effort := b.reasoningEffort
	if effort == "" {
		effort = "medium"
	}
	reasoning.Effort = Ptr(effort)
	// Anthropic requires MaxTokens for thinking
	if b.provider == schemas.Anthropic && b.thinkingBudget > 0 {
		reasoning.MaxTokens = Ptr(b.thinkingBudget)
	}
	return reasoning
}

// convertToBifrostResponsesRequest converts our CompletionRequest to Bifrost's Responses API format.
func (b *LLM) convertToBifrostResponsesRequest(request llm.CompletionRequest, cfg llm.LanguageModelConfig) *schemas.BifrostResponsesRequest {
	messages := b.convertToResponsesMessages(request.Posts)
	tools := b.convertToResponsesTools(request, cfg)

	req := &schemas.BifrostResponsesRequest{
		Provider: b.provider,
		Model:    cfg.Model,
		Input:    messages,
	}

	// Set parameters
	params := &schemas.ResponsesParameters{}
	if cfg.MaxGeneratedTokens > 0 {
		params.MaxOutputTokens = Ptr(cfg.MaxGeneratedTokens)
	}
	if len(tools) > 0 {
		params.Tools = tools
	}
	// Apply reasoning configuration
	if b.reasoningEnabled {
		params.Reasoning = b.buildResponsesReasoning()
	}
	req.Params = params

	return req
}

// streamResponses handles the streaming Responses API completion.
func (b *LLM) streamResponses(request llm.CompletionRequest, cfg llm.LanguageModelConfig, output chan<- llm.TextStreamEvent) {
	bifrostCtx, cancel := schemas.NewBifrostContextWithTimeout(context.Background(), b.streamingTimeout*10)
	defer cancel()

	// Convert to Bifrost Responses API request
	bifrostReq := b.convertToBifrostResponsesRequest(request, cfg)

	// Make streaming request
	streamChan, bifrostErr := b.client.ResponsesStreamRequest(bifrostCtx, bifrostReq)
	if bifrostErr != nil {
		output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: fmt.Errorf("bifrost error: %s", bifrostErr.Error.Message),
		}
		return
	}

	// Process stream
	var toolCalls []llm.ToolCall
	toolCallsBuffer := make(map[string]*responsesToolCallBuffer)

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
			case <-bifrostCtx.Done():
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

		// Process Responses API stream response
		if chunk.BifrostResponsesStreamResponse != nil {
			resp := chunk.BifrostResponsesStreamResponse

			switch resp.Type {
			case schemas.ResponsesStreamResponseTypeOutputTextDelta:
				// Text delta
				if resp.Delta != nil && *resp.Delta != "" {
					output <- llm.TextStreamEvent{
						Type:  llm.EventTypeText,
						Value: *resp.Delta,
					}
				}

			case schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDelta:
				// Tool call arguments delta
				if resp.Item != nil && resp.Item.ResponsesToolMessage != nil {
					tm := resp.Item.ResponsesToolMessage
					callID := ""
					if tm.CallID != nil {
						callID = *tm.CallID
					}
					if callID != "" {
						if toolCallsBuffer[callID] == nil {
							toolCallsBuffer[callID] = &responsesToolCallBuffer{id: callID}
						}
						if tm.Name != nil {
							toolCallsBuffer[callID].name = *tm.Name
						}
						if resp.Delta != nil {
							toolCallsBuffer[callID].arguments.WriteString(*resp.Delta)
						}
					}
				}

			case schemas.ResponsesStreamResponseTypeOutputItemAdded:
				// New output item added - could be function call
				if resp.Item != nil && resp.Item.Type != nil {
					if *resp.Item.Type == schemas.ResponsesMessageTypeFunctionCall && resp.Item.ResponsesToolMessage != nil {
						tm := resp.Item.ResponsesToolMessage
						callID := ""
						if tm.CallID != nil {
							callID = *tm.CallID
						}
						if callID != "" {
							if toolCallsBuffer[callID] == nil {
								toolCallsBuffer[callID] = &responsesToolCallBuffer{id: callID}
							}
							if tm.Name != nil {
								toolCallsBuffer[callID].name = *tm.Name
							}
							if tm.Arguments != nil {
								toolCallsBuffer[callID].arguments.WriteString(*tm.Arguments)
							}
						}
					}
				}

			case schemas.ResponsesStreamResponseTypeOutputItemDone:
				// Output item completed - finalize function call if any
				if resp.Item != nil && resp.Item.Type != nil {
					if *resp.Item.Type == schemas.ResponsesMessageTypeFunctionCall && resp.Item.ResponsesToolMessage != nil {
						tm := resp.Item.ResponsesToolMessage
						callID := ""
						if tm.CallID != nil {
							callID = *tm.CallID
						}
						if callID != "" && toolCallsBuffer[callID] != nil {
							buf := toolCallsBuffer[callID]
							// Update with final values if available
							if tm.Name != nil && *tm.Name != "" {
								buf.name = *tm.Name
							}
							if tm.Arguments != nil && *tm.Arguments != "" {
								buf.arguments.Reset()
								buf.arguments.WriteString(*tm.Arguments)
							}
						}
					}
				}

			case schemas.ResponsesStreamResponseTypeCompleted:
				// Response completed - emit tool calls if any
				if len(toolCallsBuffer) > 0 {
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

				// Handle usage data from completed response
				if resp.Response != nil && resp.Response.Usage != nil {
					usage := llm.TokenUsage{
						InputTokens:  int64(resp.Response.Usage.InputTokens),
						OutputTokens: int64(resp.Response.Usage.OutputTokens),
					}
					if usage.InputTokens > 0 || usage.OutputTokens > 0 {
						output <- llm.TextStreamEvent{
							Type:  llm.EventTypeUsage,
							Value: usage,
						}
					}
				}
			}
		}

		// Check if this is the final chunk
		if bifrostcore.IsFinalChunk(bifrostCtx) {
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

type responsesToolCallBuffer struct {
	id        string
	name      string
	arguments strings.Builder
}
