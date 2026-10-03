// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	// MaxAppResourceBytes is the maximum accepted size of a ui:// app HTML body.
	MaxAppResourceBytes = 4 << 20 // 4 MiB
)

// ErrServerNotConnected is returned when the user has no live session to the
// requested MCP server and none could be established without user action.
var ErrServerNotConnected = errors.New("no connected MCP client for server")

// ErrServerNotConfigured is returned when the normalized origin matches no
// currently enabled remote, plugin, or embedded MCP server.
var ErrServerNotConfigured = errors.New("MCP server is not configured")

// ErrServerAccessDenied is returned when access policy denies the user the
// MCP server that owns the requested resource.
var ErrServerAccessDenied = errors.New("MCP server access denied by policy")

// InvalidAppResourceError is returned when a ui:// resource is served with a
// MIME type other than text/html;profile=mcp-app, or with no content.
type InvalidAppResourceError struct {
	URI      string
	MIMEType string
	Reason   string
}

func (e *InvalidAppResourceError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("resource %s is not a valid MCP App resource: %s", e.URI, e.Reason)
	}
	return fmt.Sprintf("resource %s is not a valid MCP App resource (mimeType %q)", e.URI, e.MIMEType)
}

// AppResource is the fetched content of a ui:// MCP App resource.
type AppResource struct {
	// URI echoes the resource URI that was read.
	URI string `json:"uri"`
	// MIMEType is the validated MIME type (always the mcp-app HTML profile).
	MIMEType string `json:"mime_type"`
	// HTML is the raw app HTML (decoded from text or blob content).
	HTML string `json:"html"`
	// UIMeta is the resource's _meta.ui (csp, permissions, prefersBorder…).
	// Nil when the server declared none; the host then applies the spec's
	// restrictive CSP default (Phase 1b).
	UIMeta *AppResourceUIMeta `json:"ui_meta,omitempty"`
}

