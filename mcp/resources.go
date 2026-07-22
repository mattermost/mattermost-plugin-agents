// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

type resolvedMCPOrigin struct {
	kind       string // "remote", "embedded", "plugin"
	serverID   string
	remote     ServerConfig
	plugin     PluginServerConfig
	normalized string
}

// resolveEnabledOrigin maps a server origin to an enabled remote/plugin/embedded
// target. Returns ErrServerNotConfigured when no enabled match exists.
func (m *ClientManager) resolveEnabledOrigin(serverOrigin string) (*resolvedMCPOrigin, error) {
	normalized := llm.NormalizeMCPServerOrigin(serverOrigin)
	if normalized == "" {
		return nil, ErrServerNotConfigured
	}

	if normalized == llm.NormalizeMCPServerOrigin(EmbeddedClientKey) {
		if m.embeddedClient == nil || !m.config.EmbeddedServer.Enabled {
			return nil, ErrServerNotConfigured
		}
		return &resolvedMCPOrigin{kind: "embedded", serverID: EmbeddedClientKey, normalized: normalized}, nil
	}

	if strings.HasPrefix(normalized, "plugin://") {
		for _, cfg := range m.snapshotEnabledPluginServers() {
			if llm.NormalizeMCPServerOrigin("plugin://"+cfg.PluginID) == normalized {
				return &resolvedMCPOrigin{kind: "plugin", serverID: "plugin://" + cfg.PluginID, plugin: cfg, normalized: normalized}, nil
			}
		}
		return nil, ErrServerNotConfigured
	}

	for _, server := range m.config.Servers {
		if !server.Enabled || server.BaseURL == "" {
			continue
		}
		if llm.NormalizeMCPServerOrigin(server.BaseURL) == normalized {
			return &resolvedMCPOrigin{kind: "remote", serverID: server.Name, remote: server, normalized: normalized}, nil
		}
	}
	return nil, ErrServerNotConfigured
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

// ReadUserAppResource fetches a ui:// resource for a user from the MCP server
// identified by serverOrigin. It connects only that enabled origin into the
// per-user client set (lazy, targeted) — unlike GetToolsForUser, it does not
// fan out to every configured server. Returns ErrServerNotConfigured when the
// origin is unknown or disabled, *OAuthNeededError when the user must complete
// OAuth first, and ErrServerNotConnected when the server is otherwise unreachable.
func (m *ClientManager) ReadUserAppResource(ctx context.Context, userID, serverOrigin, uri string) (*AppResource, error) {
	target, err := m.resolveEnabledOrigin(serverOrigin)
	if err != nil {
		return nil, err
	}

	userClient := m.getOrCreateUserClientsShell(userID)

	switch target.kind {
	case "embedded":
		m.connectEmbeddedForUser(ctx, userClient, userID)
	case "plugin":
		if connectErr := userClient.ConnectToPluginServer(ctx, target.plugin, m.sourcePluginAPI); connectErr != nil {
			m.log.Error("Failed to connect to plugin MCP server for app resource",
				"userID", userID, "pluginID", target.plugin.PluginID, "error", connectErr)
			return nil, fmt.Errorf("%w: %v", ErrServerNotConnected, connectErr)
		}
	case "remote":
		if connectErr := userClient.ConnectToRemoteServer(cacheableContext(ctx), target.remote, false); connectErr != nil {
			var oauthErr *OAuthNeededError
			if errors.As(connectErr, &oauthErr) {
				return nil, oauthErr
			}
			m.log.Error("Failed to connect to MCP server for app resource",
				"userID", userID, "server", target.remote.Name, "error", connectErr)
			return nil, fmt.Errorf("%w: %v", ErrServerNotConnected, connectErr)
		}
	}

	res, err := userClient.ReadAppResource(ctx, serverOrigin, uri)
	if err == nil {
		return res, nil
	}
	var oauthErr *OAuthNeededError
	if errors.As(err, &oauthErr) {
		return nil, oauthErr
	}
	if errors.Is(err, ErrServerNotConnected) {
		if m.oauthManager != nil && target.kind == "remote" {
			state, loadErr := m.oauthManager.LoadAuthNeededState(userID, target.remote.Name)
			if loadErr != nil {
				m.log.Debug("Failed to load OAuth-needed state for app resource",
					"userID", userID, "server", target.remote.Name, "error", loadErr)
			} else if state != nil && state.AuthURL != "" {
				return nil, NewOAuthNeededError(state.AuthURL)
			}
		}
		return nil, err
	}
	return nil, err
}
