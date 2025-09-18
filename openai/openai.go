// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/subtitles"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/azure"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/shared"
)

type Config struct {
	APIKey              string        `json:"apiKey"`
	APIURL              string        `json:"apiURL"`
	OrgID               string        `json:"orgID"`
	DefaultModel        string        `json:"defaultModel"`
	InputTokenLimit     int           `json:"inputTokenLimit"`
	OutputTokenLimit    int           `json:"outputTokenLimit"`
	StreamingTimeout    time.Duration `json:"streamingTimeout"`
	SendUserID          bool          `json:"sendUserID"`
	EmbeddingModel      string        `json:"embeddingModel"`
	EmbeddingDimensions int           `json:"embeddingDimensions"`
}

type OpenAI struct {
	client openai.Client
	config Config
}

const (
	MaxFunctionCalls   = 10
	OpenAIMaxImageSize = 20 * 1024 * 1024 // 20 MB
)

var ErrStreamingTimeout = errors.New("timeout streaming")

func NewAzure(config Config, httpClient *http.Client) *OpenAI {
	opts := []option.RequestOption{
		azure.WithEndpoint(strings.TrimSuffix(config.APIURL, "/"), "2024-06-01"),
		azure.WithAPIKey(config.APIKey),
		option.WithHTTPClient(httpClient),
	}

	client := openai.NewClient(opts...)

	return &OpenAI{
		client: client,
		config: config,
	}
}

func NewCompatible(config Config, httpClient *http.Client) *OpenAI {
	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithHTTPClient(httpClient),
		option.WithBaseURL(strings.TrimSuffix(config.APIURL, "/")),
	}

	client := openai.NewClient(opts...)

	return &OpenAI{
		client: client,
		config: config,
	}
}

func New(config Config, httpClient *http.Client) *OpenAI {
	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithHTTPClient(httpClient),
	}

	if config.OrgID != "" {
		opts = append(opts, option.WithOrganization(config.OrgID))
	}

	client := openai.NewClient(opts...)

	return &OpenAI{
		client: client,
		config: config,
	}
}

// NewEmbeddings creates a new OpenAI client configured only for embeddings functionality
func NewEmbeddings(config Config, httpClient *http.Client) *OpenAI {
	if config.EmbeddingModel == "" {
		config.EmbeddingModel = openai.EmbeddingModelTextEmbedding3Large
		config.EmbeddingDimensions = 3072
	}

	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithHTTPClient(httpClient),
	}

	client := openai.NewClient(opts...)

	return &OpenAI{
		client: client,
		config: config,
	}
}

// NewCompatibleEmbeddings creates a new OpenAI client configured only for embeddings functionality
func NewCompatibleEmbeddings(config Config, httpClient *http.Client) *OpenAI {
	if config.EmbeddingModel == "" {
		config.EmbeddingModel = openai.EmbeddingModelTextEmbedding3Large
		config.EmbeddingDimensions = 3072
	}

	opts := []option.RequestOption{
		option.WithAPIKey(config.APIKey),
		option.WithHTTPClient(httpClient),
		option.WithBaseURL(strings.TrimSuffix(config.APIURL, "/")),
	}

	client := openai.NewClient(opts...)

	return &OpenAI{
		client: client,
		config: config,
	}
}

func modifyCompletionRequestWithRequest(params openai.ChatCompletionNewParams, internalRequest llm.CompletionRequest) openai.ChatCompletionNewParams {
	params.Messages = postsToChatCompletionMessages(internalRequest.Posts)
	if internalRequest.Context.Tools != nil {
		params.Tools = toolsToOpenAITools(internalRequest.Context.Tools.GetTools())
	}
	return params
}

// schemaToFunctionParameters converts a jsonschema.Schema to shared.FunctionParameters
func schemaToFunctionParameters(schema *jsonschema.Schema) shared.FunctionParameters {
	// Default schema that satisfies OpenAI's requirements
	defaultSchema := shared.FunctionParameters{
		"type":       "object",
		"properties": map[string]any{},
	}

	// Convert the schema to a map by marshaling and unmarshaling
	data, err := json.Marshal(schema)
	if err != nil {
		return defaultSchema
	}

	var result shared.FunctionParameters
	if err := json.Unmarshal(data, &result); err != nil {
		return defaultSchema
	}

	// Ensure the result has the required fields for OpenAI
	// OpenAI requires "type" and "properties" to be present, even if properties is empty
	// This is because OpenAI's FunctionDefinitionParam has `omitzero` on the `Parameters` field
	if result == nil {
		return defaultSchema
	}
	if _, hasType := result["type"]; !hasType {
		result["type"] = "object"
	}
	if _, hasProps := result["properties"]; !hasProps {
		result["properties"] = map[string]any{}
	}

	return result
}

