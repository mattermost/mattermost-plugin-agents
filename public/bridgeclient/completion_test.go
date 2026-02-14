// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"encoding/json"
	"errors"
	"fmt"
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

func TestServiceCompletionReturnsErrorFromJSONBodyWithoutErrorField(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"message":"invalid request"}`)),
		}, nil
	})

	_, err := client.ServiceCompletion("openai", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 400")
	require.Contains(t, err.Error(), `{"message":"invalid request"}`)
}

func TestAgentCompletionReturnsMarshalError(t *testing.T) {
	client := &Client{}

	_, err := client.AgentCompletion("abcdefghijklmnopqrstuvwxyz", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
		JSONOutputFormat: map[string]interface{}{
			"bad": func() {},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to marshal request")
}

func TestServiceCompletionReturnsMarshalError(t *testing.T) {
	client := &Client{}

	_, err := client.ServiceCompletion("openai", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
		JSONOutputFormat: map[string]interface{}{
			"bad": func() {},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to marshal request")
}

func TestServiceCompletionReturnsReadBodyError(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(&erroringReader{
				err: errors.New("read failure"),
			}),
		}, nil
	})

	_, err := client.ServiceCompletion("openai", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read response body")
}

func TestServiceCompletionReturnsExecuteRequestError(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp timeout")
	})

	_, err := client.ServiceCompletion("openai", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to execute request")
	require.Contains(t, err.Error(), "dial tcp timeout")
}

func TestServiceCompletionReturnsStatusOnlyWhenErrorBodyEmpty(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("   ")),
		}, nil
	})

	_, err := client.ServiceCompletion("openai", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.Error(t, err)
	require.EqualError(t, err, "request failed with status 503")
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

func TestAgentCompletionStreamHandlesLargeSSELine(t *testing.T) {
	client := &Client{}
	largeChunk := strings.Repeat("a", 70*1024)

	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			fmt.Sprintf(`data: {"Type":0,"Value":"%s"}`, largeChunk),
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

	text, readErr := result.ReadAll()
	require.NoError(t, readErr)
	require.Equal(t, largeChunk, text)
}

func TestAgentCompletionStreamEmitsErrorWhenSSELineExceedsLimit(t *testing.T) {
	client := &Client{}
	tooLargeChunk := strings.Repeat("a", maxSSELineBytes+16)

	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			fmt.Sprintf(`data: {"Type":0,"Value":"%s"}`, tooLargeChunk),
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

	event := <-result.Stream
	require.Equal(t, llm.EventTypeError, event.Type)
	streamErr, ok := event.Value.(error)
	require.True(t, ok)
	require.Contains(t, streamErr.Error(), "error reading stream")
	require.Contains(t, streamErr.Error(), "token too long")
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

func TestAgentCompletionStreamConvertsServerStringErrorToError(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
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

	event := <-result.Stream
	require.Equal(t, llm.EventTypeError, event.Type)
	streamErr, ok := event.Value.(error)
	require.True(t, ok)
	require.EqualError(t, streamErr, "server failed")
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

func TestAgentCompletionStreamConvertsServerMapErrorToError(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			`data: {"Type":2,"Value":{"error":"tool execution failed"}}`,
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
	require.Equal(t, llm.EventTypeError, event.Type)
	streamErr, ok := event.Value.(error)
	require.True(t, ok)
	require.EqualError(t, streamErr, "tool execution failed")
}

func TestAgentCompletionStreamConvertsServerMapMessageToError(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			`data: {"Type":2,"Value":{"message":"provider unavailable"}}`,
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
	require.Equal(t, llm.EventTypeError, event.Type)
	streamErr, ok := event.Value.(error)
	require.True(t, ok)
	require.EqualError(t, streamErr, "provider unavailable")
}

func TestAgentCompletionStreamConvertsNilErrorValueToFallbackError(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			`data: {"Type":2,"Value":null}`,
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
	require.Equal(t, llm.EventTypeError, event.Type)
	streamErr, ok := event.Value.(error)
	require.True(t, ok)
	require.EqualError(t, streamErr, "unknown stream error")
}

func TestAgentCompletionStreamConvertsBlankStringErrorToFallbackError(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			`data: {"Type":2,"Value":"   "}`,
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
	require.Equal(t, llm.EventTypeError, event.Type)
	streamErr, ok := event.Value.(error)
	require.True(t, ok)
	require.EqualError(t, streamErr, "unknown stream error")
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

func TestServiceCompletionStreamReturnsErrorFromJSONBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"permission denied"}`)),
		}, nil
	})

	_, err := client.ServiceCompletionStream("openai", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 403")
	require.Contains(t, err.Error(), "permission denied")
}

