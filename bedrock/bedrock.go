// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go/auth/bearer"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

const (
	DefaultMaxTokens       = 8192
	MaxToolResolutionDepth = 10
)

type messageState struct {
	messages []types.Message
	system   []types.SystemContentBlock
	output   chan<- llm.TextStreamEvent
	depth    int
	config   llm.LanguageModelConfig
	tools    []llm.Tool
	resolver func(name string, argsGetter llm.ToolArgumentGetter, context *llm.Context) (string, error)
	context  *llm.Context
}

type Bedrock struct {
	client           *bedrockruntime.Client
	defaultModel     string
	inputTokenLimit  int
	outputTokenLimit int
	region           string
}

func New(llmService llm.ServiceConfig, httpClient *http.Client) (*Bedrock, error) {
	// Prepare config options
	configOpts := []func(*config.LoadOptions) error{
		config.WithRegion(llmService.Region),
		config.WithHTTPClient(httpClient),
	}

	// Configure authentication based on provided credentials
	// Priority: IAM credentials > Bearer token (API Key) > Default credential chain
	var clientOpts []func(*bedrockruntime.Options)

	// Option 1: IAM user credentials (takes precedence)
	if llmService.AWSAccessKeyID != "" && llmService.AWSSecretAccessKey != "" {
		// Use static IAM credentials for standard AWS SigV4 signing
		configOpts = append(configOpts, config.WithCredentialsProvider(
			aws.NewCredentialsCache(
				credentials.NewStaticCredentialsProvider(
					llmService.AWSAccessKeyID,
					llmService.AWSSecretAccessKey,
					"", // No session token for long-term credentials
				),
			),
		))
	} else if llmService.APIKey != "" {
		// Option 2: Bedrock console API key (bearer token)
		// Disable default credentials to force bearer token authentication
		configOpts = append(configOpts, config.WithCredentialsProvider(aws.AnonymousCredentials{}))

		clientOpts = append(clientOpts, func(o *bedrockruntime.Options) {
			// Set credentials to anonymous to prevent any AWS credential provider from being used
			o.Credentials = aws.AnonymousCredentials{}

			// Use bearer token authentication (base64 encoded format from Bedrock console)
			o.BearerAuthTokenProvider = bearer.TokenProviderFunc(func(ctx context.Context) (bearer.Token, error) {
				return bearer.Token{Value: llmService.APIKey}, nil
			})

			// Force bearer auth to be the only auth scheme
			o.AuthSchemePreference = []string{"httpBearerAuth"}
		})
	}
	// Option 3: If no credentials provided, AWS SDK will use default credential chain
	// (environment variables AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY, IAM role, etc.)

	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(context.Background(), configOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// If APIURL is provided, use it as a custom base endpoint (for proxies, VPC endpoints, etc.)
	if llmService.APIURL != "" {
		clientOpts = append(clientOpts, func(o *bedrockruntime.Options) {
			o.BaseEndpoint = aws.String(llmService.APIURL)
		})
	}

	client := bedrockruntime.NewFromConfig(cfg, clientOpts...)

	return &Bedrock{
		client:           client,
		defaultModel:     llmService.DefaultModel,
		inputTokenLimit:  llmService.InputTokenLimit,
		outputTokenLimit: llmService.OutputTokenLimit,
		region:           llmService.Region,
	}, nil
}

// isValidImageType checks if the MIME type is supported by the Bedrock API
func isValidImageType(mimeType string) bool {
	validTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/gif":  true,
		"image/webp": true,
	}
	return validTypes[mimeType]
}