func toolsToOpenAITools(tools []llm.Tool) []openai.ChatCompletionToolUnionParam {
	result := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		result = append(result, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        tool.Name,
			Description: openai.String(tool.Description),
			Parameters:  schemaToFunctionParameters(tool.Schema),
		}))
	}

	return result
}

func postsToChatCompletionMessages(posts []llm.Post) []openai.ChatCompletionMessageParamUnion {
	result := make([]openai.ChatCompletionMessageParamUnion, 0, len(posts))

	for _, post := range posts {
		switch post.Role {
		case llm.PostRoleSystem:
			result = append(result, openai.SystemMessage(post.Message))
		case llm.PostRoleBot:
			// Assistant message - if it has tool calls, we need to construct it differently
			if len(post.ToolUse) > 0 {
				// For messages with tool calls, we need to build it manually
				toolCalls := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(post.ToolUse))
				for _, tool := range post.ToolUse {
					// Create function tool call
					toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: tool.ID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tool.Name,
								Arguments: string(tool.Arguments),
							},
						},
					})
				}

				// Create assistant message with tool calls
				msgParam := openai.ChatCompletionAssistantMessageParam{}

				// Only set content if it's not empty
				if post.Message != "" {
					msgParam.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: openai.String(post.Message),
					}
				}

				msgParam.ToolCalls = toolCalls

				result = append(result, openai.ChatCompletionMessageParamUnion{
					OfAssistant: &msgParam,
				})

				// Add tool results as separate messages
				for _, tool := range post.ToolUse {
					result = append(result, openai.ToolMessage(tool.Result, tool.ID))
				}
			} else {
				// Simple assistant message
				result = append(result, openai.AssistantMessage(post.Message))
			}
		case llm.PostRoleUser:
			// User message
			if len(post.Files) > 0 {
				// Create multipart content for images
				parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(post.Files)+1)

				if post.Message != "" {
					parts = append(parts, openai.TextContentPart(post.Message))
				}

				for _, file := range post.Files {
					if file.MimeType != "image/png" &&
						file.MimeType != "image/jpeg" &&
						file.MimeType != "image/gif" &&
						file.MimeType != "image/webp" {
						parts = append(parts, openai.TextContentPart("User submitted image was not a supported format. Tell the user this."))
						continue
					}
					if file.Size > OpenAIMaxImageSize {
						parts = append(parts, openai.TextContentPart("User submitted an image larger than 20MB. Tell the user this."))
						continue
					}
					fileBytes, err := io.ReadAll(file.Reader)
					if err != nil {
						continue
					}
					imageEncoded := base64.StdEncoding.EncodeToString(fileBytes)
					encodedString := fmt.Sprintf("data:"+file.MimeType+";base64,%s", imageEncoded)
					parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
						URL:    encodedString,
						Detail: "auto",
					}))
				}

				// Create a user message with multipart content
				result = append(result, openai.UserMessage(parts))
			} else {
				result = append(result, openai.UserMessage(post.Message))
			}
		}
	}

	return result
}

type ToolBufferElement struct {
	id   strings.Builder
	name strings.Builder
	args strings.Builder
}