func TestAgentCompletionStreamReturnsMarshalError(t *testing.T) {
	client := &Client{}

	_, err := client.AgentCompletionStream("abcdefghijklmnopqrstuvwxyz", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
		JSONOutputFormat: map[string]interface{}{
			"bad": func() {},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to marshal request")
}

func TestServiceCompletionStreamReturnsMarshalError(t *testing.T) {
	client := &Client{}

	_, err := client.ServiceCompletionStream("openai", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
		JSONOutputFormat: map[string]interface{}{
			"bad": func() {},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to marshal request")
}

func TestServiceCompletionStreamStatusWhenErrorBodyUnreadable(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body: io.NopCloser(&erroringReader{
				err: errors.New("read failure"),
			}),
		}, nil
	})

	_, err := client.ServiceCompletionStream("openai", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.Error(t, err)
	require.EqualError(t, err, "request failed with status 503")
}

func TestAgentCompletionStreamReturnsExecuteRequestError(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset")
	})

	_, err := client.AgentCompletionStream("abcdefghijklmnopqrstuvwxyz", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to execute request")
	require.Contains(t, err.Error(), "connection reset")
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

	_, err = client.ServiceCompletion("   ", CompletionRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "service cannot be empty")

	_, err = client.ServiceCompletionStream("", CompletionRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "service cannot be empty")

	_, err = client.ServiceCompletionStream(" \t ", CompletionRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "service cannot be empty")
}

func TestAgentCompletionTrimsAgentPath(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/agent/abcdefghijklmnopqrstuvwxyz/nostream", req.URL.Path)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"completion":"ok"}`)),
		}, nil
	})

	completion, err := client.AgentCompletion(" \tabcdefghijklmnopqrstuvwxyz  ", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "ok", completion)
}