// ReadAppResource fetches a ui:// resource from this MCP server via
// resources/read, validates the MCP Apps MIME profile, and returns the HTML
// plus the resource's _meta.ui. It reuses the client's existing session and
// transparently reconnects once on a closed connection (same policy as
// CallToolWithMetadata).
func (c *Client) ReadAppResource(ctx context.Context, uri string) (*AppResource, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "mcp read resource",
		trace.WithAttributes(
			telemetry.MCPServer.String(c.config.Name),
		),
	)
	defer span.End()

	if err := validateUIResourceURI(uri); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	session := c.currentSession()
	if session == nil {
		err := fmt.Errorf("MCP client not connected")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		if errors.Is(err, mcp.ErrConnectionClosed) {
			if reconnectErr := c.reconnect(ctx, session); reconnectErr != nil {
				if oauthErr := c.oauthNeededError(reconnectErr); oauthErr != nil {
					span.RecordError(oauthErr)
					span.SetStatus(codes.Error, oauthErr.Error())
					return nil, oauthErr
				}
				span.RecordError(reconnectErr)
				span.SetStatus(codes.Error, reconnectErr.Error())
				return nil, reconnectErr
			}
			session = c.currentSession()
			if session == nil {
				err = fmt.Errorf("MCP client not connected after reconnecting")
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return nil, err
			}
			result, err = session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		}
		if err != nil {
			if oauthErr := c.oauthNeededError(err); oauthErr != nil {
				span.RecordError(oauthErr)
				span.SetStatus(codes.Error, oauthErr.Error())
				return nil, oauthErr
			}
			err = fmt.Errorf("failed to read resource %s on server %s: %w", uri, c.config.Name, err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
	}

	res, err := appResourceFromReadResult(uri, result)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return res, nil
}

func appResourceFromReadResult(uri string, result *mcp.ReadResourceResult) (*AppResource, error) {
	if result == nil || len(result.Contents) == 0 {
		return nil, &InvalidAppResourceError{URI: uri, Reason: "empty contents"}
	}

	var contents *mcp.ResourceContents
	for _, c := range result.Contents {
		if c != nil && c.URI == uri {
			contents = c
			break
		}
	}
	if contents == nil {
		return nil, &InvalidAppResourceError{URI: uri, Reason: "no content with matching URI"}
	}

	if !IsUIResourceMIMEType(contents.MIMEType) {
		return nil, &InvalidAppResourceError{URI: uri, MIMEType: contents.MIMEType}
	}

	html := contents.Text
	if html == "" && len(contents.Blob) > 0 {
		if !utf8.Valid(contents.Blob) {
			return nil, &InvalidAppResourceError{URI: uri, Reason: "invalid UTF-8 blob"}
		}
		html = string(contents.Blob)
	}
	if html == "" {
		return nil, &InvalidAppResourceError{URI: uri, MIMEType: contents.MIMEType, Reason: "empty body"}
	}
	if !utf8.ValidString(html) {
		return nil, &InvalidAppResourceError{URI: uri, Reason: "invalid UTF-8 text"}
	}
	if len(html) > MaxAppResourceBytes {
		return nil, &InvalidAppResourceError{URI: uri, Reason: "resource exceeds size limit"}
	}

	return &AppResource{
		URI:      uri,
		MIMEType: UIResourceMIMEType,
		HTML:     html,
		UIMeta:   parseResourceUIMeta(contents.Meta),
	}, nil
}

// ReadAppResource routes a ui:// resource read to the user's client for the
// given server origin. Returns ErrServerNotConnected when no client for that
// origin exists in this user's client set.
func (c *UserClients) ReadAppResource(ctx context.Context, serverOrigin, uri string) (*AppResource, error) {
	normalized := llm.NormalizeMCPServerOrigin(serverOrigin)
	for _, entry := range c.snapshotClients() {
		if llm.NormalizeMCPServerOrigin(entry.client.config.BaseURL) == normalized {
			res, err := entry.client.ReadAppResource(ctx, uri)
			if err != nil {
				c.rememberOAuthNeededForToolCall(entry.client, err)
			}
			return res, err
		}
	}
	return nil, ErrServerNotConnected
}

// narrowToOrigin keeps only the eligible server whose normalized origin is
// normalized, so a targeted read never plans connects to unrelated servers.
func (s eligibleServers) narrowToOrigin(normalized string) (eligibleServers, bool) {
	narrowed := eligibleServers{origins: make(map[string]bool)}
	for _, server := range s.remote {
		if llm.NormalizeMCPServerOrigin(server.BaseURL) == normalized {
			narrowed.remote = append(narrowed.remote, server)
			narrowed.origins[normalized] = true
			return narrowed, true
		}
	}
	if s.embedded && llm.NormalizeMCPServerOrigin(EmbeddedClientKey) == normalized {
		narrowed.embedded = true
		narrowed.origins[EmbeddedClientKey] = true
		return narrowed, true
	}
	for _, cfg := range s.plugins {
		origin := pluginServerOriginKey(cfg.PluginID)
		if llm.NormalizeMCPServerOrigin(origin) == normalized {
			narrowed.plugins = append(narrowed.plugins, cfg)
			narrowed.origins[origin] = true
			return narrowed, true
		}
	}
	return narrowed, false
}

// ReadUserAppResource fetches a ui:// resource for a user from the MCP server
// identified by serverOrigin, authenticating as that user. Eligibility follows
// the user-mode catalog (enabled, licensed, conflict-free, and allowed by
// access policy), and only that one origin is connected (lazy, targeted) —
// unlike catalog construction, it does not fan out to every configured server.
// Returns ErrServerNotConfigured when the origin is unknown or ineligible,
// ErrServerAccessDenied when access policy denies it, *OAuthNeededError when the
// user must complete OAuth first, and ErrServerNotConnected when the server is
// otherwise unreachable.
func (m *ClientManager) ReadUserAppResource(ctx context.Context, userID, serverOrigin, uri string) (*AppResource, error) {
	req := UserCatalogRequest(userID)
	if err := req.validate(); err != nil {
		return nil, err
	}
	normalized := llm.NormalizeMCPServerOrigin(serverOrigin)
	if normalized == "" {
		return nil, ErrServerNotConfigured
	}

	m.lifecycleMu.RLock()
	if m.closed {
		m.lifecycleMu.RUnlock()
		return nil, ErrServerNotConnected
	}
	cfg := m.config
	embeddedClient := m.embeddedClient
	plugins := m.snapshotEnabledPluginServers()
	deniedOrigins := m.deniedMCPServerOrigins(ctx, userID, cfg, embeddedClient, plugins)
	if deniedOrigins[normalized] {
		m.lifecycleMu.RUnlock()
		return nil, ErrServerAccessDenied
	}
	servers := m.resolveEligibleServers(cfg, embeddedClient, plugins, ToolSelection{}, deniedOrigins, false)
	target, ok := servers.narrowToOrigin(normalized)
	if !ok {
		m.lifecycleMu.RUnlock()
		return nil, ErrServerNotConfigured
	}

	key := req.remoteKey()
	if len(target.remote) == 0 {
		key = clientKey{userID: userID, kind: clientKindLocal}
	}
	userClients := m.getOrCreateClient(key)
	if userClients == nil {
		m.lifecycleMu.RUnlock()
		return nil, ErrServerNotConnected
	}

	var sessionID string
	var sessionErr error
	if target.embedded {
		sessionID, _, sessionErr = m.ensureEmbeddedSessionID(userID)
	}
	plans, discarded := userClients.planConnections(
		m.buildConnectTasks(ctx, userClients, target, embeddedClient, false, sessionID, sessionErr),
	)
	m.lifecycleMu.RUnlock()
	closeDetachedClients(m.log, discarded)

	if connectErr := firstConnectError(userClients.executeConnections(ctx, plans)); connectErr != nil {
		if oauthErr, isOAuth := errors.AsType[*OAuthNeededError](connectErr); isOAuth {
			return nil, oauthErr
		}
		m.log.Debug("Failed to connect to MCP server for app resource",
			"userID", userID, "serverOrigin", serverOrigin, "error", connectErr)
		return nil, fmt.Errorf("%w: %v", ErrServerNotConnected, connectErr)
	}

	res, err := userClients.ReadAppResource(ctx, serverOrigin, uri)
	if err == nil {
		return res, nil
	}
	if oauthErr, isOAuth := errors.AsType[*OAuthNeededError](err); isOAuth {
		return nil, oauthErr
	}
	if errors.Is(err, ErrServerNotConnected) && m.oauthManager != nil && len(target.remote) > 0 {
		serverName := target.remote[0].Name
		state, loadErr := m.oauthManager.LoadAuthNeededState(userID, serverName)
		if loadErr != nil {
			m.log.Debug("Failed to load OAuth-needed state for app resource",
				"userID", userID, "server", serverName, "error", loadErr)
		} else if state != nil && state.AuthURL != "" {
			return nil, NewOAuthNeededError(state.AuthURL)
		}
	}
	return nil, err
}