func (s *OpenAI) streamResultToChannels(params openai.ChatCompletionNewParams, llmContext *llm.Context, output chan<- llm.TextStreamEvent) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	// watchdog to cancel if the streaming stalls
	watchdog := make(chan struct{})
	go func() {
		timer := time.NewTimer(s.config.StreamingTimeout)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				cancel(ErrStreamingTimeout)
				return
			case <-ctx.Done():
				return
			case <-watchdog:
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(s.config.StreamingTimeout)
			}
		}
	}()

	stream := s.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	// Buffering in the case of tool use
	var toolsBuffer map[int]*ToolBufferElement
	for stream.Next() {
		chunk := stream.Current()

		// Ping the watchdog when we receive a response
		watchdog <- struct{}{}

		// Check for usage data and emit usage event if available
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage := llm.TokenUsage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			}
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeUsage,
				Value: usage,
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		delta := choice.Delta

		// Handle tool calls
		if len(delta.ToolCalls) > 0 {
			if toolsBuffer == nil {
				toolsBuffer = make(map[int]*ToolBufferElement)
			}
			for _, toolCall := range delta.ToolCalls {
				toolIndex := int(toolCall.Index)
				if toolsBuffer[toolIndex] == nil {
					toolsBuffer[toolIndex] = &ToolBufferElement{}
				}

				if toolCall.ID != "" {
					toolsBuffer[toolIndex].id.WriteString(toolCall.ID)
				}
				if toolCall.Function.Name != "" {
					toolsBuffer[toolIndex].name.WriteString(toolCall.Function.Name)
				}
				if toolCall.Function.Arguments != "" {
					toolsBuffer[toolIndex].args.WriteString(toolCall.Function.Arguments)
				}
			}
		}

		if delta.Content != "" {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeText,
				Value: delta.Content,
			}
		}

		// Check finishing conditions
		switch choice.FinishReason {
		case "stop":
			// Continue processing to get usage data, but don't send more text
			// The EventTypeEnd will be sent when we run out of chunks
			continue
		case "tool_calls":
			// Verify OpenAI functions are not recursing too deep.
			numFunctionCalls := 0
			for i := len(params.Messages) - 1; i >= 0; i-- {
				// Check if it's a tool message
				if params.Messages[i].OfTool != nil {
					numFunctionCalls++
				} else {
					break
				}
			}
			if numFunctionCalls > MaxFunctionCalls {
				output <- llm.TextStreamEvent{
					Type:  llm.EventTypeError,
					Value: errors.New("too many function calls"),
				}
				return
			}

			// Transfer the buffered tools into tool calls
			pendingToolCalls := make([]llm.ToolCall, 0, len(toolsBuffer))
			for _, tool := range toolsBuffer {
				pendingToolCalls = append(pendingToolCalls, llm.ToolCall{
					ID:          tool.id.String(),
					Name:        tool.name.String(),
					Description: "", // OpenAI doesn't provide description in the response
					Arguments:   []byte(tool.args.String()),
				})
			}

			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeToolCalls,
				Value: pendingToolCalls,
			}
			return
		case "":
			// Not done yet, keep going
		default:
			fmt.Printf("Unknown finish reason: %s", choice.FinishReason)
			return
		}
	}

	if err := stream.Err(); err != nil {
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: ctxErr,
			}
		} else {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: err,
			}
		}
	}

	output <- llm.TextStreamEvent{
		Type:  llm.EventTypeEnd,
		Value: nil,
	}
}

func (s *OpenAI) streamResult(params openai.ChatCompletionNewParams, llmContext *llm.Context) (*llm.TextStreamResult, error) {
	eventStream := make(chan llm.TextStreamEvent)
	go func() {
		defer close(eventStream)
		s.streamResultToChannels(params, llmContext, eventStream)
	}()

	return &llm.TextStreamResult{Stream: eventStream}, nil
}

func (s *OpenAI) GetDefaultConfig() llm.LanguageModelConfig {
	return llm.LanguageModelConfig{
		Model:              s.config.DefaultModel,
		MaxGeneratedTokens: s.config.OutputTokenLimit,
	}
}

func (s *OpenAI) createConfig(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := s.GetDefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

func (s *OpenAI) completionRequestFromConfig(cfg llm.LanguageModelConfig) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model: getModelConstant(cfg.Model),
	}

	if cfg.MaxGeneratedTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(cfg.MaxGeneratedTokens))
	}

	if cfg.JSONOutputFormat != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "output_format",
					Schema: cfg.JSONOutputFormat,
					Strict: openai.Bool(true),
				},
			},
		}
	}

	return params
}

// getModelConstant converts string model names to the SDK's model constants
func getModelConstant(model string) shared.ChatModel {
	// Try to match common model names to constants
	switch model {
	case "gpt-4o":
		return shared.ChatModelGPT4o
	case "gpt-4o-mini":
		return shared.ChatModelGPT4oMini
	case "gpt-4-turbo":
		return shared.ChatModelGPT4Turbo
	case "gpt-4":
		return shared.ChatModelGPT4
	case "gpt-3.5-turbo":
		return shared.ChatModelGPT3_5Turbo
	case "o1-preview":
		return shared.ChatModelO1Preview
	case "o1-mini":
		return shared.ChatModelO1Mini
	default:
		// For custom models or newer versions, use the string as-is
		return model
	}
}

func (s *OpenAI) ChatCompletion(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	params := s.completionRequestFromConfig(s.createConfig(opts))
	params = modifyCompletionRequestWithRequest(params, request)
	params.StreamOptions.IncludeUsage = openai.Bool(true)

	if s.config.SendUserID {
		if request.Context.RequestingUser != nil {
			params.User = openai.String(request.Context.RequestingUser.Id)
		}
	}
	return s.streamResult(params, request.Context)
}

func (s *OpenAI) ChatCompletionNoStream(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	// This could perform better if we didn't use the streaming API here, but the complexity is not worth it.
	result, err := s.ChatCompletion(request, opts...)
	if err != nil {
		return "", err
	}
	return result.ReadAll()
}

func (s *OpenAI) Transcribe(file io.Reader) (*subtitles.Subtitles, error) {
	params := openai.AudioTranscriptionNewParams{
		Model:          openai.AudioModelWhisper1,
		File:           file,
		ResponseFormat: openai.AudioResponseFormatVTT,
	}

	resp, err := s.client.Audio.Transcriptions.New(context.Background(), params)
	if err != nil {
		return nil, fmt.Errorf("unable to create whisper transcription: %w", err)
	}

	// The response for VTT format is the Text field
	timedTranscript, err := subtitles.NewSubtitlesFromVTT(strings.NewReader(resp.Text))
	if err != nil {
		return nil, fmt.Errorf("unable to parse whisper transcription: %w", err)
	}

	return timedTranscript, nil
}

