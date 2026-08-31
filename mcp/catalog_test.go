// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCatalogRequestConstructors(t *testing.T) {
	t.Parallel()

	t.Run("user catalog rejects empty user", func(t *testing.T) {
		_, err := NewUserCatalogRequest("")
		require.ErrorIs(t, err, ErrCatalogUserIDRequired)
	})

	t.Run("SA catalog rejects empty remote owner", func(t *testing.T) {
		_, err := NewServiceAccountCatalogRequest("", "user-1")
		require.ErrorIs(t, err, ErrCatalogRemoteOwnerRequired)
	})

	t.Run("SA catalog rejects empty invoker", func(t *testing.T) {
		_, err := NewServiceAccountCatalogRequest("bot-1", "")
		require.ErrorIs(t, err, ErrCatalogInvokerRequired)
	})

	t.Run("user catalog authenticates remotes and locals as the user", func(t *testing.T) {
		req, err := NewUserCatalogRequest("user-1")
		require.NoError(t, err)
		require.Equal(t, "user-1", req.RemoteOwnerID())
		require.Equal(t, "user-1", req.InvokingUserID())
		require.False(t, req.UsesServiceAccount())
		require.Equal(t, clientKey{userID: "user-1", kind: clientKindUserRemote}, req.remoteKey())
		require.Equal(t, clientKey{userID: "user-1", kind: clientKindLocal}, req.localKey())
	})

	t.Run("SA catalog splits remote owner from invoker", func(t *testing.T) {
		req, err := NewServiceAccountCatalogRequest("bot-1", "user-a")
		require.NoError(t, err)
		require.Equal(t, "bot-1", req.RemoteOwnerID())
		require.Equal(t, "user-a", req.InvokingUserID())
		require.True(t, req.UsesServiceAccount())
		require.Equal(t, clientKey{userID: "bot-1", kind: clientKindSARemote}, req.remoteKey())
		require.Equal(t, clientKey{userID: "user-a", kind: clientKindLocal}, req.localKey())
	})

	t.Run("SA preview uses the viewer as remote owner and invoker", func(t *testing.T) {
		req, err := NewServiceAccountPreviewRequest("viewer-1")
		require.NoError(t, err)
		require.Equal(t, "viewer-1", req.RemoteOwnerID())
		require.Equal(t, "viewer-1", req.InvokingUserID())
		require.True(t, req.UsesServiceAccount())
	})
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
		bag := newRemoteClients("bot-1", true, log, nil, http.DefaultClient, cache)
		err := bag.ConnectToEmbeddedServerIfAvailable(context.Background(), "sess", nil, EmbeddedServerConfig{Enabled: true})
		require.Error(t, err)
		require.Empty(t, bag.snapshotClients())

		err = bag.ConnectToPluginServer(context.Background(), PluginServerConfig{PluginID: "com.example.mcp"}, nil)
		require.Error(t, err)
		require.Empty(t, bag.snapshotClients())
	})

	t.Run("user remotes reject embedded and plugin connect", func(t *testing.T) {
		bag := newRemoteClients("user-1", false, log, nil, http.DefaultClient, cache)
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

func TestCreateAndStoreUserClientRejectsNonRemoteKind(t *testing.T) {
	pluginAPI := newTestPluginAPIForEmbeddedManager("user-1", "session-1")
	manager := &ClientManager{
		log:      pluginAPI.Log,
		clients:  make(map[clientKey]*UserClients),
		activity: make(map[clientKey]time.Time),
	}

	_, errs := manager.createAndStoreUserClient(context.Background(), clientKey{userID: "user-1", kind: clientKindLocal}, false)
	require.NotNil(t, errs)
	require.NotEmpty(t, errs.Errors)
	require.Empty(t, manager.clients)
}
