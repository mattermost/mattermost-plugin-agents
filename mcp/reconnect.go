// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// ReconnectNeededError is returned by a remote MCP tool call when the server
// rejects the user's authorization because it expired or was revoked. The
// session has already been dropped, so the next catalog build reports the
// server as unconnected instead of offering its dead tools. Server describes
// it from admin configuration; the conversation decides whether this turn can
// offer RequestMCPConnection for it (see llmcontext.MCPToolErrorResult), and
// Error is the result text when it cannot.
type ReconnectNeededError struct {
	Server llm.UnconnectedMCPServer
	Err    error
}

func (e *ReconnectNeededError) Error() string {
	return fmt.Sprintf("The user's access to %[1]s has expired or was revoked, so this call failed and nothing was done. "+
		"Tell the user they need to reconnect %[1]s before you can use it.", e.Server.Name)
}

func (e *ReconnectNeededError) Unwrap() error {
	return e.Err
}

// ConnectGuidance is the tool result text when this turn offers
// RequestMCPConnection for the server.
func (e *ReconnectNeededError) ConnectGuidance() string {
	return fmt.Sprintf("The user's access to %[1]s has expired or was revoked, so this call failed and nothing was done. "+
		"Its tools are unavailable until the user reconnects: call %[2]s with server %[1]q so they can, then retry.",
		e.Server.Name, RequestConnectionToolName)
}

// toolCallError maps a failed remote tool call to the error its resolver
// returns. When the server needs the user to authorize again, it records that,
// drops the dead session, and returns a ReconnectNeededError.
func (c *UserClients) toolCallError(client *Client, err error) error {
	if client == nil || client.serviceAccount {
		return err
	}
	if !c.rememberOAuthNeededForToolCall(client, err) {
		return err
	}
	closeDetachedClients(c.log, c.detachOrigins(clientOrigin(client, client.config.Name)))
	return &ReconnectNeededError{Server: describeConfiguredServer(&client.config), Err: err}
}