// conversationToMessages creates a system prompt and a slice of messages from conversation posts.
func conversationToMessages(posts []llm.Post) ([]types.SystemContentBlock, []types.Message) {
	var systemBlocks []types.SystemContentBlock
	messages := make([]types.Message, 0, len(posts))

	var currentBlocks []types.ContentBlock
	var currentRole types.ConversationRole

	flushCurrentMessage := func() {
		if len(currentBlocks) > 0 {
			messages = append(messages, types.Message{
				Role:    currentRole,
				Content: currentBlocks,
			})
			currentBlocks = nil
		}
	}

	for _, post := range posts {
		switch post.Role {
		case llm.PostRoleSystem:
			// System messages go in a separate array
			systemBlocks = append(systemBlocks, &types.SystemContentBlockMemberText{
				Value: post.Message,
			})
			continue
		case llm.PostRoleBot:
			if currentRole != types.ConversationRoleAssistant {
				flushCurrentMessage()
				currentRole = types.ConversationRoleAssistant
			}
		case llm.PostRoleUser:
			if currentRole != types.ConversationRoleUser {
				flushCurrentMessage()
				currentRole = types.ConversationRoleUser
			}
		default:
			continue
		}

		if post.Message != "" {
			currentBlocks = append(currentBlocks, &types.ContentBlockMemberText{
				Value: post.Message,
			})
		}

		for _, file := range post.Files {
			if !isValidImageType(file.MimeType) {
				currentBlocks = append(currentBlocks, &types.ContentBlockMemberText{
					Value: fmt.Sprintf("[Unsupported image type: %s]", file.MimeType),
				})
				continue
			}

			data, err := io.ReadAll(file.Reader)
			if err != nil {
				currentBlocks = append(currentBlocks, &types.ContentBlockMemberText{
					Value: "[Error reading image data]",
				})
				continue
			}

			// Determine format string from MIME type
			var format types.ImageFormat
			switch file.MimeType {
			case "image/jpeg":
				format = types.ImageFormatJpeg
			case "image/png":
				format = types.ImageFormatPng
			case "image/gif":
				format = types.ImageFormatGif
			case "image/webp":
				format = types.ImageFormatWebp
			}

			imageBlock := &types.ContentBlockMemberImage{
				Value: types.ImageBlock{
					Format: format,
					Source: &types.ImageSourceMemberBytes{
						Value: data,
					},
				},
			}
			currentBlocks = append(currentBlocks, imageBlock)
		}

		if len(post.ToolUse) > 0 {
			for _, tool := range post.ToolUse {
				// Convert tool arguments to document
				var inputDoc map[string]interface{}
				if err := json.Unmarshal(tool.Arguments, &inputDoc); err != nil {
					// If we can't unmarshal, create an empty document
					inputDoc = make(map[string]interface{})
				}

				toolBlock := &types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String(tool.ID),
						Name:      aws.String(tool.Name),
						Input:     document.NewLazyDocument(inputDoc),
					},
				}
				currentBlocks = append(currentBlocks, toolBlock)
			}

			// Flush assistant message with tool use
			flushCurrentMessage()

			// Create tool result blocks for the user message
			resultBlocks := make([]types.ContentBlock, 0, len(post.ToolUse))
			for _, tool := range post.ToolUse {
				isError := tool.Status != llm.ToolCallStatusSuccess
				status := types.ToolResultStatusSuccess
				if isError {
					status = types.ToolResultStatusError
				}

				toolResultBlock := &types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						ToolUseId: aws.String(tool.ID),
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{
								Value: tool.Result,
							},
						},
						Status: status,
					},
				}
				resultBlocks = append(resultBlocks, toolResultBlock)
			}

			if len(resultBlocks) > 0 {
				currentRole = types.ConversationRoleUser
				currentBlocks = resultBlocks
				flushCurrentMessage()
			}
		}
	}

	flushCurrentMessage()
	return systemBlocks, messages
}

func (b *Bedrock) GetDefaultConfig() llm.LanguageModelConfig {
	config := llm.LanguageModelConfig{
		Model: b.defaultModel,
	}
	if b.outputTokenLimit == 0 {
		config.MaxGeneratedTokens = DefaultMaxTokens
	} else {
		config.MaxGeneratedTokens = b.outputTokenLimit
	}
	return config
}

