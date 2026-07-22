// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ErrServerNotConnected is returned when the user has no live session to the
// requested MCP server and none could be established without user action.
var ErrServerNotConnected = errors.New("no connected MCP client for server")

// InvalidAppResourceError is returned when a ui:// resource is served with a
// MIME type other than text/html;profile=mcp-app, or with no content.
type InvalidAppResourceError struct {
	URI      string
	MIMEType string
}

func (e *InvalidAppResourceError) Error() string {
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

	session := c.currentSession()
	if session == nil {
		err := fmt.Errorf("MCP client not connected")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		if !errors.Is(err, mcp.ErrConnectionClosed) {
			err = fmt.Errorf("failed to read resource %s on server %s: %w", uri, c.config.Name, err)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, err
		}
		if reconnectErr := c.reconnect(ctx, session); reconnectErr != nil {
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
		if err != nil {
			err = fmt.Errorf("failed to read resource %s on server %s after reconnecting: %w", uri, c.config.Name, err)
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
		return nil, &InvalidAppResourceError{URI: uri}
	}

	var contents *mcp.ResourceContents
	for _, c := range result.Contents {
		if c != nil && c.URI == uri {
			contents = c
			break
		}
	}
	if contents == nil {
		contents = result.Contents[0]
	}
	if contents == nil {
		return nil, &InvalidAppResourceError{URI: uri}
	}

	if !IsUIResourceMIMEType(contents.MIMEType) {
		return nil, &InvalidAppResourceError{URI: uri, MIMEType: contents.MIMEType}
	}

	html := contents.Text
	if html == "" && len(contents.Blob) > 0 {
		html = string(contents.Blob)
	}
	if html == "" {
		return nil, &InvalidAppResourceError{URI: uri, MIMEType: contents.MIMEType}
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
			return entry.client.ReadAppResource(ctx, uri)
		}
	}
	return nil, ErrServerNotConnected
}

// ReadUserAppResource fetches a ui:// resource for a user from the MCP server
// identified by serverOrigin, establishing the user's client set on demand
// (same connect strategy as GetToolsForUser: cached remote clients, embedded
// session, plugin servers). Returns *OAuthNeededError when the user must
// complete OAuth with that server first, and ErrServerNotConnected when the
// server is unreachable for this user for any other reason.
func (m *ClientManager) ReadUserAppResource(ctx context.Context, userID, serverOrigin, uri string) (*AppResource, error) {
	userClient, mcpErrors := m.getClientForUser(ctx, userID)
	normalized := llm.NormalizeMCPServerOrigin(serverOrigin)

	if normalized == llm.NormalizeMCPServerOrigin(EmbeddedClientKey) {
		m.connectEmbeddedForUser(ctx, userClient, userID)
	}

	if strings.HasPrefix(normalized, "plugin://") {
		for _, cfg := range m.snapshotEnabledPluginServers() {
			if llm.NormalizeMCPServerOrigin("plugin://"+cfg.PluginID) == normalized {
				if connectErr := userClient.ConnectToPluginServer(ctx, cfg, m.sourcePluginAPI); connectErr != nil {
					m.log.Error("Failed to connect to plugin MCP server for app resource", "userID", userID, "pluginID", cfg.PluginID, "error", connectErr)
				}
				break
			}
		}
	}

	res, err := userClient.ReadAppResource(ctx, serverOrigin, uri)
	if err == nil {
		return res, nil
	}
	if !errors.Is(err, ErrServerNotConnected) {
		return nil, err
	}

	if mcpErrors != nil {
		for _, ae := range mcpErrors.ToolAuthErrors {
			if llm.NormalizeMCPServerOrigin(ae.ServerOrigin) == normalized {
				return nil, NewOAuthNeededError(ae.AuthURL)
			}
		}
	}

	if m.oauthManager != nil {
		for _, server := range m.config.Servers {
			if llm.NormalizeMCPServerOrigin(server.BaseURL) != normalized {
				continue
			}
			state, loadErr := m.oauthManager.LoadAuthNeededState(userID, server.Name)
			if loadErr != nil {
				m.log.Debug("Failed to load OAuth-needed state for app resource", "userID", userID, "server", server.Name, "error", loadErr)
				break
			}
			if state != nil && state.AuthURL != "" {
				return nil, NewOAuthNeededError(state.AuthURL)
			}
			break
		}
	}

	if mcpErrors != nil && len(mcpErrors.Errors) > 0 {
		return nil, fmt.Errorf("%w: %v", ErrServerNotConnected, mcpErrors.Errors)
	}
	return nil, ErrServerNotConnected
}
