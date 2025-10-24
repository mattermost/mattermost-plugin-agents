// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package client provides a client library for other Mattermost plugins to interact
// with the AI plugin's LLM Bridge API.
//
// Security Notice: The AI plugin's inter-plugin API supports optional permission checking.
// By default, if no UserID is provided in the CompletionRequest, no permission checks are performed.
// To enable permission checking, provide the UserID field (for user-level checks) or both UserID and
// ChannelID fields (for user and channel-level checks). If permission checks fail, the API will
// return a 403 Forbidden error. The calling plugin remains responsible for verifying permissions
// when not using the built-in permission checking.
package bridgeclient

import (
	"net/http"
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

// CompletionRequest represents a completion request
type CompletionRequest struct {
	Posts              []Post                 `json:"posts"`
	MaxGeneratedTokens int                    `json:"max_generated_tokens,omitempty"`
	JSONOutputFormat   map[string]interface{} `json:"json_output_format,omitempty"`
	// UserID is the optional Mattermost user ID making the request.
	// If provided, the bridge will check user-level permissions.
	UserID string `json:"user_id,omitempty"`
	// ChannelID is the optional Mattermost channel ID context for the request.
	// If provided along with UserID, the bridge will check both user and channel permissions.
	ChannelID string `json:"channel_id,omitempty"`
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
//   - api: Usually p.API from the plugin
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
//   - api: The App struct from the app package
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
