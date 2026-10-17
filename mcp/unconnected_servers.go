// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// newToolAuthError describes an OAuth-needed failure for a configured server.
// The description and tool names come only from admin configuration: the
// shared tools cache reflects another user's view and must not be advertised
// for a server this user has not connected.
func newToolAuthError(server ServerConfig, authURL string, err error) llm.ToolAuthError {
	return llm.ToolAuthError{
		ServerName:        server.Name,
		ServerOrigin:      server.BaseURL,
		AuthURL:           authURL,
		Error:             err,
		ServerDescription: server.Description,
		ToolNames:         adminConfiguredToolNames(server.ToolConfigs),
	}
}

// adminConfiguredToolNames returns the enabled tool names an admin configured
// for a server.
func adminConfiguredToolNames(toolConfigs []ToolConfig) []string {
	names := make([]string, 0, len(toolConfigs))
	for _, toolConfig := range toolConfigs {
		if toolConfig.Enabled && toolConfig.Name != "" {
			names = append(names, toolConfig.Name)
		}
	}
	return names
}

// ReconnectNeededError is returned by an MCP tool call whose server rejected
// the user's lapsed authorization. The tool-loop owner decides whether the
// turn can offer ConnectMCPServer for Auth's server (see
// llmcontext.MCPToolErrorResult); Error is the result text when it cannot.
type ReconnectNeededError struct {
	Auth llm.ToolAuthError
}

func (e *ReconnectNeededError) Error() string {
	return fmt.Sprintf("MCP server %q needs the user to reconnect because their authorization expired or was revoked. "+
		"Ask the user to reconnect %s and then try again. Do not claim the action was performed: %v",
		e.Auth.ServerName, e.Auth.ServerName, e.Auth.Error)
}

func (e *ReconnectNeededError) Unwrap() error {
	return e.Auth.Error
}

// ConnectGuidance is the tool result text when the turn offers
// ConnectMCPServer for the server.
func (e *ReconnectNeededError) ConnectGuidance() string {
	return fmt.Sprintf("MCP server %q needs the user to reconnect because their authorization expired or was revoked. "+
		"Call %s with {\"server\":%q} so the user can reconnect it, then retry. Do not claim the action was performed: %v",
		e.Auth.ServerName, llm.ConnectMCPServerToolName, e.Auth.ServerName, e.Auth.Error)
}

// toolCallOAuthNeeded classifies a failed tool call. Service-account clients
// never use per-user OAuth, so their failures are never OAuth-needed.
func (c *UserClients) toolCallOAuthNeeded(client *Client, err error) (*OAuthNeededError, bool) {
	if err == nil || client == nil || client.serviceAccount || c.oauthManager == nil || client.config.Name == "" {
		return nil, false
	}
	if needed, ok := errors.AsType[*OAuthNeededError](err); ok {
		return needed, true
	}
	return errors.AsType[*OAuthNeededError](client.oauthNeededError(err))
}

// persistToolCallOAuthNeeded records that the user must reauthorize the
// server and returns the URL that starts it, or "" when there is none.
func (c *UserClients) persistToolCallOAuthNeeded(client *Client, needed *OAuthNeededError) string {
	authURL := needed.AuthURL()
	if authURL == "" {
		authURL = c.oauthManager.StartURL(client.config.Name)
	}
	if authURL == "" {
		return ""
	}
	if err := c.oauthManager.StoreAuthNeededState(c.userID, client.config.Name, authURL); err != nil {
		c.log.Warn("Failed to persist MCP OAuth-needed state after tool call",
			"userID", c.userID,
			"serverID", client.config.Name,
			"error", err)
	}
	return authURL
}

// dropSession forgets the client's server so the next catalog build re-dials
// it and reports it as unconnected instead of offering its dead tools.
func (c *UserClients) dropSession(client *Client) {
	closeDetachedClients(c.log, c.detachOrigins(clientOrigin(client, client.config.Name)))
}

// toolCallError maps a failed tool call to the error its resolver returns,
// turning lapsed authorization into a ReconnectNeededError.
func (c *UserClients) toolCallError(client *Client, err error) error {
	needed, ok := c.toolCallOAuthNeeded(client, err)
	if !ok {
		return err
	}
	authURL := c.persistToolCallOAuthNeeded(client, needed)
	if authURL == "" {
		return err
	}
	c.dropSession(client)
	return &ReconnectNeededError{Auth: newToolAuthError(client.config, authURL, err)}
}