func (s *OpenAI) GenerateImage(prompt string) (image.Image, error) {
	params := openai.ImageGenerateParams{
		Prompt:         prompt,
		Size:           openai.ImageGenerateParamsSize256x256,
		ResponseFormat: openai.ImageGenerateParamsResponseFormatB64JSON,
		N:              openai.Int(1),
	}

	resp, err := s.client.Images.Generate(context.Background(), params)
	if err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, errors.New("no image data returned")
	}

	var imgBytes []byte
	if resp.Data[0].B64JSON != "" {
		imgBytes, err = base64.StdEncoding.DecodeString(resp.Data[0].B64JSON)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("no base64 image data")
	}

	r := bytes.NewReader(imgBytes)
	imgData, err := png.Decode(r)
	if err != nil {
		return nil, err
	}

	return imgData, nil
}

func (s *OpenAI) CountTokens(text string) int {
	// Counting tokens is really annoying, so we approximate for now.
	charCount := float64(len(text)) / 4.0
	wordCount := float64(len(strings.Fields(text))) / 0.75

	// Average the two
	return int((charCount + wordCount) / 2.0)
}

func (s *OpenAI) InputTokenLimit() int {
	if s.config.InputTokenLimit > 0 {
		return s.config.InputTokenLimit
	}

	switch {
	case strings.HasPrefix(s.config.DefaultModel, "gpt-4o"),
		strings.HasPrefix(s.config.DefaultModel, "o1-preview"),
		strings.HasPrefix(s.config.DefaultModel, "o1-mini"),
		strings.HasPrefix(s.config.DefaultModel, "gpt-4-turbo"),
		strings.HasPrefix(s.config.DefaultModel, "gpt-4-0125-preview"),
		strings.HasPrefix(s.config.DefaultModel, "gpt-4-1106-preview"):
		return 128000
	case strings.HasPrefix(s.config.DefaultModel, "gpt-4"):
		return 8192
	case s.config.DefaultModel == "gpt-3.5-turbo-instruct":
		return 4096
	case strings.HasPrefix(s.config.DefaultModel, "gpt-3.5-turbo"),
		s.config.DefaultModel == "gpt-3.5-turbo-0125",
		s.config.DefaultModel == "gpt-3.5-turbo-1106":
		return 16385
	}

	return 128000 // Default fallback
}

func (s *OpenAI) CreateEmbedding(ctx context.Context, text string) ([]float32, error) {
	params := openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfString: openai.String(text),
		},
		Model: getEmbeddingModelConstant(s.config.EmbeddingModel),
	}

	// Only set dimensions if it's explicitly configured (> 0)
	if s.config.EmbeddingDimensions > 0 {
		params.Dimensions = openai.Int(int64(s.config.EmbeddingDimensions))
	}

	resp, err := s.client.Embeddings.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	// Convert float64 to float32
	embedding := make([]float32, len(resp.Data[0].Embedding))
	for i, v := range resp.Data[0].Embedding {
		embedding[i] = float32(v)
	}
	return embedding, nil
}

// BatchCreateEmbeddings generates embeddings for multiple texts in a single API call
func (s *OpenAI) BatchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	params := openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: texts,
		},
		Model: getEmbeddingModelConstant(s.config.EmbeddingModel),
	}

	// Only set dimensions if it's explicitly configured (> 0)
	if s.config.EmbeddingDimensions > 0 {
		params.Dimensions = openai.Int(int64(s.config.EmbeddingDimensions))
	}

	resp, err := s.client.Embeddings.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create embeddings batch: %w", err)
	}

	embeddings := make([][]float32, len(resp.Data))
	for i, data := range resp.Data {
		// Convert float64 to float32
		embedding := make([]float32, len(data.Embedding))
		for j, v := range data.Embedding {
			embedding[j] = float32(v)
		}
		embeddings[i] = embedding
	}

	return embeddings, nil
}

// getEmbeddingModelConstant converts string model names to the SDK's embedding model constants
func getEmbeddingModelConstant(model string) openai.EmbeddingModel {
	switch model {
	case "text-embedding-3-large":
		return openai.EmbeddingModelTextEmbedding3Large
	case "text-embedding-3-small":
		return openai.EmbeddingModelTextEmbedding3Small
	case "text-embedding-ada-002":
		return openai.EmbeddingModelTextEmbeddingAda002
	default:
		// For custom models, use the string as-is
		return model
	}
}

func (s *OpenAI) Dimensions() int {
	return s.config.EmbeddingDimensions
}
