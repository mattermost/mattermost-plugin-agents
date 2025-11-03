// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package auth

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/mattermost/mattermost/server/public/model"
)

// SessionAuthenticationProvider provides session-based authentication for in-memory transport
// This provider uses existing Mattermost session tokens passed through context,
// eliminating the need for separate OAuth flows for embedded MCP servers
type SessionAuthenticationProvider struct {
	mmServerURL         string // Mattermost server URL for API communication
	mmInternalServerURL string // Internal server URL (may be different for containerized deployments)
	logger              logger.Logger
}

// NewSessionAuthenticationProvider creates a new session authentication provider for in-memory transport
// Uses internalURL for API communication if provided, otherwise falls back to externalURL
func NewSessionAuthenticationProvider(externalURL, internalURL string, logger logger.Logger) *SessionAuthenticationProvider {
	// Use internal URL for API communication if provided, otherwise fallback to external URL
	mmServerURL := internalURL
	if mmServerURL == "" {
		mmServerURL = externalURL
	}

	return &SessionAuthenticationProvider{
		mmServerURL:         mmServerURL,
		mmInternalServerURL: internalURL,
		logger:              logger,
	}
}

// ValidateAuth validates session authentication from context
// The session token must be present in the context and be valid
func (p *SessionAuthenticationProvider) ValidateAuth(ctx context.Context) error {
	// Get authenticated client, which handles all validation
	_, err := p.GetAuthenticatedMattermostClient(ctx)
	return err
}

// GetAuthenticatedMattermostClient returns a session-authenticated Mattermost client
// Uses token resolver to get tokens from session IDs for the embedded server
func (p *SessionAuthenticationProvider) GetAuthenticatedMattermostClient(ctx context.Context) (*model.Client4, error) {
	// Get token resolver from context (required for embedded server authentication)
	resolver, ok := ctx.Value(TokenResolverContextKey).(TokenResolver)
	if !ok {
		return nil, fmt.Errorf("session authentication provider requires token resolver in context")
	}

	// Get session ID from context
	sessionID, ok := ctx.Value(SessionIDContextKey).(string)
	if !ok || sessionID == "" {
		return nil, fmt.Errorf("session authentication provider requires valid session ID in context")
	}

	// Resolve token from session ID
	token, err := resolver(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve token from session: %w", err)
	}

	// Create client and set session token
	// Session tokens can be used directly as Bearer tokens in Mattermost API
	client := model.NewAPIv4Client(p.mmServerURL)
	client.SetToken(token)

	// Validate the token by attempting to get current user information
	// This ensures the session is still valid and not expired
	if _, err := p.fetchAuthenticatedUser(ctx, client); err != nil {
		return nil, err
	}

	return client, nil
}

// GetAuthenticatedUser returns the authenticated Mattermost user for the session token in context.
// Uses token resolver to get tokens from session IDs for the embedded server
func (p *SessionAuthenticationProvider) GetAuthenticatedUser(ctx context.Context) (*model.User, error) {
	// Get token resolver from context (required for embedded server authentication)
	resolver, ok := ctx.Value(TokenResolverContextKey).(TokenResolver)
	if !ok {
		return nil, fmt.Errorf("session authentication provider requires token resolver in context")
	}

	// Get session ID from context
	sessionID, ok := ctx.Value(SessionIDContextKey).(string)
	if !ok || sessionID == "" {
		return nil, fmt.Errorf("session authentication provider requires valid session ID in context")
	}

	// Resolve token from session ID
	token, err := resolver(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve token from session: %w", err)
	}

	client := model.NewAPIv4Client(p.mmServerURL)
	client.SetToken(token)

	user, err := p.fetchAuthenticatedUser(ctx, client)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (p *SessionAuthenticationProvider) fetchAuthenticatedUser(ctx context.Context, client *model.Client4) (*model.User, error) {
	user, _, err := client.GetMe(ctx, "")
	if err != nil {
		p.logger.Error("failed to validate session token",
			"error", err,
			"server_url", p.mmServerURL)
		return nil, fmt.Errorf("invalid session token: %w", err)
	}

	p.logger.Debug("Validated session token for embedded MCP server",
		"user_id", user.Id,
		"username", user.Username)

	return user, nil
}
