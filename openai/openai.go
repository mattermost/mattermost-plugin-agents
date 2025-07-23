// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"errors"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/subtitles"
	openaiClient "github.com/sashabaranov/go-openai"
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
	EmbeddingDimentions int           `json:"embeddingDimensions"`
}

type OpenAI struct {
	client *openaiClient.Client
	config Config
}

const (
	MaxFunctionCalls   = 10
	OpenAIMaxImageSize = 20 * 1024 * 1024 // 20 MB
)

var ErrStreamingTimeout = errors.New("timeout streaming")

func NewAzure(config Config, httpClient *http.Client) *OpenAI {
	return newOpenAI(config, httpClient,
		func(apiKey string) openaiClient.ClientConfig {
			clientConfig := openaiClient.DefaultAzureConfig(apiKey, strings.TrimSuffix(config.APIURL, "/"))
			clientConfig.APIVersion = "2024-06-01"
			return clientConfig
		},
	)
}

func NewCompatible(config Config, httpClient *http.Client) *OpenAI {
	return newOpenAI(config, httpClient,
		func(apiKey string) openaiClient.ClientConfig {
			clientConfig := openaiClient.DefaultConfig(apiKey)
			clientConfig.BaseURL = strings.TrimSuffix(config.APIURL, "/")
			return clientConfig
		},
	)
}

func New(config Config, httpClient *http.Client) *OpenAI {
	return newOpenAI(config, httpClient,
		func(apiKey string) openaiClient.ClientConfig {
			clientConfig := openaiClient.DefaultConfig(apiKey)
			clientConfig.OrgID = config.OrgID
			return clientConfig
		},
	)
}

// NewEmbeddings creates a new OpenAI client configured only for embeddings functionality
func NewEmbeddings(config Config, httpClient *http.Client) *OpenAI {
	if config.EmbeddingModel == "" {
		config.EmbeddingModel = string(openaiClient.LargeEmbedding3)
		config.EmbeddingDimentions = 3072
	}
	return newOpenAI(config, httpClient,
		func(apiKey string) openaiClient.ClientConfig {
			clientConfig := openaiClient.DefaultConfig(apiKey)
			return clientConfig
		},
	)
}

// NewCompatibleEmbeddings creates a new OpenAI client configured only for embeddings functionality
func NewCompatibleEmbeddings(config Config, httpClient *http.Client) *OpenAI {
	if config.EmbeddingModel == "" {
		config.EmbeddingModel = string(openaiClient.LargeEmbedding3)
		config.EmbeddingDimentions = 3072
	}

	return newOpenAI(config, httpClient,
		func(apiKey string) openaiClient.ClientConfig {
			clientConfig := openaiClient.DefaultConfig(apiKey)
			clientConfig.BaseURL = strings.TrimSuffix(config.APIURL, "/")
			return clientConfig
		},
	)
}

func newOpenAI(
	config Config,
	httpClient *http.Client,
	baseConfigFunc func(apiKey string) openaiClient.ClientConfig,
) *OpenAI {
	clientConfig := baseConfigFunc(config.APIKey)
	clientConfig.HTTPClient = httpClient

	return &OpenAI{
		client: openaiClient.NewClientWithConfig(clientConfig),
		config: config,
	}
}

func modifyCompletionRequestWithRequest(openAIRequest openaiClient.ChatCompletionRequest, interalRequest llm.CompletionRequest) openaiClient.ChatCompletionRequest {
	openAIRequest.Messages = postsToChatCompletionMessages(interalRequest.Posts)
	if interalRequest.Context.Tools != nil {
		openAIRequest.Tools = toolsToOpenAITools(interalRequest.Context.Tools.GetTools())
	}
	return openAIRequest
}

func toolsToOpenAITools(tools []llm.Tool) []openaiClient.Tool {
	result := make([]openaiClient.Tool, 0, len(tools))
	for _, tool := range tools {
		result = append(result, openaiClient.Tool{
			Type: openaiClient.ToolTypeFunction,
			Function: &openaiClient.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Schema,
			},
		})
	}

	return result
}

