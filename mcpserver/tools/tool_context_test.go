// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// Each embedded tool call builds its context from the session; one session
// validation (GET /users/me) must yield both the client and the user.
func TestCreateMCPToolContextValidatesSessionOnce(t *testing.T) {
	userID := model.NewId()
	var getMeCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/users/me" {
			http.NotFound(w, r)
			return
		}
		getMeCalls.Add(1)
		_ = json.NewEncoder(w).Encode(&model.User{Id: userID, Username: "user"})
	}))
	defer server.Close()

	p := newTestProvider(t, server.URL)
	p.authProvider = auth.NewSessionAuthenticationProvider(server.URL, "", &testLogger{t: t})

	ctx := context.WithValue(context.Background(), auth.SessionIDContextKey, "session-id")
	ctx = context.WithValue(ctx, auth.TokenResolverContextKey, auth.TokenResolver(func(string) (string, error) {
		return "token", nil
	}))

	mcpContext, err := p.createMCPToolContext(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, mcpContext.Client)
	require.Equal(t, userID, mcpContext.UserID)
	require.Equal(t, int32(1), getMeCalls.Load())
}
