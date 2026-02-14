// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/stretchr/testify/require"
)

func TestAgentCompletionSendsExpectedPayload(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/agent/abcdefghijklmnopqrstuvwxyz/nostream", req.URL.Path)
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))

		bodyBytes, err := io.ReadAll(req.Body)
		require.NoError(t, err)

		var payload CompletionRequest
		err = json.Unmarshal(bodyBytes, &payload)
		require.NoError(t, err)

		require.Len(t, payload.Posts, 1)
		require.Equal(t, "user", payload.Posts[0].Role)
		require.Equal(t, "hello", payload.Posts[0].Message)
		require.Equal(t, 128, payload.MaxGeneratedTokens)
		require.Equal(t, "json_schema", payload.JSONOutputFormat["type"])
		require.Equal(t, []string{"weather_lookup"}, payload.AllowedTools)
		require.Equal(t, "zyxwvutsrqponmlkjihgfedcba", payload.UserID)
		require.Equal(t, "mnopqrstuvwxabcdefghijkl", payload.ChannelID)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"completion":"done"}`)),
		}, nil
	})

	completion, err := client.AgentCompletion("abcdefghijklmnopqrstuvwxyz", CompletionRequest{
		Posts: []Post{
			{Role: "user", Message: "hello"},
		},
		MaxGeneratedTokens: 128,
		JSONOutputFormat: map[string]interface{}{
			"type": "json_schema",
		},
		AllowedTools: []string{"weather_lookup"},
		UserID:       "zyxwvutsrqponmlkjihgfedcba",
		ChannelID:    "mnopqrstuvwxabcdefghijkl",
	})
	require.NoError(t, err)
	require.Equal(t, "done", completion)
}

func TestServiceCompletionReturnsErrorFromPlainTextBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("upstream timeout")),
		}, nil
	})

	_, err := client.ServiceCompletion("openai", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 502")
	require.Contains(t, err.Error(), "upstream timeout")
}

func TestAgentCompletionStreamParsesSSEEvents(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "text/event-stream", req.Header.Get("Accept"))

		sse := strings.Join([]string{
			`data: {"Type":0,"Value":"hello "}`,
			`data: {"Type":0,"Value":"world"}`,
			`data: {"Type":1,"Value":null}`,
			"",
		}, "\n")

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	})

	result, err := client.AgentCompletionStream("abcdefghijklmnopqrstuvwxyz", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.NoError(t, err)

	text, err := result.ReadAll()
	require.NoError(t, err)
	require.Equal(t, "hello world", text)
}

func TestAgentCompletionStreamEmitsErrorForMalformedEvent(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			`data: {"Type":0,"Value":"hello "}`,
			`data: {`,
			"",
		}, "\n")

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	})

	result, err := client.AgentCompletionStream("abcdefghijklmnopqrstuvwxyz", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.NoError(t, err)

	event := <-result.Stream
	require.Equal(t, llm.EventTypeText, event.Type)

	event = <-result.Stream
	require.Equal(t, llm.EventTypeError, event.Type)
	require.Error(t, event.Value.(error))
}

func TestServiceCompletionStreamReturnsErrorFromPlainTextBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("bridge unavailable")),
		}, nil
	})

	_, err := client.ServiceCompletionStream("openai", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 503")
	require.Contains(t, err.Error(), "bridge unavailable")
}

func TestCompletionEndpointInputValidation(t *testing.T) {
	client := &Client{}

	_, err := client.AgentCompletion("bad", CompletionRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid agent ID")

	_, err = client.AgentCompletionStream("bad", CompletionRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid agent ID")

	_, err = client.ServiceCompletion("", CompletionRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "service cannot be empty")

	_, err = client.ServiceCompletionStream("", CompletionRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "service cannot be empty")
}

func TestServiceCompletionEscapesServicePath(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai/v1 beta/nostream", req.URL.Path)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai%2Fv1%20beta/nostream", req.URL.EscapedPath())

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"completion":"ok"}`)),
		}, nil
	})

	completion, err := client.ServiceCompletion("openai/v1 beta", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "ok", completion)
}

func TestServiceCompletionStreamEscapesServicePath(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai/v1 beta", req.URL.Path)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai%2Fv1%20beta", req.URL.EscapedPath())

		sse := strings.Join([]string{
			`data: {"Type":1,"Value":null}`,
			"",
		}, "\n")

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(sse)),
		}, nil
	})

	result, err := client.ServiceCompletionStream("openai/v1 beta", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.NoError(t, err)

	text, readErr := result.ReadAll()
	require.NoError(t, readErr)
	require.Empty(t, text)
}
