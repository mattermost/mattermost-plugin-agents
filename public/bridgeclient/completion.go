// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost-plugin-ai/llm"
)

// AgentCompletion makes a non-streaming completion request to a specific agent
//
// Parameters:
//   - agent: The username of the agent to use
//   - request: The completion request containing the conversation
//
// Returns the complete response text or an error.
//
// Example:
//
//	response, err := client.AgentCompletion("matty", client.CompletionRequest{
//	    Posts: []client.Post{
//	        {Role: "user", Message: "Take me on an adventure"},
//	    },
//	})
func (c *Client) AgentCompletion(agent string, request CompletionRequest) (string, error) {
	url := fmt.Sprintf("/%s/bridge/v1/completion/agent/%s/nostream", aiPluginID, agent)
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
//	response, err := client.ServiceCompletion("anthropic", client.CompletionRequest{
//	    Posts: []client.Post{
//	        {Role: "user", Message: "Write a haiku about coding"},
//	    },
//	})
func (c *Client) ServiceCompletion(service string, request CompletionRequest) (string, error) {
	url := fmt.Sprintf("/%s/bridge/v1/completion/service/%s/nostream", aiPluginID, service)
	return c.doCompletionRequest(url, request)
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
	url := fmt.Sprintf("/%s/bridge/v1/completion/agent/%s", aiPluginID, agent)
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
	url := fmt.Sprintf("/%s/bridge/v1/completion/service/%s", aiPluginID, service)
	return c.doStreamingRequest(url, request)
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

	// Ensure body is closed in all paths
	bodyClosed := false
	defer func() {
		if !bodyClosed {
			resp.Body.Close()
		}
	}()

	// Check for error status codes
	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
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

	// Mark body as handled by goroutine so defer doesn't close it
	bodyClosed = true

	return &llm.TextStreamResult{
		Stream: stream,
	}, nil
}
