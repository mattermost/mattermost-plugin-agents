// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package client provides a client library for other Mattermost plugins to interact
// with the AI plugin's LLM Bridge API.
//
// Security Notice: The AI plugin's inter-plugin API does not perform permission checks.
// The calling plugin is responsible for verifying that the user has appropriate permissions
// before making requests on their behalf.
package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/pkg/errors"
)

const (
	aiPluginID         = "mattermost-ai"
	mattermostServerID = "mattermost-server"
)

// PluginAPI is the minimal interface needed from the Mattermost plugin API
type PluginAPI interface {
	PluginHTTP(*http.Request) *http.Response
}

// AppAPI is the minimal interface needed from the Mattermost app layer
type AppAPI interface {
	ServeInternalPluginRequest(userID string, w http.ResponseWriter, r *http.Request, sourcePluginID, destinationPluginID string)
}

// Client is a client for the Mattermost AI Plugin LLM Bridge API
type Client struct {
	httpClient http.Client
}

// pluginAPIRoundTripper wraps the Mattermost plugin API for HTTP requests
type pluginAPIRoundTripper struct {
	api PluginAPI
}

func (p *pluginAPIRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp := p.api.PluginHTTP(req)
	if resp == nil {
		return nil, errors.Errorf("failed to make interplugin request")
	}
	return resp, nil
}

// appAPIRoundTripper wraps the Mattermost app layer API for HTTP requests
type appAPIRoundTripper struct {
	api    AppAPI
	userID string
}

func removeFirstPath(r *http.Request) {
	path := r.URL.Path

	// Find the position of the second slash (first slash after the leading one)
	secondSlash := strings.Index(path[1:], "/")

	if secondSlash == -1 {
		// No second slash found, set to just "/"
		r.URL.Path = "/"
		return
	}

	// Update the path to everything from the second slash onwards
	r.URL.Path = path[1+secondSlash:]
}

func (a *appAPIRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Create a response recorder to capture the response
	recorder := httptest.NewRecorder()

	removeFirstPath(req)

	// Make the inter-plugin request from the server to the AI plugin
	a.api.ServeInternalPluginRequest(a.userID, recorder, req, mattermostServerID, aiPluginID)

	// Convert the recorder to an http.Response
	return recorder.Result(), nil
}

// Post represents a single message in the conversation
type Post struct {
	Role    string `json:"role"` // user|assistant|system
	Message string `json:"message"`
	Files   []File `json:"files,omitempty"`
}

// File represents a file attachment
type File struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Data     string `json:"data"` // base64 encoded
}

// CompletionRequest represents a completion request
type CompletionRequest struct {
	Posts              []Post                 `json:"posts"`
	MaxGeneratedTokens int                    `json:"max_generated_tokens,omitempty"`
	JSONOutputFormat   map[string]interface{} `json:"json_output_format,omitempty"`
}

// CompletionResponse represents a non-streaming completion response
type CompletionResponse struct {
	Completion string `json:"completion"`
}

// ErrorResponse represents an error response from the API
type ErrorResponse struct {
	Error string `json:"error"`
}

// NewClient creates a new LLM Bridge API client using a PluginAPI interface
//
// Parameters:
//   - api: Any type that implements PluginAPI (has a PluginHTTP method)
//
// Example:
//
//	type MyPlugin struct {
//	    plugin.MattermostPlugin
//	    llmClient *client.Client
//	}
//
//	func (p *MyPlugin) OnActivate() error {
//	    p.llmClient = client.NewClient(p.API)
//	    return nil
//	}
func NewClient(api PluginAPI) *Client {
	client := &Client{}
	client.httpClient.Transport = &pluginAPIRoundTripper{api}
	return client
}

// NewClientFromApp creates a new LLM Bridge API client using the app layer API
//
// This constructor is for use within the Mattermost server app layer to make
// inter-plugin requests to the AI plugin.
//
// Parameters:
//   - api: Any type that implements AppAPI (has a ServeInterPluginRequest method)
//
// Example:
//
//	type MyService struct {
//	    app       *app.App
//	    llmClient *client.Client
//	}
//
//	func NewMyService(app *app.App) *MyService {
//	    return &MyService{
//	        app:       app,
//	        llmClient: client.NewClientFromApp(app, userID),
//	    }
//	}
func NewClientFromApp(api AppAPI, userID string) *Client {
	client := &Client{}
	client.httpClient.Transport = &appAPIRoundTripper{api, userID}
	return client
}

// AgentCompletion makes a non-streaming completion request to a specific agent (bot)
//
// Parameters:
//   - agent: The username of the agent/bot to use
//   - request: The completion request containing the conversation
//
// Returns the complete response text or an error.
//
// Example:
//
//	response, err := client.AgentCompletion("gpt4", client.CompletionRequest{
//	    Posts: []client.Post{
//	        {Role: "user", Message: "What is the capital of France?"},
//	    },
//	})
func (c *Client) AgentCompletion(agent string, request CompletionRequest) (string, error) {
	url := fmt.Sprintf("/%s/api/v1/agent/%s/completion/nostream", aiPluginID, agent)
	return c.doCompletionRequest(url, request)
}

