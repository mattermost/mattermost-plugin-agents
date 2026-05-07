// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
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

func TestAgentCompletionMalformedSuccessResponseBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"completion":123}`)),
		}, nil
	})

	_, err := client.AgentCompletion("abcdefghijklmnopqrstuvwxyz", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal response")
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

func TestAgentCompletionStreamReadAllReturnsServerError(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			`data: {"Type":0,"Value":"partial "}`,
			`data: {"Type":2,"Value":"server failed"}`,
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

	text, readErr := result.ReadAll()
	require.Error(t, readErr)
	require.Empty(t, text)
	require.EqualError(t, readErr, "server failed")
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
	type endpointType int
	const (
		agentNoStream endpointType = iota
		agentStream
		serviceNoStream
		serviceStream
	)

	testCases := []struct {
		name          string
		endpoint      endpointType
		input         string
		expectedError string
	}{
		{
			name:          "agent completion rejects invalid id",
			endpoint:      agentNoStream,
			input:         "bad",
			expectedError: "invalid agent ID",
		},
		{
			name:          "service completion rejects empty service",
			endpoint:      serviceNoStream,
			input:         "",
			expectedError: "service cannot be empty",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{}

			var err error
			switch tc.endpoint {
			case agentNoStream:
				_, err = client.AgentCompletion(tc.input, CompletionRequest{})
			case agentStream:
				_, err = client.AgentCompletionStream(tc.input, CompletionRequest{})
			case serviceNoStream:
				_, err = client.ServiceCompletion(tc.input, CompletionRequest{})
			case serviceStream:
				_, err = client.ServiceCompletionStream(tc.input, CompletionRequest{})
			default:
				t.Fatalf("unexpected endpoint type: %d", tc.endpoint)
			}

			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedError)
		})
	}
}
