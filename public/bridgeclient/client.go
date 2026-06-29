// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package bridgeclient provides a client library for Mattermost plugins and the server
// to interact with the AI plugin's LLM Bridge API to make requests to Agents to LLM providers.
package bridgeclient

import (
	"net/http"
)

const (
	AiPluginID         = "mattermost-ai"
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

// ToolHookConfig holds an optional HTTP callback path (plugin-relative) for a tool.
// The calling plugin encodes run context in the path; the agents plugin does not inspect it.
type ToolHookConfig struct {
	BeforeCallback string `json:"before_callback,omitempty"`
}

// Post represents a single message in the conversation
type Post struct {
	Role    string   `json:"role"`               // user|assistant|system
	Message string   `json:"message"`            // message content
	FileIDs []string `json:"file_ids,omitempty"` // Mattermost file IDs
}

// CompletionRequest represents a completion request
type CompletionRequest struct {
	Posts              []Post                 `json:"posts"`
	MaxGeneratedTokens int                    `json:"max_generated_tokens,omitempty"`
	JSONOutputFormat   map[string]interface{} `json:"json_output_format,omitempty"`
	// AllowedTools is an optional allowlist for agent completions. Each entry is a tool
	// name as returned by GET .../agents/{id}/tools (MCP and embedded tools only; built-in
	// tools are not discoverable or allowlistable via the bridge).
	// When provided on agent endpoints, only these eligible tools may run without approval.
	AllowedTools []string `json:"allowed_tools,omitempty"`
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
	// ToolHooks maps tool names to optional before-callback paths for that tool.
	// Requires Mattermost-Plugin-ID on the bridge request; callbacks hit that plugin's routes.
	ToolHooks map[string]ToolHookConfig `json:"tool_hooks,omitempty"`
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

// BridgeToolInfo represents a bridge-eligible tool (MCP or embedded; not built-in).
type BridgeToolInfo struct {
	// Name is the namespaced runtime tool name (e.g. "mattermost__search_posts").
	Name string `json:"name"`
	// BareName is the tool's bare name without the server namespace prefix
	// (e.g. "search_posts"). Both Name and BareName are accepted in allowed_tools;
	// prefer Name (namespaced) for new integrations.
	BareName    string `json:"bare_name,omitempty"`
	Description string `json:"description"`
	// ServerOrigin is the MCP server base URL or the embedded client key; never empty.
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