func (b *Bedrock) createConfig(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := b.GetDefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// toolUseData tracks a tool use block with accumulated input
type toolUseData struct {
	id        string
	name      string
	inputJSON strings.Builder
}

// getInputJSON returns the input JSON string, defaulting to "{}" if empty
func (t *toolUseData) getInputJSON() string {
	if s := t.inputJSON.String(); s != "" {
		return s
	}
	return "{}"
}

// extractToolCallsFromBlocks converts tool use blocks into ToolCalls
func extractToolCallsFromBlocks(toolBlocks map[int]*toolUseData) []llm.ToolCall {
	keys := make([]int, 0, len(toolBlocks))
	for k := range toolBlocks {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	toolCalls := make([]llm.ToolCall, 0, len(toolBlocks))
	for _, k := range keys {
		toolBlock := toolBlocks[k]
		toolCalls = append(toolCalls, llm.ToolCall{
			ID:        toolBlock.id,
			Name:      toolBlock.name,
			Arguments: []byte(toolBlock.getInputJSON()),
		})
	}
	return toolCalls
}

// buildBedrockAssistantMessage creates an assistant message from accumulated content
func buildBedrockAssistantMessage(textContent string, toolBlocks map[int]*toolUseData) types.Message {
	content := make([]types.ContentBlock, 0, len(toolBlocks)+1)

	if textContent != "" {
		content = append(content, &types.ContentBlockMemberText{
			Value: textContent,
		})
	}

	// Sort keys for deterministic ordering
	indices := make([]int, 0, len(toolBlocks))
	for idx := range toolBlocks {
		indices = append(indices, idx)
	}
	slices.Sort(indices)

	for _, idx := range indices {
		toolBlock := toolBlocks[idx]
		var inputDoc map[string]interface{}
		if err := json.Unmarshal([]byte(toolBlock.getInputJSON()), &inputDoc); err != nil {
			inputDoc = make(map[string]interface{})
		}

		content = append(content, &types.ContentBlockMemberToolUse{
			Value: types.ToolUseBlock{
				ToolUseId: aws.String(toolBlock.id),
				Name:      aws.String(toolBlock.name),
				Input:     document.NewLazyDocument(inputDoc),
			},
		})
	}

	return types.Message{
		Role:    types.ConversationRoleAssistant,
		Content: content,
	}
}

// buildBedrockToolResultsMessage creates a user message containing tool results
func buildBedrockToolResultsMessage(results []llm.AutoRunResult) types.Message {
	content := make([]types.ContentBlock, 0, len(results))

	for _, result := range results {
		content = append(content, &types.ContentBlockMemberToolResult{
			Value: types.ToolResultBlock{
				ToolUseId: aws.String(result.ToolCallID),
				Content: []types.ToolResultContentBlock{
					&types.ToolResultContentBlockMemberText{
						Value: result.Result,
					},
				},
				Status: toolResultStatus(result.IsError),
			},
		})
	}

	return types.Message{
		Role:    types.ConversationRoleUser,
		Content: content,
	}
}

func toolResultStatus(isError bool) types.ToolResultStatus {
	if isError {
		return types.ToolResultStatusError
	}
	return types.ToolResultStatusSuccess
}

func (b *Bedrock) streamChatWithTools(initialState messageState) {
	state := initialState

	sendError := func(err error) {
		state.output <- llm.TextStreamEvent{Type: llm.EventTypeError, Value: err}
	}

	for {
		if state.depth >= MaxToolResolutionDepth {
			sendError(fmt.Errorf("max tool resolution depth (%d) exceeded", MaxToolResolutionDepth))
			return
		}

		params := &bedrockruntime.ConverseStreamInput{
			ModelId:  aws.String(state.config.Model),
			Messages: state.messages,
		}

		if len(state.system) > 0 {
			params.System = state.system
		}

		maxTokens := state.config.MaxGeneratedTokens
		if maxTokens > 2147483647 { // math.MaxInt32
			sendError(fmt.Errorf("max token value (%d) exceeds int32 maximum", maxTokens))
			return
		}
		params.InferenceConfig = &types.InferenceConfiguration{
			MaxTokens: aws.Int32(int32(maxTokens)), //nolint:gosec // G115: Overflow checked above
		}

		if !state.config.ToolsDisabled && len(state.tools) > 0 {
			params.ToolConfig = &types.ToolConfiguration{
				Tools: convertTools(state.tools),
			}
		}

		stream, err := b.client.ConverseStream(context.Background(), params)
		if err != nil {
			sendError(fmt.Errorf("error starting stream: %w", err))
			return
		}

		eventStream := stream.GetStream()
		currentToolUseBlocks := make(map[int]*toolUseData)
		var stopReason types.StopReason
		var accumulatedText strings.Builder

		for event := range eventStream.Events() {
			switch e := event.(type) {
			case *types.ConverseStreamOutputMemberContentBlockStart:
				if e.Value.Start == nil || e.Value.ContentBlockIndex == nil {
					continue
				}
				start, ok := e.Value.Start.(*types.ContentBlockStartMemberToolUse)
				if !ok {
					continue
				}
				idx := int(*e.Value.ContentBlockIndex)
				currentToolUseBlocks[idx] = &toolUseData{
					id:   aws.ToString(start.Value.ToolUseId),
					name: aws.ToString(start.Value.Name),
				}

			case *types.ConverseStreamOutputMemberContentBlockDelta:
				if e.Value.Delta == nil {
					continue
				}
				switch delta := e.Value.Delta.(type) {
				case *types.ContentBlockDeltaMemberText:
					state.output <- llm.TextStreamEvent{Type: llm.EventTypeText, Value: delta.Value}
					accumulatedText.WriteString(delta.Value)
				case *types.ContentBlockDeltaMemberToolUse:
					if e.Value.ContentBlockIndex == nil || delta.Value.Input == nil {
						continue
					}
					idx := int(*e.Value.ContentBlockIndex)
					if toolBlock, ok := currentToolUseBlocks[idx]; ok {
						toolBlock.inputJSON.WriteString(aws.ToString(delta.Value.Input))
					}
				}

			case *types.ConverseStreamOutputMemberMessageStop:
				if e.Value.StopReason != "" {
					stopReason = e.Value.StopReason
				}

			case *types.ConverseStreamOutputMemberMetadata:
				if e.Value.Usage != nil {
					state.output <- llm.TextStreamEvent{
						Type: llm.EventTypeUsage,
						Value: llm.TokenUsage{
							InputTokens:  int64(aws.ToInt32(e.Value.Usage.InputTokens)),
							OutputTokens: int64(aws.ToInt32(e.Value.Usage.OutputTokens)),
						},
					}
				}
			}
		}

		eventStream.Close()

		if err := eventStream.Err(); err != nil {
			sendError(fmt.Errorf("error from bedrock stream: %w", err))
			return
		}

		if stopReason == types.StopReasonToolUse && len(currentToolUseBlocks) > 0 {
			pendingToolCalls := extractToolCallsFromBlocks(currentToolUseBlocks)

			if llm.ShouldAutoRunTools(pendingToolCalls, state.config.AutoRunTools) {
				state.messages = append(state.messages,
					buildBedrockAssistantMessage(accumulatedText.String(), currentToolUseBlocks))

				toolResults := llm.ExecuteAutoRunTools(
					pendingToolCalls,
					state.resolver,
					state.context,
				)

				state.messages = append(state.messages, buildBedrockToolResultsMessage(toolResults))
				state.depth++
				continue
			}

			state.output <- llm.TextStreamEvent{Type: llm.EventTypeToolCalls, Value: pendingToolCalls}
		}

		state.output <- llm.TextStreamEvent{Type: llm.EventTypeEnd, Value: nil}
		return
	}
}

func (b *Bedrock) ChatCompletion(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	eventStream := make(chan llm.TextStreamEvent)

	cfg := b.createConfig(opts)

	system, messages := conversationToMessages(request.Posts)

	initialState := messageState{
		messages: messages,
		system:   system,
		output:   eventStream,
		depth:    0,
		config:   cfg,
		context:  request.Context,
	}

	if request.Context.Tools != nil {
		initialState.tools = request.Context.Tools.GetTools()
		initialState.resolver = request.Context.Tools.ResolveTool
	}

	go func() {
		defer close(eventStream)
		b.streamChatWithTools(initialState)
	}()

	return &llm.TextStreamResult{Stream: eventStream}, nil
}

func (b *Bedrock) ChatCompletionNoStream(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	// This could perform better if we didn't use the streaming API here, but the complexity is not worth it.
	result, err := b.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (b *Bedrock) CountTokens(text string) int {
	// Bedrock doesn't provide a token counting API
	// Approximate using character and word counts
	charCount := float64(len(text)) / 4.0
	wordCount := float64(len(strings.Fields(text))) / 0.75

	// Average the two
	return int((charCount + wordCount) / 2.0)
}

// convertTools converts from llm.Tool to Bedrock types.Tool format
func convertTools(tools []llm.Tool) []types.Tool {
	converted := make([]types.Tool, 0, len(tools))
	for _, tool := range tools {
		// Marshal the schema to a document
		schemaJSON, err := json.Marshal(tool.Schema)
		if err != nil {
			continue
		}

		var schemaDoc map[string]interface{}
		if err := json.Unmarshal(schemaJSON, &schemaDoc); err != nil {
			continue
		}

		converted = append(converted, &types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        aws.String(tool.Name),
				Description: aws.String(tool.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{
					Value: document.NewLazyDocument(schemaDoc),
				},
			},
		})
	}
	return converted
}

func (b *Bedrock) InputTokenLimit() int {
	if b.inputTokenLimit > 0 {
		return b.inputTokenLimit
	}
	// Return a conservative default. Users should configure inputTokenLimit
	// in the service config for their specific model.
	// See: https://docs.aws.amazon.com/bedrock/latest/userguide/models-supported.html
	return 200000
}
