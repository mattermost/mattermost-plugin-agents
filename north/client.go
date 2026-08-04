// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package north implements a Cohere North provider for the Agents plugin.
//
// Unlike the Bifrost-backed providers, this provider fully delegates the
// agent loop to a Cohere North instance: requests go to North's native chat
// API (POST {base}/v1/chat) optionally scoped to a North agent, and North
// executes its own tools server-side. The provider translates North's SSE
// stream into the plugin's TextStreamEvent stream and never emits tool-call
// events, so the plugin-side tool runner treats every response as final.
package north

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	chatPath = "/v1/chat"

	// DefaultStreamingTimeout bounds the wait between consecutive stream
	// events. North runs multi-step agent loops server-side, so the overall
	// request duration is unbounded, but silence longer than this indicates
	// a stalled connection.
	DefaultStreamingTimeout = 120 * time.Second

	// maxSSELineSize bounds a single SSE line. North debug events can carry
	// whole prompts, so this needs to be generous.
	maxSSELineSize = 10 * 1024 * 1024
)

// Client is a minimal client for the Cohere North native chat API.
type Client struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	idleTimeout time.Duration
}

// NewClient creates a North API client. apiURL is the instance API base URL
// (e.g. "https://north.example.com/api"); a trailing "/v1" segment is
// tolerated and stripped. idleTimeout bounds the wait between stream events;
// non-positive values fall back to DefaultStreamingTimeout.
func NewClient(apiURL, token string, idleTimeout time.Duration) *Client {
	if idleTimeout <= 0 {
		idleTimeout = DefaultStreamingTimeout
	}
	return &Client{
		baseURL:     normalizeBaseURL(apiURL),
		token:       token,
		httpClient:  &http.Client{},
		idleTimeout: idleTimeout,
	}
}

// normalizeBaseURL strips trailing slashes and a trailing /v1 segment so both
// "https://host/api" and "https://host/api/v1" resolve chat to
// "https://host/api/v1/chat".
func normalizeBaseURL(apiURL string) string {
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	apiURL = strings.TrimSuffix(apiURL, "/v1")
	return apiURL
}

// ChatMessage is a single message in a North native chat request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AgentRef selects the North agent that handles the request.
type AgentRef struct {
	ID string `json:"id"`
}

// ThinkingOptions configures North's reasoning mode.
type ThinkingOptions struct {
	Type string `json:"type"` // "enabled" or "disabled"
}

// ChatRequest is the subset of North's native chat request the provider uses.
// Requests are always stateless: the plugin resends the full thread each turn,
// so no server-side conversation state is required.
type ChatRequest struct {
	Messages  []ChatMessage    `json:"messages"`
	Stream    bool             `json:"stream"`
	Stateless bool             `json:"stateless"`
	Agent     *AgentRef        `json:"agent,omitempty"`
	Thinking  *ThinkingOptions `json:"thinking,omitempty"`
	MaxTokens int              `json:"max_tokens,omitempty"`
}

