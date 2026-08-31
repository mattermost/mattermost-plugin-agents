// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"errors"
	"strings"
)

var (
	ErrCatalogUserIDRequired      = errors.New("catalog request user ID is required")
	ErrCatalogRemoteOwnerRequired = errors.New("catalog request remote pool owner is required")
	ErrCatalogInvokerRequired     = errors.New("catalog request invoking user ID is required")
)

const (
	ServerKindRemote   = "remote"
	ServerKindEmbedded = "embedded"
	ServerKindPlugin   = "plugin"
)

// CatalogRequest is a validated request to build an MCP tool catalog.
// Construct it with NewUserCatalogRequest, NewServiceAccountCatalogRequest,
// or NewServiceAccountPreviewRequest — do not build one by hand.
type CatalogRequest struct {
	remoteOwnerID  string
	invokingUserID string
	serviceAccount bool
}

// NewUserCatalogRequest builds the per-user catalog: remotes and
// embedded/plugin all authenticate as userID.
func NewUserCatalogRequest(userID string) (CatalogRequest, error) {
	if userID == "" {
		return CatalogRequest{}, ErrCatalogUserIDRequired
	}
	return CatalogRequest{
		remoteOwnerID:  userID,
		invokingUserID: userID,
	}, nil
}

// NewServiceAccountCatalogRequest builds a service-account catalog:
// remotes pooled by remoteOwnerID with admin SA headers, embedded/plugin
// connected as invokingUserID.
func NewServiceAccountCatalogRequest(remoteOwnerID, invokingUserID string) (CatalogRequest, error) {
	if remoteOwnerID == "" {
		return CatalogRequest{}, ErrCatalogRemoteOwnerRequired
	}
	if invokingUserID == "" {
		return CatalogRequest{}, ErrCatalogInvokerRequired
	}
	return CatalogRequest{
		remoteOwnerID:  remoteOwnerID,
		invokingUserID: invokingUserID,
		serviceAccount: true,
	}, nil
}

// NewServiceAccountPreviewRequest builds the unsaved-agent SA preview:
// the viewer is both the remote-pool owner and the invoking user.
func NewServiceAccountPreviewRequest(viewerUserID string) (CatalogRequest, error) {
	return NewServiceAccountCatalogRequest(viewerUserID, viewerUserID)
}

func (r CatalogRequest) RemoteOwnerID() string  { return r.remoteOwnerID }
func (r CatalogRequest) InvokingUserID() string { return r.invokingUserID }
func (r CatalogRequest) UsesServiceAccount() bool {
	return r.serviceAccount
}

func (r CatalogRequest) validate() error {
	if r.remoteOwnerID == "" {
		return ErrCatalogRemoteOwnerRequired
	}
	if r.invokingUserID == "" {
		return ErrCatalogInvokerRequired
	}
	return nil
}

func (r CatalogRequest) remoteKey() clientKey {
	kind := clientKindUserRemote
	if r.serviceAccount {
		kind = clientKindSARemote
	}
	return clientKey{userID: r.remoteOwnerID, kind: kind}
}

func (r CatalogRequest) localKey() clientKey {
	return clientKey{userID: r.invokingUserID, kind: clientKindLocal}
}

// ServerKind reports the wire kind for an MCP server origin: remote, embedded, or plugin.
func ServerKind(origin string) string {
	switch {
	case origin == EmbeddedClientKey:
		return ServerKindEmbedded
	case strings.HasPrefix(origin, "plugin://"):
		return ServerKindPlugin
	default:
		return ServerKindRemote
	}
}
