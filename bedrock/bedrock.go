// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

func (b *Bedrock) streamChatWithTools(state messageState) {
	if state.depth >= MaxToolResolutionDepth {
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: fmt.Errorf("max tool resolution depth (%d) exceeded", MaxToolResolutionDepth),
		}
		return
	}

	// Set up parameters for the Bedrock API
	params := &bedrockruntime.ConverseStreamInput{
		ModelId:  aws.String(state.config.Model),
		Messages: state.messages,
	}

	// Only include system messages if non-empty
	if len(state.system) > 0 {
		params.System = state.system
	}

	// Add inference configuration, check for overflow to avoid int -> int32 conversion issues
	maxTokens := state.config.MaxGeneratedTokens
	if maxTokens > 2147483647 { // math.MaxInt32
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: fmt.Errorf("max token value (%d) exceeds int32 maximum", maxTokens),
		}
		return
	}
	params.InferenceConfig = &types.InferenceConfiguration{
		MaxTokens: aws.Int32(int32(maxTokens)), //nolint:gosec // G115: Overflow checked above
	}

	// Add tools if present
	if len(state.tools) > 0 {
		params.ToolConfig = &types.ToolConfiguration{
			Tools: convertTools(state.tools),
		}
	}

	stream, err := b.client.ConverseStream(context.Background(), params)
	if err != nil {
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: fmt.Errorf("error starting stream: %w", err),
		}
		return
	}

	eventStream := stream.GetStream()
	defer eventStream.Close()

	var pendingToolCalls []llm.ToolCall
	// Track tool use blocks with their accumulated input as JSON strings
	type toolUseData struct {
		id        string
		name      string
		inputJSON strings.Builder
	}
	var currentToolUseBlocks []*toolUseData
	var stopReason types.StopReason

	for {
		event, ok := <-eventStream.Events()
		if !ok {
			break
		}

		switch e := event.(type) {
		case *types.ConverseStreamOutputMemberMessageStart:
			// Message starting - no action needed

		case *types.ConverseStreamOutputMemberContentBlockStart:
			// Content block starting - track if it's a tool use block
			if e.Value.Start != nil {
				if start, ok := e.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
					currentToolUseBlocks = append(currentToolUseBlocks, &toolUseData{
						id:   aws.ToString(start.Value.ToolUseId),
						name: aws.ToString(start.Value.Name),
					})
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockDelta:
			// Handle delta events
			if e.Value.Delta != nil {
				switch delta := e.Value.Delta.(type) {
				case *types.ContentBlockDeltaMemberText:
					state.output <- llm.TextStreamEvent{
						Type:  llm.EventTypeText,
						Value: delta.Value,
					}
				case *types.ContentBlockDeltaMemberToolUse:
					// Accumulate tool use input JSON
					if e.Value.ContentBlockIndex != nil && int(*e.Value.ContentBlockIndex) < len(currentToolUseBlocks) {
						idx := int(*e.Value.ContentBlockIndex)
						if delta.Value.Input != nil {
							currentToolUseBlocks[idx].inputJSON.WriteString(aws.ToString(delta.Value.Input))
						}
					}
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockStop:
			// Content block completed

		case *types.ConverseStreamOutputMemberMessageStop:
			// Message completed
			if e.Value.StopReason != "" {
				stopReason = e.Value.StopReason
			}

		case *types.ConverseStreamOutputMemberMetadata:
			// Extract token usage
			if e.Value.Usage != nil {
				usage := llm.TokenUsage{
					InputTokens:  int64(aws.ToInt32(e.Value.Usage.InputTokens)),
					OutputTokens: int64(aws.ToInt32(e.Value.Usage.OutputTokens)),
				}
				state.output <- llm.TextStreamEvent{
					Type:  llm.EventTypeUsage,
					Value: usage,
				}
			}
		}
	}

	if err := eventStream.Err(); err != nil {
		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeError,
			Value: fmt.Errorf("error from bedrock stream: %w", err),
		}
		return
	}

	// Check for tool usage
	if stopReason == types.StopReasonToolUse && len(currentToolUseBlocks) > 0 {
		for _, toolBlock := range currentToolUseBlocks {
			inputJSON := toolBlock.inputJSON.String()
			if inputJSON == "" {
				inputJSON = "{}"
			}

			pendingToolCalls = append(pendingToolCalls, llm.ToolCall{
				ID:          toolBlock.id,
				Name:        toolBlock.name,
				Description: "",
				Arguments:   []byte(inputJSON),
			})
		}

		state.output <- llm.TextStreamEvent{
			Type:  llm.EventTypeToolCalls,
			Value: pendingToolCalls,
		}
	}

	// Send end event
	state.output <- llm.TextStreamEvent{
		Type:  llm.EventTypeEnd,
		Value: nil,
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
	converted := make([]types.Tool, len(tools))
	for i, tool := range tools {
		// Marshal the schema to a document
		schemaJSON, err := json.Marshal(tool.Schema)
		if err != nil {
			continue
		}

		var schemaDoc map[string]interface{}
		if err := json.Unmarshal(schemaJSON, &schemaDoc); err != nil {
			continue
		}

		converted[i] = &types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        aws.String(tool.Name),
				Description: aws.String(tool.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{
					Value: document.NewLazyDocument(schemaDoc),
				},
			},
		}
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
