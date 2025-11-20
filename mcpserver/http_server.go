// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver/auth"
	loggerlib "github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/mattermost/mattermost-plugin-ai/mcpserver/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MattermostHTTPMCPServer wraps MattermostMCPServer for HTTP transport
type MattermostHTTPMCPServer struct {
	*MattermostMCPServer
	config            HTTPConfig
	sseHandler        http.Handler
	streamableHandler http.Handler
	httpServer        *http.Server
}

// NewHTTPServer creates a new HTTP transport MCP server
func NewHTTPServer(config HTTPConfig, logger loggerlib.Logger) (*MattermostHTTPMCPServer, error) {
	if config.MMServerURL == "" {
		return nil, fmt.Errorf("server URL cannot be empty")
	}
	if config.HTTPPort <= 0 {
		return nil, fmt.Errorf("HTTP port must be greater than 0")
	}
	if config.HTTPBindAddr == "" {
		return nil, fmt.Errorf("HTTP bind address cannot be empty")
	}

	// Require site-url when binding to all interfaces for security
	// Per MCP spec: binding to 0.0.0.0 is discouraged, but if used, must have proper external URL
	if config.HTTPBindAddr == "0.0.0.0" && config.SiteURL == "" {
		return nil, fmt.Errorf("site-url is required when http-bind-addr is 0.0.0.0 to ensure secure origin validation and proper OAuth metadata")
	}

	if logger == nil {
		var err error
		logger, err = loggerlib.CreateDefaultLogger()
		if err != nil {
			return nil, fmt.Errorf("failed to create default logger: %w", err)
		}
	}

	mattermostServer := &MattermostHTTPMCPServer{
		MattermostMCPServer: &MattermostMCPServer{
			logger: logger,
			config: config,
		},
		config: config,
	}

	// Create OAuth authentication provider
	mattermostServer.authProvider = auth.NewOAuthAuthenticationProvider(
		config.GetMMServerURL(),
		config.GetMMInternalServerURL(),
		config.GetMMServerURL(), // OAuth issuer is the external server URL
		logger,
	)

	mattermostServer.mcpServer = mcp.NewServer(
		&mcp.Implementation{
			Name:    "mattermost-mcp-server",
			Version: "0.1.0",
		},
		nil, // ServerOptions - keeping nil for now
	)

	// Register tools with remote access mode
	mattermostServer.registerTools(tools.AccessModeRemote)

	// Create HTTP server with OAuth endpoints and MCP routing
	addr := fmt.Sprintf("%s:%d", config.HTTPBindAddr, config.HTTPPort)

	// Create SSE handler for backwards compatibility
	mattermostServer.sseHandler = mcp.NewSSEHandler(func(req *http.Request) *mcp.Server {
		return mattermostServer.mcpServer
	}, &mcp.SSEOptions{})

	// Create streamable HTTP handler for modern MCP communication
	mattermostServer.streamableHandler = mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		return mattermostServer.mcpServer
	}, nil)

	// Create HTTP mux router and setup all routes
	httpMux := http.NewServeMux()
	mattermostServer.setupRoutes(httpMux)

	// Apply recovery, logging, and security middleware to the mux
	mainHandler := mattermostServer.loggingMiddleware(httpMux)
	recoveryHandler := mattermostServer.recoveryMiddleware(mainHandler)
	secureHandler := mattermostServer.securityMiddleware(recoveryHandler)

	// Create HTTP server with security middleware
	mattermostServer.httpServer = &http.Server{
		Addr:         addr,
		Handler:      secureHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return mattermostServer, nil
}

// Serve starts the HTTP MCP server
func (s *MattermostHTTPMCPServer) Serve() error {
	s.logger.Info("starting HTTP MCP server with SSE support",
		"bind_addr", s.config.HTTPBindAddr,
		"port", s.config.HTTPPort,
		"server_url", s.config.GetMMServerURL(),
	)

	// Start the custom HTTP server with OAuth endpoints
	return s.httpServer.ListenAndServe()
}

// GetTestHandler returns the HTTP handler for testing purposes
func (s *MattermostHTTPMCPServer) GetTestHandler() http.Handler {
	return s.httpServer.Handler
}

