// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAgentToolsValidation(t *testing.T) {
	client := &Client{}

	_, err := client.GetAgentTools("bad", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid agent ID")

	validID := "abcdefghijklmnopqrstuvwxyz"
	_, err = client.GetAgentTools(validID, "bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user ID")
}

func TestGetAgentToolsTrimsAgentAndUserID(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/agents/abcdefghijklmnopqrstuvwxyz/tools", req.URL.Path)
		require.Equal(t, "user_id=zyxwvutsrqponmlkjihgfedcba", req.URL.RawQuery)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"tools":[]}`)),
		}, nil
	})

	tools, err := client.GetAgentTools("  abcdefghijklmnopqrstuvwxyz  ", " \tzyxwvutsrqponmlkjihgfedcba ")
	require.NoError(t, err)
	require.Empty(t, tools)
}

func TestGetAgentsValidation(t *testing.T) {
	client := &Client{}

	_, err := client.GetAgents("bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user ID")
}

func TestGetServicesValidation(t *testing.T) {
	client := &Client{}

	_, err := client.GetServices("bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid user ID")
}

func TestGetAgentToolsSuccess(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/agents/abcdefghijklmnopqrstuvwxyz/tools", req.URL.Path)
		require.Equal(t, "user_id=zyxwvutsrqponmlkjihgfedcba", req.URL.RawQuery)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"tools": [
					{"name":"weather_lookup","description":"Looks up weather"},
					{"name":"stock_quote","description":"Gets stock quote"}
				]
			}`)),
		}, nil
	})

	tools, err := client.GetAgentTools("abcdefghijklmnopqrstuvwxyz", "zyxwvutsrqponmlkjihgfedcba")
	require.NoError(t, err)
	require.Len(t, tools, 2)
	require.Equal(t, "weather_lookup", tools[0].Name)
	require.Equal(t, "Looks up weather", tools[0].Description)
	require.Equal(t, "stock_quote", tools[1].Name)
	require.Equal(t, "Gets stock quote", tools[1].Description)
}

func TestGetAgentToolsErrorResponse(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"missing permission"}`)),
		}, nil
	})

	_, err := client.GetAgentTools("abcdefghijklmnopqrstuvwxyz", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 403")
	require.Contains(t, err.Error(), "missing permission")
}

func TestGetAgentsSuccess(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/agents", req.URL.Path)
		require.Equal(t, "user_id=abcdefghijklmnopqrstuvwxyz", req.URL.RawQuery)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"agents": [
					{
						"id":"abcdefghijklmnopqrstuvwxyz",
						"displayName":"Support Agent",
						"username":"support.bot",
						"service_id":"svc-openai",
						"service_type":"openai",
						"is_default":true
					}
				]
			}`)),
		}, nil
	})

	agents, err := client.GetAgents("abcdefghijklmnopqrstuvwxyz")
	require.NoError(t, err)
	require.Len(t, agents, 1)
	require.Equal(t, "abcdefghijklmnopqrstuvwxyz", agents[0].ID)
	require.Equal(t, "Support Agent", agents[0].DisplayName)
	require.Equal(t, "support.bot", agents[0].Username)
	require.Equal(t, "svc-openai", agents[0].ServiceID)
	require.Equal(t, "openai", agents[0].ServiceType)
	require.True(t, agents[0].IsDefault)
}

func TestGetServicesSuccess(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/services", req.URL.Path)
		require.Equal(t, "user_id=abcdefghijklmnopqrstuvwxyz", req.URL.RawQuery)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"services": [
					{"id":"svc-openai","name":"OpenAI","type":"openai"},
					{"id":"svc-anthropic","name":"Anthropic","type":"anthropic"}
				]
			}`)),
		}, nil
	})

	services, err := client.GetServices("abcdefghijklmnopqrstuvwxyz")
	require.NoError(t, err)
	require.Len(t, services, 2)
	require.Equal(t, "svc-openai", services[0].ID)
	require.Equal(t, "OpenAI", services[0].Name)
	require.Equal(t, "openai", services[0].Type)
	require.Equal(t, "svc-anthropic", services[1].ID)
	require.Equal(t, "Anthropic", services[1].Name)
	require.Equal(t, "anthropic", services[1].Type)
}

func TestGetAgentsErrorResponse(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"upstream unavailable"}`)),
		}, nil
	})

	_, err := client.GetAgents("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 500")
	require.Contains(t, err.Error(), "upstream unavailable")
}

func TestGetServicesErrorResponse(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"bridge timeout"}`)),
		}, nil
	})

	_, err := client.GetServices("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 502")
	require.Contains(t, err.Error(), "bridge timeout")
}

func TestGetAgentsWithoutUserIDOmitsQuery(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/agents", req.URL.Path)
		require.Empty(t, req.URL.RawQuery)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"agents":[]}`)),
		}, nil
	})

	agents, err := client.GetAgents("")
	require.NoError(t, err)
	require.Empty(t, agents)
}

func TestGetAgentsWithWhitespaceUserIDOmitsQuery(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/agents", req.URL.Path)
		require.Empty(t, req.URL.RawQuery)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"agents":[]}`)),
		}, nil
	})

	agents, err := client.GetAgents(" \t\n ")
	require.NoError(t, err)
	require.Empty(t, agents)
}

func TestGetServicesWithoutUserIDOmitsQuery(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/services", req.URL.Path)
		require.Empty(t, req.URL.RawQuery)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"services":[]}`)),
		}, nil
	})

	services, err := client.GetServices("")
	require.NoError(t, err)
	require.Empty(t, services)
}

