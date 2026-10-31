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

	if r.Header.Get(ReregisterHeader) != "" {
		s.serveReregister(w, r)
		return
	}

	userID := r.Header.Get("X-Mattermost-UserID")
	ctx := withUserID(r.Context(), userID)
	r = r.WithContext(ctx)

	s.streamableHandler().ServeHTTP(w, r)
}

// serveReregister answers the Agents plugin's request to register again. The
// registration itself is posted asynchronously, because the Agents plugin may
// send this request before it can serve /register.
func (s *Server) serveReregister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.startRegistration() {
		w.Header().Set(ReregisterHeader, ReregisterStopped)
		w.WriteHeader(http.StatusGone)
		return
	}
	w.Header().Set(ReregisterHeader, ReregisterAccepted)
	w.WriteHeader(http.StatusAccepted)
}

// buildStreamableHandler constructs the go-sdk HTTP handler. JSON responses
// are required because PluginHTTP buffers the full response.
func (s *Server) buildStreamableHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return s.server },
		&mcp.StreamableHTTPOptions{
			Stateless:    true,
			JSONResponse: true,
			// Explicitly the SDK default (introduced in go-sdk v1.7.0,
			// previously unlimited): requests are LLM-generated tool calls,
			// so 4 MiB is ample.
			MaxRequestBodyBytes: mcp.DefaultMaxRequestBodyBytes,
		},
	)
}
