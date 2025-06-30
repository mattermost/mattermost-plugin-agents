// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Asage experimental LLM provider.
// Upstream has no support for streaming or tool calling, so some features may be limited.
package asage

import (
	"net/http"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

// customHeadersTransport wraps an http.RoundTripper to add custom headers to every request
type customHeadersTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *customHeadersTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	newReq := req.Clone(req.Context())

	// Add custom headers
	for key, value := range t.headers {
		newReq.Header.Set(key, value)
	}

	return t.base.RoundTrip(newReq)
}

// wrapHTTPClientWithCustomHeaders wraps an http.Client to add custom headers to all requests
func wrapHTTPClientWithCustomHeaders(baseClient *http.Client, customHeaders map[string]string) *http.Client {
	if len(customHeaders) == 0 {
		return baseClient
	}

	transport := baseClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	wrappedClient := &http.Client{
		Transport: &customHeadersTransport{
			base:    transport,
			headers: customHeaders,
		},
		CheckRedirect: baseClient.CheckRedirect,
		Jar:           baseClient.Jar,
		Timeout:       baseClient.Timeout,
	}

	return wrappedClient
}

type Provider struct {
	client           *Client
	defaultModel     string
	inputTokenLimit  int
	outputTokenLimit int
}

func New(llmService llm.ServiceConfig, httpClient *http.Client) *Provider {
	// Wrap the HTTP client with custom headers if any are provided
	wrappedHTTPClient := wrapHTTPClientWithCustomHeaders(httpClient, llmService.CustomHeaders)
	client := NewClient(llmService.APIKey, wrappedHTTPClient, llmService.APIURL)

	return &Provider{
		client:           client,
		defaultModel:     llmService.DefaultModel,
		inputTokenLimit:  llmService.InputTokenLimit,
		outputTokenLimit: llmService.OutputTokenLimit,
	}
}

func conversationToMessagesList(posts []llm.Post) []Message {
	result := make([]Message, 0, len(posts))

	for _, post := range posts {
		role := RoleUser
		if post.Role == llm.PostRoleBot {
			role = RoleGPT
		} else if post.Role == llm.PostRoleSystem {
			continue // ASage doesn't support this
		}
		result = append(result, Message{
			User:    role,
			Message: post.Message,
		})
	}

	return result
}

func (s *Provider) GetDefaultConfig() llm.LanguageModelConfig {
	return llm.LanguageModelConfig{
		Model:              s.defaultModel,
		MaxGeneratedTokens: s.outputTokenLimit,
	}
}

func (s *Provider) createConfig(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := s.GetDefaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func (s *Provider) queryParamsFromConfig(cfg llm.LanguageModelConfig) QueryParams {
	return QueryParams{
		Model: cfg.Model,
	}
}

func (s *Provider) ChatCompletion(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	// ASage does not support streaming.
	result, err := s.ChatCompletionNoStream(request, opts...)
	if err != nil {
		return nil, err
	}
	return llm.NewStreamFromString(result), nil
}

func (s *Provider) ChatCompletionNoStream(request llm.CompletionRequest, opts ...llm.LanguageModelOption) (string, error) {
	params := s.queryParamsFromConfig(s.createConfig(opts))
	params.Message = conversationToMessagesList(request.Posts)
	params.SystemPrompt = request.ExtractSystemMessage()
	params.Persona = "default"

	response, err := s.client.Query(params)
	if err != nil {
		return "", err
	}
	return response.Message, nil
}

// TODO: Implement actual token counting. For now just estimated based off OpenAI estimations
func (s *Provider) CountTokens(text string) int {
	charCount := float64(len(text)) / 4.0
	wordCount := float64(len(strings.Fields(text))) / 0.75

	// Average the two and add a buffer
	return int((charCount+wordCount)/2.0) + 100
}

// TODO: Figure out what the actual token limit is. For now just be conservative.
func (s *Provider) InputTokenLimit() int {
	return 200000
}