// ServiceCompletion makes a non-streaming completion request to a specific service
//
// Parameters:
//   - service: The ID or name of the LLM service to use
//   - request: The completion request containing the conversation
//
// Returns the complete response text or an error.
//
// Example:
//
//	response, err := client.ServiceCompletion("openai", client.CompletionRequest{
//	    Posts: []client.Post{
//	        {Role: "user", Message: "Write a haiku about coding"},
//	    },
//	})
func (c *Client) ServiceCompletion(service string, request CompletionRequest) (string, error) {
	url := fmt.Sprintf("/%s/api/v1/service/%s/completion/nostream", aiPluginID, service)
	return c.doCompletionRequest(url, request)
}

// doCompletionRequest performs a non-streaming completion request
func (c *Client) doCompletionRequest(url string, request CompletionRequest) (string, error) {
	// Marshal the request body
	body, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create the HTTP request
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	// Make the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for error status codes
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
		}
		return "", fmt.Errorf("request failed with status %d: %s", resp.StatusCode, errResp.Error)
	}

	// Parse the success response
	var completionResp CompletionResponse
	if err := json.Unmarshal(respBody, &completionResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return completionResp.Completion, nil
}

// AgentCompletionStream makes a streaming completion request to a specific agent (bot)
// and returns a TextStreamResult for processing the stream.
//
// Parameters:
//   - agent: The username of the agent/bot to use
//   - request: The completion request containing the conversation
//
// Returns a *llm.TextStreamResult that provides a channel of TextStreamEvent objects.
// The caller should read from the Stream channel to process events.
//
// Example:
//
//	result, err := client.AgentCompletionStream("gpt4", client.CompletionRequest{
//	    Posts: []client.Post{
//	        {Role: "user", Message: "Tell me a story"},
//	    },
//	})
//	if err != nil {
//	    return err
//	}
//
//	for event := range result.Stream {
//	    switch event.Type {
//	    case llm.EventTypeText:
//	        fmt.Print(event.Value.(string))
//	    case llm.EventTypeError:
//	        return event.Value.(error)
//	    case llm.EventTypeEnd:
//	        return nil
//	    }
//	}
func (c *Client) AgentCompletionStream(agent string, request CompletionRequest) (*llm.TextStreamResult, error) {
	url := fmt.Sprintf("/%s/api/v1/agent/%s/completion", aiPluginID, agent)
	return c.doStreamingRequest(url, request)
}

// ServiceCompletionStream makes a streaming completion request to a specific service
// and returns a TextStreamResult for processing the stream.
//
// Parameters:
//   - service: The ID or name of the LLM service to use
//   - request: The completion request containing the conversation
//
// Returns a *llm.TextStreamResult that provides a channel of TextStreamEvent objects.
// The caller should read from the Stream channel to process events.
//
// Example:
//
//	result, err := client.ServiceCompletionStream("anthropic", client.CompletionRequest{
//	    Posts: []client.Post{
//	        {Role: "user", Message: "Explain quantum computing"},
//	    },
//	})
//	if err != nil {
//	    return err
//	}
//
//	// Use the helper method to read all text
//	text, err := result.ReadAll()
//	if err != nil {
//	    return err
//	}
//	fmt.Println(text)
func (c *Client) ServiceCompletionStream(service string, request CompletionRequest) (*llm.TextStreamResult, error) {
	url := fmt.Sprintf("/%s/api/v1/service/%s/completion", aiPluginID, service)
	return c.doStreamingRequest(url, request)
}

// doStreamingRequest performs a streaming completion request and returns a TextStreamResult
func (c *Client) doStreamingRequest(url string, request CompletionRequest) (*llm.TextStreamResult, error) {
	// Marshal the request body
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create the HTTP request
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	// Make the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}

	// Check for error status codes
	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("request failed with status %d", resp.StatusCode)
		}
		var errResp ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
		}
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, errResp.Error)
	}

	// Create a channel for the stream
	stream := make(chan llm.TextStreamEvent)

	// Start a goroutine to read the SSE stream and populate the channel
	go func() {
		defer resp.Body.Close()
		defer close(stream)

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()

			// SSE lines start with "data: "
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			// Extract the data portion
			data := strings.TrimPrefix(line, "data: ")

			// Check for empty data lines
			if data == "" {
				continue
			}

			// Parse the JSON event
			var event llm.TextStreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				// Send an error event
				stream <- llm.TextStreamEvent{
					Type:  llm.EventTypeError,
					Value: fmt.Errorf("error parsing stream event: %w", err),
				}
				return
			}

			// Send the event to the channel
			stream <- event

			// If this is an end or error event, stop reading
			if event.Type == llm.EventTypeEnd || event.Type == llm.EventTypeError {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			stream <- llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: fmt.Errorf("error reading stream: %w", err),
			}
		}
	}()

	return &llm.TextStreamResult{
		Stream: stream,
	}, nil
}
