// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package auth

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/mcpserver/logger"
	"github.com/mattermost/mattermost/server/public/model"
)

// TokenAuthenticationProvider provides PAT token authentication for STDIO transport
type TokenAuthenticationProvider struct {
	mmServerURL string // Mattermost server URL for API communication
	token       string
	logger      logger.Logger
}

// NewTokenAuthenticationProvider creates a new PAT token authentication provider for STDIO transport
// Uses internalURL for API communication if provided, otherwise falls back to externalURL
func NewTokenAuthenticationProvider(externalURL, internalURL, token string, logger logger.Logger) *TokenAuthenticationProvider {
	// Use internal URL for API communication if provided, otherwise fallback to external URL
	mmServerURL := internalURL
	if mmServerURL == "" {
		mmServerURL = externalURL
	}

	return &TokenAuthenticationProvider{
		mmServerURL: mmServerURL,
		token:       token,
		logger:      logger,
	}
}

// ValidateAuth validates authentication
func (p *TokenAuthenticationProvider) ValidateAuth(ctx context.Context) error {
	// Get authenticated client and validate token (single GetMe call)
	_, err := p.GetAuthenticatedMattermostClient(ctx)
	return err
}

// GetAuthenticatedMattermostClient returns an authenticated Mattermost client
func (p *TokenAuthenticationProvider) GetAuthenticatedMattermostClient(ctx context.Context) (*model.Client4, error) {
	if p.token == "" {
		return nil, fmt.Errorf("no authentication token available")
	}

	// Create client with configured token
	client := model.NewAPIv4Client(p.mmServerURL)
	client.SetToken(p.token)

	// Validate token by getting current user (single validation call)
	user, _, err := client.GetMe(ctx, "")
	if err != nil {
		p.logger.Error("failed to validate token", "error", err)
		return nil, fmt.Errorf("invalid authentication token: %w", err)
	}

	p.logger.Debug("validated token for user", "user_id", user.Id, "username", user.Username)

	return client, nil
}
