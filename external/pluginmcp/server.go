// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package pluginmcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServeHTTP gates the MCP endpoint on the Mattermost-Plugin-ID header so only
// the Agents plugin can call it; X-Mattermost-UserID is trusted only after
// that check.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Mattermost-Plugin-ID") != agentsPluginID {
		http.Error(w, "forbidden: plugin-ID header missing or mismatched", http.StatusForbidden)
		return
	}

	userID := r.Header.Get("X-Mattermost-UserID")
	ctx := withUserID(r.Context(), userID)
	r = r.WithContext(ctx)

	s.streamableHandler().ServeHTTP(w, r)
}

// streamableHandler lazily constructs the go-sdk HTTP handler. JSON responses
// are required because PluginHTTP buffers the full response.
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
