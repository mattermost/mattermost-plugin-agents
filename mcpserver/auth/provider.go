// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package auth

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
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
	// BeforeHookResolverContextKey is used to store a function that resolves before-hook keys
	BeforeHookResolverContextKey ContextKey = "before_hook_resolver"
	// UserIDContextKey is used to store the user ID in context for HTTP callbacks
	UserIDContextKey ContextKey = "user_id"
)

// TokenResolver is a function that resolves a sessionID to a token
type TokenResolver func(sessionID string) (string, error)

// BeforeHookResolver resolves an opaque before-hook key into a trusted callback URL.
type BeforeHookResolver func(userID, toolName, hookKey string) (string, error)

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

// providerBase holds what every authentication provider needs: the Mattermost
// server URL for API communication and a logger.
type providerBase struct {
	mmServerURL string
	logger      logger.Logger
}

// newProviderBase uses internalURL for API communication if provided,
// otherwise falls back to externalURL.
func newProviderBase(externalURL, internalURL string, logger logger.Logger) providerBase {
	mmServerURL := internalURL
	if mmServerURL == "" {
		mmServerURL = externalURL
	}
	return providerBase{
		mmServerURL: mmServerURL,
		logger:      logger,
	}
}

// validateAuth implements AuthenticationProvider.ValidateAuth for providers
// whose GetAuthenticatedMattermostClient performs all validation.
func validateAuth(ctx context.Context, p AuthenticationProvider) error {
	_, err := p.GetAuthenticatedMattermostClient(ctx)
	return err
}
