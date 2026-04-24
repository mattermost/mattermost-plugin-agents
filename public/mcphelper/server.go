// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcphelper

import (
	"net/http"

	"github.com/mattermost/mattermost-plugin-agents/public/bridgeclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServeHTTP is the http.Handler for this plugin's MCP endpoint. Source plugins
// wire it in their own ServeHTTP for requests whose path matches
// Server.config.Path.
//
// The handler performs two jobs before delegating to the go-sdk streamable
// handler:
//
//  1. Security gate. The request is rejected with 403 Forbidden unless it
//     carries Mattermost-Plugin-ID == "mattermost-ai". Mattermost server
//     strips this header on external requests (see plugin_requests.go), so
//     only genuine inter-plugin RPC from the Agents plugin can clear the gate.
//     This closes a trust gap: the X-Mattermost-UserID header is NOT stripped
//     by the server, so external callers could otherwise impersonate any user.
//
//  2. User-ID context propagation. If the gate passes, the handler extracts
//     X-Mattermost-UserID and injects it into the request context via
//     withUserID, where tool handlers can retrieve it with GetUserID.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Mattermost-Plugin-ID") != bridgeclient.AiPluginID {
		http.Error(w, "forbidden: plugin-ID header missing or mismatched", http.StatusForbidden)
		return
	}

	userID := r.Header.Get("X-Mattermost-UserID")
	ctx := withUserID(r.Context(), userID)
	r = r.WithContext(ctx)

	s.streamableHandler().ServeHTTP(w, r)
}

// streamableHandler lazily constructs the go-sdk streamable HTTP handler on
// first request. Returning a shared *mcp.Server from the getServer closure
// matches the agents plugin's own pattern at mcpserver/plugin_handlers.go:83.
//
// The options are:
//
//   - Stateless: true — required because PluginHTTP is strict request/response;
//     there is no persistent session transport available for session-id
//     tracking.
//   - JSONResponse: true — required because PluginHTTP buffers the full
//     response before returning; SSE (the default) would deadlock.
func (s *Server) streamableHandler() http.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handlerBuiltOK {
		return s.handler
	}
	s.handler = mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return s.server },
		&mcp.StreamableHTTPOptions{
			Stateless:    true,
			JSONResponse: true,
		},
	)
	s.handlerBuiltOK = true
	return s.handler
}