func TestGetAgentToolsWithoutUserIDOmitsQuery(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/mattermost-ai/bridge/v1/agents/abcdefghijklmnopqrstuvwxyz/tools", req.URL.Path)
		require.Empty(t, req.URL.RawQuery)

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"tools":[]}`)),
		}, nil
	})

	tools, err := client.GetAgentTools("abcdefghijklmnopqrstuvwxyz", "")
	require.NoError(t, err)
	require.Empty(t, tools)
}

func TestGetAgentToolsErrorResponseWithPlainTextBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("temporary outage")),
		}, nil
	})

	_, err := client.GetAgentTools("abcdefghijklmnopqrstuvwxyz", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 503")
	require.Contains(t, err.Error(), "temporary outage")
}

func TestGetAgentsMalformedResponseBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"agents":"not-an-array"}`)),
		}, nil
	})

	_, err := client.GetAgents("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal response")
}

func TestGetServicesMalformedResponseBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"services":"not-an-array"}`)),
		}, nil
	})

	_, err := client.GetServices("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal response")
}

func TestGetAgentToolsMalformedResponseBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"tools":"not-an-array"}`)),
		}, nil
	})

	_, err := client.GetAgentTools("abcdefghijklmnopqrstuvwxyz", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal response")
}

func TestGetAgentsErrorResponseWithPlainTextBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("upstream failed")),
		}, nil
	})

	_, err := client.GetAgents("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 502")
	require.Contains(t, err.Error(), "upstream failed")
}

func TestGetServicesErrorResponseWithPlainTextBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("internal error")),
		}, nil
	})

	_, err := client.GetServices("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 500")
	require.Contains(t, err.Error(), "internal error")
}

func TestGetAgentsErrorResponseWithoutErrorFieldFallsBackToBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"message":"invalid user filter"}`)),
		}, nil
	})

	_, err := client.GetAgents("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "request failed with status 400")
	require.Contains(t, err.Error(), `{"message":"invalid user filter"}`)
}

func TestGetAgentToolsReturnsReadBodyError(t *testing.T) {
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

	_, err := client.GetAgentTools("abcdefghijklmnopqrstuvwxyz", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read response body")
}

func TestGetAgentsReturnsExecuteRequestError(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network failure")
	})

	_, err := client.GetAgents("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to execute request")
	require.Contains(t, err.Error(), "network failure")
}

func TestGetServicesErrorResponseWithEmptyBody(t *testing.T) {
	client := &Client{}
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(" \n\t ")),
		}, nil
	})

	_, err := client.GetServices("")
	require.Error(t, err)
	require.EqualError(t, err, "request failed with status 502")
}

func TestAppendValidatedUserIDQuery(t *testing.T) {
	t.Run("empty user id leaves url unchanged", func(t *testing.T) {
		requestURL, err := appendValidatedUserIDQuery("/mattermost-ai/bridge/v1/agents", "")
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/agents", requestURL)
	})

	t.Run("whitespace-only user id leaves url unchanged", func(t *testing.T) {
		requestURL, err := appendValidatedUserIDQuery("/mattermost-ai/bridge/v1/agents", " \t\n ")
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/agents", requestURL)
	})

	t.Run("valid user id appends escaped query", func(t *testing.T) {
		requestURL, err := appendValidatedUserIDQuery("/mattermost-ai/bridge/v1/services", "abcdefghijklmnopqrstuvwxyz")
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/services?user_id=abcdefghijklmnopqrstuvwxyz", requestURL)
	})

	t.Run("valid user id with surrounding whitespace is trimmed", func(t *testing.T) {
		requestURL, err := appendValidatedUserIDQuery("/mattermost-ai/bridge/v1/services", "  abcdefghijklmnopqrstuvwxyz \t")
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/services?user_id=abcdefghijklmnopqrstuvwxyz", requestURL)
	})

	t.Run("valid user id appends with ampersand when query exists", func(t *testing.T) {
		requestURL, err := appendValidatedUserIDQuery("/mattermost-ai/bridge/v1/services?foo=bar", "abcdefghijklmnopqrstuvwxyz")
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/services?foo=bar&user_id=abcdefghijklmnopqrstuvwxyz", requestURL)
	})

	t.Run("existing user id query is replaced", func(t *testing.T) {
		requestURL, err := appendValidatedUserIDQuery("/mattermost-ai/bridge/v1/services?foo=bar&user_id=oldvalue", "abcdefghijklmnopqrstuvwxyz")
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/services?foo=bar&user_id=abcdefghijklmnopqrstuvwxyz", requestURL)
	})

	t.Run("valid user id appends without extra separator when query ends with question mark", func(t *testing.T) {
		requestURL, err := appendValidatedUserIDQuery("/mattermost-ai/bridge/v1/services?", "abcdefghijklmnopqrstuvwxyz")
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/services?user_id=abcdefghijklmnopqrstuvwxyz", requestURL)
	})

	t.Run("valid user id appends without extra separator when query ends with ampersand", func(t *testing.T) {
		requestURL, err := appendValidatedUserIDQuery("/mattermost-ai/bridge/v1/services?foo=bar&", "abcdefghijklmnopqrstuvwxyz")
		require.NoError(t, err)
		require.Equal(t, "/mattermost-ai/bridge/v1/services?foo=bar&user_id=abcdefghijklmnopqrstuvwxyz", requestURL)
	})

	t.Run("invalid user id returns validation error", func(t *testing.T) {
		_, err := appendValidatedUserIDQuery("/mattermost-ai/bridge/v1/services", "bad")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid user ID")
	})

	t.Run("malformed request url returns parse error", func(t *testing.T) {
		_, err := appendValidatedUserIDQuery("%", "abcdefghijklmnopqrstuvwxyz")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse request URL")
	})
}