func postsToChatCompletionMessages(posts []llm.Post) []openaiClient.ChatCompletionMessage {
	result := make([]openaiClient.ChatCompletionMessage, 0, len(posts))

	for _, post := range posts {
		role := openaiClient.ChatMessageRoleUser
		switch post.Role {
		case llm.PostRoleBot:
			role = openaiClient.ChatMessageRoleAssistant
		case llm.PostRoleSystem:
			role = openaiClient.ChatMessageRoleSystem
		}
		completionMessage := openaiClient.ChatCompletionMessage{
			Role: role,
		}

		if len(post.Files) > 0 {
			completionMessage.MultiContent = make([]openaiClient.ChatMessagePart, 0, len(post.Files)+1)
			if post.Message != "" {
				completionMessage.MultiContent = append(completionMessage.MultiContent, openaiClient.ChatMessagePart{
					Type: openaiClient.ChatMessagePartTypeText,
					Text: post.Message,
				})
			}
			for _, file := range post.Files {
				if file.MimeType != "image/png" &&
					file.MimeType != "image/jpeg" &&
					file.MimeType != "image/gif" &&
					file.MimeType != "image/webp" {
					completionMessage.MultiContent = append(completionMessage.MultiContent, openaiClient.ChatMessagePart{
						Type: openaiClient.ChatMessagePartTypeText,
						Text: "User submitted image was not a supported format. Tell the user this.",
					})
					continue
				}
				if file.Size > OpenAIMaxImageSize {
					completionMessage.MultiContent = append(completionMessage.MultiContent, openaiClient.ChatMessagePart{
						Type: openaiClient.ChatMessagePartTypeText,
						Text: "User submitted a image larger than 20MB. Tell the user this.",
					})
					continue
				}
				fileBytes, err := io.ReadAll(file.Reader)
				if err != nil {
					continue
				}
				imageEncoded := base64.StdEncoding.EncodeToString(fileBytes)
				encodedString := fmt.Sprintf("data:"+file.MimeType+";base64,%s", imageEncoded)
				completionMessage.MultiContent = append(completionMessage.MultiContent, openaiClient.ChatMessagePart{
					Type: openaiClient.ChatMessagePartTypeImageURL,
					ImageURL: &openaiClient.ChatMessageImageURL{
						URL:    encodedString,
						Detail: openaiClient.ImageURLDetailAuto,
					},
				})
			}
		} else {
			completionMessage.Content = post.Message
		}

		// Add the original tool calls back to the message
		if len(post.ToolUse) > 0 {
			completionMessage.ToolCalls = make([]openaiClient.ToolCall, 0, len(post.ToolUse))
			for _, tool := range post.ToolUse {
				completionMessage.ToolCalls = append(completionMessage.ToolCalls, openaiClient.ToolCall{
					ID:   tool.ID,
					Type: openaiClient.ToolTypeFunction,
					Function: openaiClient.FunctionCall{
						Name:      tool.Name,
						Arguments: string(tool.Arguments),
					},
				})
			}
		}

		result = append(result, completionMessage)

		// Add the results of the tool calls in additional messages
		if len(post.ToolUse) > 0 {
			for _, tool := range post.ToolUse {
				result = append(result, openaiClient.ChatCompletionMessage{
					Role:       openaiClient.ChatMessageRoleTool,
					ToolCallID: tool.ID,
					Content:    tool.Result,
				})
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

func (s *OpenAI) streamResultToChannels(request openaiClient.ChatCompletionRequest, llmContext *llm.Context, output chan<- llm.TextStreamEvent) {
	request.Stream = true

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

	stream, err := s.client.CreateChatCompletionStream(ctx, request)
	if err != nil {
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
		return
	}

	defer stream.Close()

	// Buffering in the case of tool use
	var toolsBuffer map[int]*ToolBufferElement
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeEnd,
				Value: nil,
			}
			return
		}
		if err != nil {
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
			return
		}

		// Ping the watchdog when we receive a response
		watchdog <- struct{}{}

		if len(response.Choices) == 0 {
			continue
		}

		delta := response.Choices[0].Delta
		numTools := len(delta.ToolCalls)

		if numTools != 0 {
			if toolsBuffer == nil {
				toolsBuffer = make(map[int]*ToolBufferElement)
			}
			for _, toolCall := range delta.ToolCalls {
				if toolCall.Index == nil {
					continue
				}
				toolIndex := *toolCall.Index
				if toolsBuffer[toolIndex] == nil {
					toolsBuffer[toolIndex] = &ToolBufferElement{}
				}

				toolsBuffer[toolIndex].name.WriteString(toolCall.Function.Name)
				toolsBuffer[toolIndex].args.WriteString(toolCall.Function.Arguments)
				toolsBuffer[toolIndex].id.WriteString(toolCall.ID)
			}
		}

		// Check finishing conditions
		switch response.Choices[0].FinishReason {
		case "":
			// Not done yet, keep going
		case openaiClient.FinishReasonStop:
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeEnd,
				Value: nil,
			}
			return
		case openaiClient.FinishReasonToolCalls:
			// Verify OpenAI functions are not recursing too deep.
			numFunctionCalls := 0
			for i := len(request.Messages) - 1; i >= 0; i-- {
				if request.Messages[i].Role == openaiClient.ChatMessageRoleTool {
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
			tools := []openaiClient.ToolCall{}
			for i, tool := range toolsBuffer {
				name := tool.name.String()
				arguments := tool.args.String()
				toolID := tool.id.String()
				num := i
				tools = append(tools, openaiClient.ToolCall{
					Function: openaiClient.FunctionCall{
						Name:      name,
						Arguments: arguments,
					},
					ID:    toolID,
					Index: &num,
					Type:  openaiClient.ToolTypeFunction,
				})
			}

			// Send tool calls event and end the stream
			pendingToolCalls := make([]llm.ToolCall, 0, len(tools))
			for _, tool := range tools {
				pendingToolCalls = append(pendingToolCalls, llm.ToolCall{
					ID:          tool.ID,
					Name:        tool.Function.Name,
					Description: "", // OpenAI doesn't provide description in the response
					Arguments:   []byte(tool.Function.Arguments),
				})
			}

			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeToolCalls,
				Value: pendingToolCalls,
			}
			return
		default:
			fmt.Printf("Unknown finish reason: %s", response.Choices[0].FinishReason)
			return
		}

		if response.Choices[0].Delta.Content != "" {
			output <- llm.TextStreamEvent{
				Type:  llm.EventTypeText,
				Value: response.Choices[0].Delta.Content,
			}
		}
	}
}

func (s *OpenAI) streamResult(request openaiClient.ChatCompletionRequest, llmContext *llm.Context) (*llm.TextStreamResult, error) {
	eventStream := make(chan llm.TextStreamEvent)
	go func() {
		defer close(eventStream)
		s.streamResultToChannels(request, llmContext, eventStream)
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

func (s *OpenAI) completionRequestFromConfig(cfg llm.LanguageModelConfig) openaiClient.ChatCompletionRequest {
	request := openaiClient.ChatCompletionRequest{
		Model: cfg.Model,
	}
	request.MaxTokens = cfg.MaxGeneratedTokens

	if cfg.JSONOutputFormat != nil {
		request.ResponseFormat = &openaiClient.ChatCompletionResponseFormat{
			Type: openaiClient.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openaiClient.ChatCompletionResponseFormatJSONSchema{
				Name:   "output_format",
				Schema: cfg.JSONOutputFormat,
				Strict: true,
			},
		}
	}

	return request
}

func (s *OpenAI) ChatCompletion(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	openAIRequest := s.completionRequestFromConfig(s.createConfig(opts))
	openAIRequest = modifyCompletionRequestWithRequest(openAIRequest, request)
	openAIRequest.Stream = true
	if s.config.SendUserID {
		if request.Context.RequestingUser != nil {
			openAIRequest.User = request.Context.RequestingUser.Id
		}
	}
	return s.streamResult(openAIRequest, request.Context)
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
	resp, err := s.client.CreateTranscription(context.Background(), openaiClient.AudioRequest{
		Model:    openaiClient.Whisper1,
		Reader:   file,
		FilePath: "input.mp3",
		Format:   openaiClient.AudioResponseFormatVTT,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create whisper transcription: %w", err)
	}

	timedTranscript, err := subtitles.NewSubtitlesFromVTT(strings.NewReader(resp.Text))
	if err != nil {
		return nil, fmt.Errorf("unable to parse whisper transcription: %w", err)
	}

	return timedTranscript, nil
}

func (s *OpenAI) GenerateImage(prompt string) (image.Image, error) {
	req := openaiClient.ImageRequest{
		Prompt:         prompt,
		Size:           openaiClient.CreateImageSize256x256,
		ResponseFormat: openaiClient.CreateImageResponseFormatB64JSON,
		N:              1,
	}

	respBase64, err := s.client.CreateImage(context.Background(), req)
	if err != nil {
		return nil, err
	}

	imgBytes, err := base64.StdEncoding.DecodeString(respBase64.Data[0].B64JSON)
	if err != nil {
		return nil, err
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
	case strings.HasPrefix(s.config.DefaultModel, "gpt-3.5-turbo"),
		s.config.DefaultModel == "gpt-3.5-turbo-0125",
		s.config.DefaultModel == "gpt-3.5-turbo-1106":
		return 16385
	case s.config.DefaultModel == "gpt-3.5-turbo-instruct":
		return 4096
	}

	return 128000 // Default fallback
}

func (s *OpenAI) CreateEmbedding(ctx context.Context, text string) ([]float32, error) {
	resp, err := s.client.CreateEmbeddings(ctx, openaiClient.EmbeddingRequest{
		Input:      []string{text},
		Model:      openaiClient.EmbeddingModel(s.config.EmbeddingModel),
		Dimensions: s.config.EmbeddingDimentions,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	return resp.Data[0].Embedding, nil
}

// BatchCreateEmbeddings generates embeddings for multiple texts in a single API call
func (s *OpenAI) BatchCreateEmbeddings(ctx context.Context, texts []string) ([][]float32, error) {
	resp, err := s.client.CreateEmbeddings(ctx, openaiClient.EmbeddingRequest{
		Input:      texts,
		Model:      openaiClient.EmbeddingModel(s.config.EmbeddingModel),
		Dimensions: s.config.EmbeddingDimentions,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create embeddings batch: %w", err)
	}

	embeddings := make([][]float32, len(resp.Data))
	for i, data := range resp.Data {
		embeddings[i] = data.Embedding
	}

	return embeddings, nil
}

func (s *OpenAI) Dimensions() int {
	return s.config.EmbeddingDimentions
}