// Usage mirrors North's token usage block.
type Usage struct {
	Tokens *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"tokens"`
	CachedTokens int64 `json:"cached_tokens"`
}

// ChatError is the error payload embedded in responses and stream-end events.
type ChatError struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

func (e *ChatError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("north: %s (%s)", e.Message, e.ErrorCode)
	}
	return fmt.Sprintf("north: %s", e.ErrorCode)
}

// ContentItem is one typed content entry of a response message.
type ContentItem struct {
	Type     string `json:"type"` // "text", "thinking", "document"
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
}

// ResponseMessage is a message in a non-streaming chat response.
type ResponseMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ContentItems decodes the message content, which North serializes either as
// a plain string or as a list of typed items.
func (m ResponseMessage) ContentItems() []ContentItem {
	if len(m.Content) == 0 {
		return nil
	}
	var asString string
	if err := json.Unmarshal(m.Content, &asString); err == nil {
		return []ContentItem{{Type: "text", Text: asString}}
	}
	var items []ContentItem
	if err := json.Unmarshal(m.Content, &items); err == nil {
		return items
	}
	return nil
}

// ChatResponse is a non-streaming North chat response.
type ChatResponse struct {
	ConversationID string            `json:"conversation_id"`
	FinishReason   string            `json:"finish_reason"`
	Messages       []ResponseMessage `json:"messages"`
	Usage          *Usage            `json:"usage"`
	Error          *ChatError        `json:"error"`
}

// Citation mirrors North's citation payload from citation-start events.
type Citation struct {
	Text    string           `json:"text"`
	Start   int              `json:"start"`
	End     int              `json:"end"`
	Sources []CitationSource `json:"sources"`
}

// CitationSource is one source backing a citation. Document sources carry a
// free-form document map that typically includes url/title fields for web
// results.
type CitationSource struct {
	Type     string         `json:"type"` // "document" or "tool"
	ID       string         `json:"id"`
	Document map[string]any `json:"document"`
}

// ToolCallDelta mirrors the tool_calls payload of tool-call-start events.
type ToolCallDelta struct {
	DisplayName string `json:"display_name"`
	Function    *struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// DeltaMessage is the message body of a streaming delta.
type DeltaMessage struct {
	Content   json.RawMessage `json:"content"`
	ToolPlan  string          `json:"tool_plan"`
	ToolCalls *ToolCallDelta  `json:"tool_calls"`
	Citations *Citation       `json:"citations"`
}

// DeltaContent decodes a delta's content, which is either a plain string or a
// typed object ({"type":"text","text":...} / {"type":"thinking",...}).
func (m *DeltaMessage) DeltaContent() (ContentItem, bool) {
	if m == nil || len(m.Content) == 0 {
		return ContentItem{}, false
	}
	var asString string
	if err := json.Unmarshal(m.Content, &asString); err == nil {
		return ContentItem{Type: "text", Text: asString}, true
	}
	var item ContentItem
	if err := json.Unmarshal(m.Content, &item); err == nil {
		return item, true
	}
	return ContentItem{}, false
}

// StreamDelta is the delta body of a streaming event.
type StreamDelta struct {
	Message      *DeltaMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
	Usage        *Usage        `json:"usage"`
	Error        *ChatError    `json:"error"`
}

// StreamEvent is a single parsed North SSE event.
type StreamEvent struct {
	Type  string       `json:"type"`
	Delta *StreamDelta `json:"delta"`
}

// errorResponse is North's structured non-2xx error body.
type errorResponse struct {
	ErrorType string `json:"error_type"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func (c *Client) newChatRequest(ctx context.Context, request ChatRequest) (*http.Request, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal north chat request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+chatPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create north chat request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// decodeErrorResponse turns a non-2xx response body into a descriptive error.
func decodeErrorResponse(statusCode int, body []byte) error {
	var errResp errorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Message != "" {
		return fmt.Errorf("north API error (HTTP %d, %s): %s", statusCode, errResp.ErrorCode, errResp.Message)
	}
	return fmt.Errorf("north API error (HTTP %d)", statusCode)
}

// Chat performs a non-streaming chat request.
func (c *Client) Chat(ctx context.Context, request ChatRequest) (*ChatResponse, error) {
	request.Stream = false
	req, err := c.newChatRequest(ctx, request)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("north chat request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read north chat response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, decodeErrorResponse(resp.StatusCode, body)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("failed to decode north chat response: %w", err)
	}
	return &chatResp, nil
}

// ChatStream performs a streaming chat request and returns a channel of
// parsed events. The channel is closed when the stream ends, errors, or the
// idle timeout elapses without an event; a trailing event with Type "error"
// and a populated Delta.Error reports stream-level failures.
func (c *Client) ChatStream(ctx context.Context, request ChatRequest) (<-chan StreamEvent, error) {
	request.Stream = true

	ctx, cancel := context.WithCancel(ctx)
	req, err := c.newChatRequest(ctx, request)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req) //nolint:bodyclose // closed by the reader goroutine (or the error paths below)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("north chat stream request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		cancel()
		return nil, decodeErrorResponse(resp.StatusCode, body)
	}

	events := make(chan StreamEvent)

	// Watchdog: cancel the request when no event arrives within the idle
	// timeout. Each delivered event resets the timer.
	idleTimer := time.AfterFunc(c.idleTimeout, cancel)

	go func() {
		defer close(events)
		defer resp.Body.Close()
		defer cancel()
		defer idleTimer.Stop()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), maxSSELineSize)
		for scanner.Scan() {
			line := scanner.Text()
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var event StreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			idleTimer.Reset(c.idleTimeout)
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			select {
			case events <- StreamEvent{
				Type:  "error",
				Delta: &StreamDelta{Error: &ChatError{ErrorCode: "STREAM_READ_ERROR", Message: err.Error()}},
			}:
			case <-ctx.Done():
			}
		}
	}()

	return events, nil
}
