// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"errors"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

var (
	ErrCatalogRemoteOwnerRequired = errors.New("catalog request remote pool owner is required")
	ErrCatalogInvokerRequired     = errors.New("catalog request invoking user ID is required")
)

const (
	ServerKindRemote   = "remote"
	ServerKindEmbedded = "embedded"
	ServerKindPlugin   = "plugin"
)

// CatalogRequest identifies whose MCP tool catalog to build. GetTools
// validates it fail-closed, so a zero value never yields tools.
type CatalogRequest struct {
	// RemoteOwnerID keys the pooled remote-server connections: the user in
	// user mode, the agent's bot in service-account mode.
	RemoteOwnerID string
	// InvokingUserID is who embedded and plugin servers connect as, in both modes.
	InvokingUserID string
	// ServiceAccount selects admin SA headers (and fail-closed exclusion of
	// remotes without them) instead of per-user OAuth for remote servers.
	ServiceAccount bool
}

// UserCatalogRequest is the per-user catalog: remotes and embedded/plugin all
// authenticate as userID.
func UserCatalogRequest(userID string) CatalogRequest {
	return CatalogRequest{RemoteOwnerID: userID, InvokingUserID: userID}
}

// ServiceAccountCatalogRequest is the service-account catalog: remotes pooled
// by remoteOwnerID with admin SA headers, embedded/plugin connected as
// invokingUserID.
func ServiceAccountCatalogRequest(remoteOwnerID, invokingUserID string) CatalogRequest {
	return CatalogRequest{RemoteOwnerID: remoteOwnerID, InvokingUserID: invokingUserID, ServiceAccount: true}
}

func (r CatalogRequest) validate() error {
	if r.RemoteOwnerID == "" {
		return ErrCatalogRemoteOwnerRequired
	}
	if r.InvokingUserID == "" {
		return ErrCatalogInvokerRequired
	}
	return nil
}

func (r CatalogRequest) remoteKey() clientKey {
	kind := clientKindUserRemote
	if r.ServiceAccount {
		kind = clientKindSARemote
	}
	return clientKey{userID: r.RemoteOwnerID, kind: kind}
}

// ServerKind reports the wire kind for an MCP server origin: remote, embedded,
// or plugin. An empty origin maps to remote; built-in (non-MCP) tools never
// reach the wire response this feeds.
func ServerKind(origin string) string {
	origin = llm.NormalizeMCPServerOrigin(origin)
	switch {
	case origin == EmbeddedClientKey:
		return ServerKindEmbedded
	case strings.HasPrefix(origin, "plugin://"):
		return ServerKindPlugin
	default:
		return ServerKindRemote
	}
}
