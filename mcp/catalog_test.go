// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogRequest(t *testing.T) {
	t.Parallel()

	t.Run("user catalog authenticates remotes and locals as the user", func(t *testing.T) {
		req := UserCatalogRequest("user-1")
		require.NoError(t, req.validate())
		require.False(t, req.ServiceAccount)
		require.Equal(t, clientKey{userID: "user-1", kind: clientKindUserRemote}, req.remoteKey())
		require.Equal(t, "user-1", req.InvokingUserID)
	})

	t.Run("SA catalog splits remote owner from invoker", func(t *testing.T) {
		req := ServiceAccountCatalogRequest("bot-1", "user-a")
		require.NoError(t, req.validate())
		require.True(t, req.ServiceAccount)
		require.Equal(t, clientKey{userID: "bot-1", kind: clientKindSARemote}, req.remoteKey())
		require.Equal(t, "user-a", req.InvokingUserID)
	})
}

// GetTools must fail closed on invalid requests instead of building a catalog.
func TestGetToolsRejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	m := &ClientManager{log: newTestLogService()}

	tests := []struct {
		name    string
		req     CatalogRequest
		wantErr error
	}{
		{name: "zero value", req: CatalogRequest{}, wantErr: ErrCatalogRemoteOwnerRequired},
		{name: "empty user", req: UserCatalogRequest(""), wantErr: ErrCatalogRemoteOwnerRequired},
		{name: "SA empty remote owner", req: ServiceAccountCatalogRequest("", "user-1"), wantErr: ErrCatalogRemoteOwnerRequired},
		{name: "SA empty invoker", req: ServiceAccountCatalogRequest("bot-1", ""), wantErr: ErrCatalogInvokerRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, errs := m.GetTools(context.Background(), tt.req)
			require.Empty(t, tools)
			require.NotNil(t, errs)
			require.Len(t, errs.Errors, 1)
			require.ErrorIs(t, errs.Errors[0], tt.wantErr)
		})
	}
}

func TestServerKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		origin string
		want   string
	}{
		{origin: EmbeddedClientKey, want: ServerKindEmbedded},
		{origin: "plugin://com.example.mcp", want: ServerKindPlugin},
		{origin: "https://mcp.example.com", want: ServerKindRemote},
		{origin: "https://mcp.example.com/", want: ServerKindRemote},
		{origin: "", want: ServerKindRemote},
	}

	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			require.Equal(t, tt.want, ServerKind(tt.origin))
		})
	}
}

func TestClientBagKindEnforced(t *testing.T) {
	t.Parallel()

	log := newTestLogService()
	cache := newTestToolsCache()

	t.Run("SA remotes reject embedded and plugin connect", func(t *testing.T) {
		bag := newRemoteClients("bot-1", clientKindSARemote, log, nil, http.DefaultClient, cache)
		err := bag.ConnectToEmbeddedServerIfAvailable(context.Background(), "sess", nil, EmbeddedServerConfig{Enabled: true})
		require.Error(t, err)
		require.Empty(t, bag.snapshotClients())

		err = bag.ConnectToPluginServer(context.Background(), PluginServerConfig{PluginID: "com.example.mcp"}, nil)
		require.Error(t, err)
		require.Empty(t, bag.snapshotClients())
	})

	t.Run("user remotes reject embedded and plugin connect", func(t *testing.T) {
		bag := newRemoteClients("user-1", clientKindUserRemote, log, nil, http.DefaultClient, cache)
		err := bag.ConnectToEmbeddedServerIfAvailable(context.Background(), "sess", nil, EmbeddedServerConfig{Enabled: true})
		require.Error(t, err)
		err = bag.ConnectToPluginServer(context.Background(), PluginServerConfig{PluginID: "com.example.mcp"}, nil)
		require.Error(t, err)
	})

	t.Run("local bag rejects remote connect", func(t *testing.T) {
		bag := newLocalClients("user-1", log, http.DefaultClient, cache)
		errs := bag.ConnectToRemoteServers(context.Background(), []ServerConfig{{
			Name:    "remote",
			BaseURL: "https://mcp.example.com",
			Enabled: true,
		}}, false)
		require.NotNil(t, errs)
		require.NotEmpty(t, errs.Errors)
		require.Empty(t, bag.snapshotClients())
	})
}