// recoveryMiddleware provides panic recovery for HTTP handlers
func (s *MattermostHTTPMCPServer) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.logger.Error("Panic in HTTP handler",
					"error", err,
					"method", r.Method,
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
				)
				// Return 500 Internal Server Error
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// extractBearerToken extracts the Bearer token from the Authorization header
func extractBearerToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", fmt.Errorf("missing authorization header")
	}

	// Check for Bearer token
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", fmt.Errorf("invalid authorization header format, expected Bearer token")
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return "", fmt.Errorf("empty bearer token")
	}

	return token, nil
}

// getResourceMetadataURL returns the URL for the protected resource metadata endpoint
func (s *MattermostHTTPMCPServer) getResourceMetadataURL() string {
	baseURL := s.config.GetMMServerURL()
	if s.config.SiteURL != "" {
		baseURL = s.config.SiteURL
	}
	return fmt.Sprintf("%s/.well-known/oauth-protected-resource", baseURL)
}

// getAllowedOrigins returns the list of allowed origins for CORS and DNS rebinding protection
// Per MCP spec: "Servers MUST validate the Origin header on all incoming connections to prevent DNS rebinding attacks"
func (s *MattermostHTTPMCPServer) getAllowedOrigins() []string {
	// Use a map to avoid duplicates
	originsMap := make(map[string]struct{})

	// Add Mattermost server URL as allowed origin
	if mmURL := s.config.GetMMServerURL(); mmURL != "" {
		originsMap[mmURL] = struct{}{}
	}

	// Add configured site URL as allowed origin (for reverse proxy scenarios)
	if siteURL := s.config.SiteURL; siteURL != "" {
		originsMap[siteURL] = struct{}{}
	}

	// Handle localhost bindings - add all localhost variations for any localhost-like binding
	bindAddr := s.config.HTTPBindAddr
	isLocalhostBinding := bindAddr == "127.0.0.1" || bindAddr == "::1" ||
		bindAddr == "localhost" || bindAddr == "0.0.0.0"

	if isLocalhostBinding {
		// Add all localhost variations to support dual-stack scenarios
		originsMap[fmt.Sprintf("http://localhost:%d", s.config.HTTPPort)] = struct{}{}
		originsMap[fmt.Sprintf("http://127.0.0.1:%d", s.config.HTTPPort)] = struct{}{}
		originsMap[fmt.Sprintf("http://[::1]:%d", s.config.HTTPPort)] = struct{}{}
	}

	// Convert map to slice
	origins := make([]string, 0, len(originsMap))
	for origin := range originsMap {
		origins = append(origins, origin)
	}

	return origins
}

// validateOrigin validates the Origin header to prevent DNS rebinding attacks
func (s *MattermostHTTPMCPServer) validateOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin header - allow for direct API calls (non-browser requests)
		// This is common for server-to-server communication
		return true
	}

	allowedOrigins := s.getAllowedOrigins()

	// Parse the origin URL to normalize it
	originURL, err := url.Parse(origin)
	if err != nil {
		s.logger.Warn("invalid origin header",
			"origin", origin,
			"error", err)
		return false
	}

	// Normalize origin (remove default ports)
	normalizedOrigin := normalizeURL(originURL)

	// Check against allowed origins
	for _, allowedOrigin := range allowedOrigins {
		if allowedURL, err := url.Parse(allowedOrigin); err == nil {
			normalizedAllowed := normalizeURL(allowedURL)
			if normalizedOrigin == normalizedAllowed {
				return true
			}
		}
	}

	s.logger.Warn("origin not in allowed list",
		"origin", origin,
		"allowed_origins", allowedOrigins)
	return false
}

// normalizeURL normalizes a URL by removing default ports while preserving IPv6 brackets
func normalizeURL(u *url.URL) string {
	// Create a copy to avoid modifying the original
	normalized := &url.URL{
		Scheme: strings.ToLower(u.Scheme),
		Host:   u.Host,
		Path:   u.Path,
	}

	// Try to split host and port
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		// No port in URL - just lowercase the host
		// This preserves IPv6 brackets if present
		normalized.Host = strings.ToLower(u.Host)
	} else {
		// Check if it's a default port that should be removed
		isDefaultPort := (u.Scheme == "http" && port == "80") ||
			(u.Scheme == "https" && port == "443")

		if isDefaultPort {
			// Remove default port, but need to preserve IPv6 brackets
			// Parse the host to check if it's an IPv6 address
			if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() {
				// It's an IPv6 address - needs brackets
				normalized.Host = strings.ToLower("[" + host + "]")
			} else {
				// Regular hostname or IPv4
				normalized.Host = strings.ToLower(host)
			}
		} else {
			// Keep the port - JoinHostPort handles IPv6 brackets correctly
			normalized.Host = strings.ToLower(net.JoinHostPort(host, port))
		}
	}

	return normalized.String()
}

