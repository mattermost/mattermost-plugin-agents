// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcphelper

import (
	"net/http"

	"github.com/mattermost/mattermost-plugin-agents/public/bridgeclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServeHTTP is the http.Handler for this plugin's MCP endpoint. It only accepts
// inter-plugin calls from the Agents plugin before trusting X-Mattermost-UserID
// and passing the request to the go-sdk handler.
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

// streamableHandler lazily constructs a stateless JSON go-sdk HTTP handler.
// JSON responses are required because PluginHTTP buffers the full response.
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
