// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package auth

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/logger"
	"github.com/mattermost/mattermost/server/public/model"
)

// TokenAuthenticationProvider provides PAT token authentication for STDIO transport
type TokenAuthenticationProvider struct {
	providerBase
	token string
}

// NewTokenAuthenticationProvider creates a new PAT token authentication provider for STDIO transport
// Uses internalURL for API communication if provided, otherwise falls back to externalURL
func NewTokenAuthenticationProvider(externalURL, internalURL, token string, logger logger.Logger) *TokenAuthenticationProvider {
	return &TokenAuthenticationProvider{
		providerBase: newProviderBase(externalURL, internalURL, logger),
		token:        token,
	}
}

// ValidateAuth validates authentication (single GetMe call)
func (p *TokenAuthenticationProvider) ValidateAuth(ctx context.Context) error {
	return validateAuth(ctx, p)
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