// securityMiddleware applies security headers and validation
func (s *MattermostHTTPMCPServer) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate Origin header to prevent DNS rebinding attacks
		if !s.validateOrigin(r) {
			s.logger.Warn("request blocked due to invalid origin",
				"origin", r.Header.Get("Origin"),
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent())

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error": {"code": -32001, "message": "Origin not allowed"}}`))
			return
		}

		// Add security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// CSP policy allows connections for MCP protocol while preventing most attacks
		w.Header().Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; frame-ancestors 'none'")

		// Add CORS headers for allowed origins (already validated above)
		if origin := r.Header.Get("Origin"); origin != "" {
			// Origin was already validated in validateOrigin(), safe to set CORS headers
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, Accept, Cache-Control, MCP-Protocol-Version, Mcp-Session-Id, Last-Event-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware creates a standard HTTP logging middleware
func (s *MattermostHTTPMCPServer) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create a response writer wrapper to capture status code
		recorder := &responseRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
			logger:         s.logger,
			requestPath:    r.URL.Path,
		}

		// Log the request
		s.logger.Debug("HTTP request received",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr)

		// Call the next handler
		next.ServeHTTP(recorder, r)

		// Log the response
		s.logger.Debug("HTTP request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.statusCode)
	})
}

// responseRecorder wraps http.ResponseWriter to capture the status code
type responseRecorder struct {
	http.ResponseWriter
	statusCode    int
	headerWritten bool
	logger        loggerlib.Logger
	requestPath   string
}

// Flush implements http.Flusher for SSE streaming support
func (r *responseRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	if r.headerWritten {
		return
	}

	r.statusCode = statusCode
	r.headerWritten = true
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	return r.ResponseWriter.Write(data)
}

// requireAuth creates HTTP middleware that requires OAuth authentication
func (s *MattermostHTTPMCPServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract Bearer token from Authorization header
		token, err := extractBearerToken(r)
		if err != nil {
			s.logger.Warn("failed to extract bearer token for middleware",
				"path", r.URL.Path,
				"error", err)

			// Return 401 Unauthorized with WWW-Authenticate header (RFC 9728)
			resourceMetadataURL := s.getResourceMetadataURL()
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, resourceMetadataURL))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Add token to context and validate it
		ctx := r.Context()
		ctx = context.WithValue(ctx, auth.AuthTokenContextKey, token)
		if err := s.authProvider.ValidateAuth(ctx); err != nil {
			s.logger.Warn("authentication failed for MCP endpoint",
				"path", r.URL.Path,
				"error", err)

			// Return 401 Unauthorized with WWW-Authenticate header (RFC 9728)
			resourceMetadataURL := s.getResourceMetadataURL()
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, resourceMetadataURL))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": {"code": -32600, "message": "Authentication required"}}`))
			return
		}
		r = r.WithContext(ctx)
		next(w, r)
	}
}

// setupRoutes sets up all HTTP routes for the MCP server on the provided mux
func (s *MattermostHTTPMCPServer) setupRoutes(httpMux *http.ServeMux) {
	// OAuth 2.0 Protected Resource Metadata endpoint (RFC 9728) - no auth required
	httpMux.HandleFunc("/.well-known/oauth-protected-resource", s.handleProtectedResourceMetadata)

	// MCP endpoint for streamable HTTP communication - requires auth
	httpMux.HandleFunc("/mcp", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		s.streamableHandler.ServeHTTP(w, r)
	}))

	// SSE endpoint (backwards compatibility) - requires auth
	httpMux.HandleFunc("/sse", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		s.sseHandler.ServeHTTP(w, r)
	}))

	// Message endpoint (backwards compatibility) - requires auth
	httpMux.HandleFunc("/message", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		s.sseHandler.ServeHTTP(w, r)
	}))

	// Default 404 handler for any other unmatched paths
	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.logger.Debug("Request to unmatched path", "path", r.URL.Path)
		http.NotFound(w, r)
	})
}