func TestAgentCompletionStreamTrimsAgentPath(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/agent/abcdefghijklmnopqrstuvwxyz", req.URL.Path)

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

	result, err := client.AgentCompletionStream(" \tabcdefghijklmnopqrstuvwxyz \n", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.NoError(t, err)

	text, readErr := result.ReadAll()
	require.NoError(t, readErr)
	require.Empty(t, text)
}

func TestServiceCompletionTrimsServicePath(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai/nostream", req.URL.Path)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai/nostream", req.URL.EscapedPath())

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"completion":"ok"}`)),
		}, nil
	})

	completion, err := client.ServiceCompletion("   openai\t", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.NoError(t, err)
	require.Equal(t, "ok", completion)
}

func TestServiceCompletionStreamTrimsServicePath(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai/v1", req.URL.Path)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai%2Fv1", req.URL.EscapedPath())

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

	result, err := client.ServiceCompletionStream("  openai/v1\t ", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.NoError(t, err)

	text, readErr := result.ReadAll()
	require.NoError(t, readErr)
	require.Empty(t, text)
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

func TestAgentCompletionStreamIgnoresNonDataLines(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			"event: keepalive",
			"id: 1",
			":heartbeat",
			`data: {"Type":0,"Value":"hello"}`,
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

	text, readErr := result.ReadAll()
	require.NoError(t, readErr)
	require.Equal(t, "hello", text)
}

func TestAgentCompletionStreamParsesDataLinesWithoutSpaceAfterColon(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			`data:{"Type":0,"Value":"hello "}`,
			`data:   {"Type":0,"Value":"world"}`,
			`data:{"Type":1,"Value":null}`,
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
	require.NoError(t, readErr)
	require.Equal(t, "hello world", text)
}

func TestAgentCompletionStreamParsesDataLinesWithTabAfterColon(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			"data:\t{\"Type\":0,\"Value\":\"hello \"}",
			"data:\t\t{\"Type\":0,\"Value\":\"world\"}",
			"data:\t{\"Type\":1,\"Value\":null}",
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
	require.NoError(t, readErr)
	require.Equal(t, "hello world", text)
}

func TestAgentCompletionStreamEmitsErrorWhenReaderFails(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(&erroringReader{
				err: errors.New("read failure"),
			}),
		}, nil
	})

	result, err := client.AgentCompletionStream("abcdefghijklmnopqrstuvwxyz", CompletionRequest{
		Posts: []Post{{Role: "user", Message: "hello"}},
	})
	require.NoError(t, err)

	event := <-result.Stream
	require.Equal(t, llm.EventTypeError, event.Type)
	require.Error(t, event.Value.(error))
	require.Contains(t, event.Value.(error).Error(), "error reading stream")
	require.Contains(t, event.Value.(error).Error(), "read failure")
}

func TestNormalizeStreamEvent(t *testing.T) {
	originalErr := errors.New("original error")

	testCases := []struct {
		name            string
		event           llm.TextStreamEvent
		expectedType    llm.EventType
		expectedMessage string
		expectSameError bool
	}{
		{
			name: "non-error event unchanged",
			event: llm.TextStreamEvent{
				Type:  llm.EventTypeText,
				Value: "hello",
			},
			expectedType: llm.EventTypeText,
		},
		{
			name: "error event with nil value uses fallback",
			event: llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: nil,
			},
			expectedType:    llm.EventTypeError,
			expectedMessage: "unknown stream error",
		},
		{
			name: "error event with string value becomes error",
			event: llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: "server failed",
			},
			expectedType:    llm.EventTypeError,
			expectedMessage: "server failed",
		},
		{
			name: "error event with empty string uses fallback",
			event: llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: "   ",
			},
			expectedType:    llm.EventTypeError,
			expectedMessage: "unknown stream error",
		},
		{
			name: "error event with map error field",
			event: llm.TextStreamEvent{
				Type: llm.EventTypeError,
				Value: map[string]interface{}{
					"error": "tool failed",
				},
			},
			expectedType:    llm.EventTypeError,
			expectedMessage: "tool failed",
		},
		{
			name: "error event with map error and message prefers error",
			event: llm.TextStreamEvent{
				Type: llm.EventTypeError,
				Value: map[string]interface{}{
					"error":   "tool failed",
					"message": "provider unavailable",
				},
			},
			expectedType:    llm.EventTypeError,
			expectedMessage: "tool failed",
		},
		{
			name: "error event with map message field",
			event: llm.TextStreamEvent{
				Type: llm.EventTypeError,
				Value: map[string]interface{}{
					"message": "provider unavailable",
				},
			},
			expectedType:    llm.EventTypeError,
			expectedMessage: "provider unavailable",
		},
		{
			name: "error event with empty map values uses fallback",
			event: llm.TextStreamEvent{
				Type: llm.EventTypeError,
				Value: map[string]interface{}{
					"error":   "",
					"message": "  ",
				},
			},
			expectedType:    llm.EventTypeError,
			expectedMessage: "unknown stream error",
		},
		{
			name: "error event with map without recognized fields uses fallback",
			event: llm.TextStreamEvent{
				Type: llm.EventTypeError,
				Value: map[string]interface{}{
					"code": "bad_gateway",
				},
			},
			expectedType:    llm.EventTypeError,
			expectedMessage: "unknown stream error",
		},
		{
			name: "error event with existing error is preserved",
			event: llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: originalErr,
			},
			expectedType:    llm.EventTypeError,
			expectSameError: true,
		},
		{
			name: "error event with unknown value type stringifies",
			event: llm.TextStreamEvent{
				Type:  llm.EventTypeError,
				Value: 42,
			},
			expectedType:    llm.EventTypeError,
			expectedMessage: "42",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			normalized := normalizeStreamEvent(tc.event)
			require.Equal(t, tc.expectedType, normalized.Type)

			if tc.expectedType != llm.EventTypeError {
				require.Equal(t, tc.event.Value, normalized.Value)
				return
			}

			normalizedErr, ok := normalized.Value.(error)
			require.True(t, ok)
			if tc.expectSameError {
				require.Equal(t, originalErr, normalizedErr)
				return
			}

			require.EqualError(t, normalizedErr, tc.expectedMessage)
		})
	}
}

func TestBuildCompletionHTTPRequest(t *testing.T) {
	t.Run("returns marshal error", func(t *testing.T) {
		_, err := buildCompletionHTTPRequest("/mattermost-ai/bridge/v1/completion/agent/abcdefghijklmnopqrstuvwxyz", CompletionRequest{
			Posts: []Post{{Role: "user", Message: "hello"}},
			JSONOutputFormat: map[string]interface{}{
				"bad": func() {},
			},
		}, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to marshal request")
	})

	t.Run("returns create request error", func(t *testing.T) {
		_, err := buildCompletionHTTPRequest("%", CompletionRequest{}, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to create request")
	})

	t.Run("returns error for empty request url", func(t *testing.T) {
		_, err := buildCompletionHTTPRequest("   ", CompletionRequest{}, false)
		require.Error(t, err)
		require.EqualError(t, err, "request URL cannot be empty")
	})

	t.Run("non-stream request sets content type only", func(t *testing.T) {
		req, err := buildCompletionHTTPRequest("/mattermost-ai/bridge/v1/completion/service/openai/nostream", CompletionRequest{
			Posts:        []Post{{Role: "user", Message: "hello"}},
			AllowedTools: []string{"weather_lookup"},
		}, false)
		require.NoError(t, err)
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		require.Empty(t, req.Header.Get("Accept"))

		body, readErr := io.ReadAll(req.Body)
		require.NoError(t, readErr)

		var payload CompletionRequest
		unmarshalErr := json.Unmarshal(body, &payload)
		require.NoError(t, unmarshalErr)
		require.Len(t, payload.Posts, 1)
		require.Equal(t, "weather_lookup", payload.AllowedTools[0])
	})

	t.Run("stream request sets accept header", func(t *testing.T) {
		req, err := buildCompletionHTTPRequest("/mattermost-ai/bridge/v1/completion/service/openai", CompletionRequest{
			Posts: []Post{{Role: "user", Message: "hello"}},
		}, true)
		require.NoError(t, err)
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		require.Equal(t, "text/event-stream", req.Header.Get("Accept"))
	})
}

func TestBuildServiceCompletionURL(t *testing.T) {
	t.Run("returns error for empty service", func(t *testing.T) {
		_, err := buildServiceCompletionURL("", false)
		require.Error(t, err)
		require.EqualError(t, err, "service cannot be empty")
	})

	t.Run("returns error for whitespace-only service", func(t *testing.T) {
		_, err := buildServiceCompletionURL("   \t ", false)
		require.Error(t, err)
		require.EqualError(t, err, "service cannot be empty")
	})

	t.Run("returns escaped non-stream url", func(t *testing.T) {
		requestURL, err := buildServiceCompletionURL("openai/v1 beta", false)
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai%2Fv1%20beta/nostream", requestURL)
	})

	t.Run("trims surrounding service whitespace", func(t *testing.T) {
		requestURL, err := buildServiceCompletionURL("  openai/v1 beta  ", false)
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai%2Fv1%20beta/nostream", requestURL)
	})

	t.Run("returns escaped stream url", func(t *testing.T) {
		requestURL, err := buildServiceCompletionURL("openai/v1 beta", true)
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/service/openai%2Fv1%20beta", requestURL)
	})
}

func TestBuildAgentCompletionURL(t *testing.T) {
	t.Run("returns error for invalid agent id", func(t *testing.T) {
		_, err := buildAgentCompletionURL("bad", false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid agent ID")
	})

	t.Run("returns non-stream url", func(t *testing.T) {
		requestURL, err := buildAgentCompletionURL("abcdefghijklmnopqrstuvwxyz", false)
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/agent/abcdefghijklmnopqrstuvwxyz/nostream", requestURL)
	})

	t.Run("returns stream url", func(t *testing.T) {
		requestURL, err := buildAgentCompletionURL("abcdefghijklmnopqrstuvwxyz", true)
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/agent/abcdefghijklmnopqrstuvwxyz", requestURL)
	})

	t.Run("trims surrounding agent id whitespace", func(t *testing.T) {
		requestURL, err := buildAgentCompletionURL("  abcdefghijklmnopqrstuvwxyz  ", false)
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/completion/agent/abcdefghijklmnopqrstuvwxyz/nostream", requestURL)
	})
}
