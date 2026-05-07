// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoveryEndpointsSuccess(t *testing.T) {
	testCases := []struct {
		name          string
		expectedPath  string
		expectedQuery string
		responseBody  string
		call          func(*Client) (any, error)
		assertResult  func(*testing.T, any)
	}{
		{
			name:          "GetAgents sends expected request and maps response",
			expectedPath:  "/mattermost-ai/bridge/v1/agents",
			expectedQuery: "user_id=abcdefghijklmnopqrstuvwxyz",
			responseBody: `{
				"agents": [
					{
						"id":"zyxwvutsrqponmlkjihgfedcba",
						"displayName":"Support Agent",
						"username":"support.bot",
						"service_id":"svc-openai",
						"service_type":"openai",
						"is_default":true
					}
				]
			}`,
			call: func(client *Client) (any, error) {
				return client.GetAgents("abcdefghijklmnopqrstuvwxyz")
			},
			assertResult: func(t *testing.T, result any) {
				agents, ok := result.([]BridgeAgentInfo)
				require.True(t, ok)
				require.Len(t, agents, 1)
				require.Equal(t, "zyxwvutsrqponmlkjihgfedcba", agents[0].ID)
				require.Equal(t, "Support Agent", agents[0].DisplayName)
				require.Equal(t, "support.bot", agents[0].Username)
				require.Equal(t, "svc-openai", agents[0].ServiceID)
				require.Equal(t, "openai", agents[0].ServiceType)
				require.True(t, agents[0].IsDefault)
			},
		},
		{
			name:          "GetServices omits query for empty user ID and maps response",
			expectedPath:  "/mattermost-ai/bridge/v1/services",
			expectedQuery: "",
			responseBody: `{
				"services": [
					{"id":"svc-openai","name":"OpenAI","type":"openai"}
				]
			}`,
			call: func(client *Client) (any, error) {
				return client.GetServices("")
			},
			assertResult: func(t *testing.T, result any) {
				services, ok := result.([]BridgeServiceInfo)
				require.True(t, ok)
				require.Len(t, services, 1)
				require.Equal(t, "svc-openai", services[0].ID)
				require.Equal(t, "OpenAI", services[0].Name)
				require.Equal(t, "openai", services[0].Type)
			},
		},
		{
			name:          "GetAgentTools sends expected request and maps response",
			expectedPath:  "/mattermost-ai/bridge/v1/agents/zyxwvutsrqponmlkjihgfedcba/tools",
			expectedQuery: "user_id=abcdefghijklmnopqrstuvwxyz",
			responseBody: `{
				"tools": [
					{"name":"weather_lookup","description":"Looks up weather","server_origin":"https://mcp.example"}
				]
			}`,
			call: func(client *Client) (any, error) {
				return client.GetAgentTools("zyxwvutsrqponmlkjihgfedcba", "abcdefghijklmnopqrstuvwxyz")
			},
			assertResult: func(t *testing.T, result any) {
				tools, ok := result.([]BridgeToolInfo)
				require.True(t, ok)
				require.Len(t, tools, 1)
				require.Equal(t, "weather_lookup", tools[0].Name)
				require.Equal(t, "Looks up weather", tools[0].Description)
				require.Equal(t, "https://mcp.example", tools[0].ServerOrigin)
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{}
			client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, req.Method)
				require.Equal(t, tc.expectedPath, req.URL.Path)
				require.Equal(t, tc.expectedQuery, req.URL.RawQuery)

				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(tc.responseBody)),
				}, nil
			})

			result, err := tc.call(client)
			require.NoError(t, err)
			tc.assertResult(t, result)
		})
	}
}

func TestDiscoveryValidation(t *testing.T) {
	const validID = "abcdefghijklmnopqrstuvwxyz"

	testCases := []struct {
		name              string
		call              func(*Client) error
		expectedErrSubstr string
	}{
		{
			name: "GetAgents rejects invalid user ID",
			call: func(client *Client) error {
				_, err := client.GetAgents("bad")
				return err
			},
			expectedErrSubstr: "invalid user ID",
		},
		{
			name: "GetServices rejects invalid user ID",
			call: func(client *Client) error {
				_, err := client.GetServices("bad")
				return err
			},
			expectedErrSubstr: "invalid user ID",
		},
		{
			name: "GetAgentTools rejects invalid agent ID",
			call: func(client *Client) error {
				_, err := client.GetAgentTools("bad", "")
				return err
			},
			expectedErrSubstr: "invalid agent ID",
		},
		{
			name: "GetAgentTools rejects invalid user ID",
			call: func(client *Client) error {
				_, err := client.GetAgentTools(validID, "bad")
				return err
			},
			expectedErrSubstr: "invalid user ID",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{}

			err := tc.call(client)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErrSubstr)
		})
	}
}
