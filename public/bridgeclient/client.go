// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package bridgeclient provides a client library for Mattermost plugins and the server
// to interact with the AI plugin's LLM Bridge API to make requests to Agents to LLM providers.
package bridgeclient

import "net/http"

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

// Client is a client for the Mattermost Agents Plugin LLM Bridge API
type Client struct {
	httpClient http.Client
}

// Post represents a single message in the conversation
type Post struct {
	Role    string   `json:"role"`               // user|assistant|system
	Message string   `json:"message"`            // message content
	FileIDs []string `json:"file_ids,omitempty"` // Mattermost file IDs
}

// MattermostAccessScope defines runtime guardrails for a bridge completion run.
// When nil, no restrictions are applied.
type MattermostAccessScope struct {
	// TeamID anchors the run to a single team. Required when allowed_channel_ids is set.
	TeamID string `json:"team_id"`
	// AllowedChannelIDs is an optional allowlist of specific channel IDs.
	// Treated as an intersection with the team constraint, not an override.
	AllowedChannelIDs []string `json:"allowed_channel_ids,omitempty"`
}

// CompletionRequest represents a completion request
type CompletionRequest struct {
	Posts              []Post                 `json:"posts"`
	MaxGeneratedTokens int                    `json:"max_generated_tokens,omitempty"`
	JSONOutputFormat   map[string]interface{} `json:"json_output_format,omitempty"`
	// AllowedTools is an optional allowlist for agent completions. Each entry matches
	// a tool's (server_origin, name) as returned by GET .../agents/{id}/tools.
	// When provided on agent endpoints, only these eligible tools may run without approval.
	AllowedTools []AllowedToolRef `json:"allowed_tools,omitempty"`
	// Operation optionally overrides the default operation used for token usage logging.
	// If empty, the bridge chooses an operation based on endpoint type (agent/service).
	Operation string `json:"operation,omitempty"`
	// OperationSubType optionally overrides the default operation subtype used for token usage logging.
	// If empty, the bridge chooses a subtype based on request mode (streaming/nostream).
	OperationSubType string `json:"operation_subtype,omitempty"`
	// UserID is the optional Mattermost user ID making the request.
	// If provided, the bridge will check user-level permissions.
	UserID string `json:"user_id,omitempty"`
	// ChannelID is the optional Mattermost channel ID context for the request.
	// If provided along with UserID, the bridge will check both user and channel permissions.
	ChannelID string `json:"channel_id,omitempty"`
	// MattermostAccessScope defines runtime guardrails restricting which teams and channels
	// tools may access during this run. If nil, no restrictions are applied.
	MattermostAccessScope *MattermostAccessScope `json:"mattermost_access_scope,omitempty"`
}

// CompletionResponse represents a non-streaming completion response
type CompletionResponse struct {
	Completion string `json:"completion"`
}

// ErrorResponse represents an error response from the API
type ErrorResponse struct {
	Error string `json:"error"`
}

// BridgeAgentInfo represents basic agent information from the bridge API
type BridgeAgentInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Username    string `json:"username"`
	ServiceID   string `json:"service_id"`
	ServiceType string `json:"service_type"`
	IsDefault   bool   `json:"is_default"`
}

// BridgeServiceInfo represents basic service information from the bridge API
type BridgeServiceInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// AllowedToolRef identifies one tool in an allowlist (matches llm.Tool identity).
// ServerOrigin is required for bridge agent completion: it must match the value
// returned for that tool from GET /bridge/v1/agents/{agent}/tools (typically the MCP server base URL).
type AllowedToolRef struct {
	ServerOrigin string `json:"server_origin"`
	Name         string `json:"name"`
}

// BridgeToolInfo represents a bridge-eligible tool.
type BridgeToolInfo struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ServerOrigin string `json:"server_origin,omitempty"`
}

// AgentsResponse represents the response for the agents endpoint
type AgentsResponse struct {
	Agents []BridgeAgentInfo `json:"agents"`
}

// ServicesResponse represents the response for the services endpoint
type ServicesResponse struct {
	Services []BridgeServiceInfo `json:"services"`
}

// AgentToolsResponse represents the response for the agent tools endpoint.
type AgentToolsResponse struct {
	Tools []BridgeToolInfo `json:"tools"`
}

// NewClient creates a new LLM Bridge API client from a plugin's API interface.
func NewClient(api PluginAPI) *Client {
	client := &Client{}
	client.httpClient.Transport = &pluginAPIRoundTripper{api}
	return client
}

// NewClientFromApp creates a new LLM Bridge API client from the Mattermost server app layer.
// The userID is used for inter-plugin request authentication.
func NewClientFromApp(api AppAPI, userID string) *Client {
	client := &Client{}
	client.httpClient.Transport = &appAPIRoundTripper{api, userID}
	return client
}
