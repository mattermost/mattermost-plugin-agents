// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package auth

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// Context keys for passing data through context
type ContextKey string

const (
	// AuthTokenContextKey is used to store the validated auth token in context
	AuthTokenContextKey ContextKey = "auth_token"
	// SessionIDContextKey is used to store the session ID in context
	SessionIDContextKey ContextKey = "session_id"
	// TokenResolverContextKey is used to store a function that resolves sessionID to token
	TokenResolverContextKey ContextKey = "token_resolver"
)

// TokenResolver is a function that resolves a sessionID to a token
type TokenResolver func(sessionID string) (string, error)

// AuthenticationProvider handles authentication for MCP requests
type AuthenticationProvider interface {
	ValidateAuth(ctx context.Context) error

	// GetAuthenticatedMattermostClient returns an authenticated Mattermost client
	GetAuthenticatedMattermostClient(ctx context.Context) (*model.Client4, error)
}

// UserIdentityProvider can supply the authenticated Mattermost user for the current context.
// Implementations may use cached validation results to avoid additional network calls.
type UserIdentityProvider interface {
	AuthenticationProvider

	// GetAuthenticatedUser returns the authenticated Mattermost user for the current context
	GetAuthenticatedUser(ctx context.Context) (*model.User, error)
}
