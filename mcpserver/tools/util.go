// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// resolveUserID returns the authenticated user's ID. It prefers the ID already
// resolved on the context (set by external servers) and falls back to GetMe so
// "me"-scoped tools work on every auth provider.
func (p *MattermostToolProvider) resolveUserID(mcpContext *MCPToolContext) (string, error) {
	if mcpContext.UserID != "" {
		return mcpContext.UserID, nil
	}
	user, _, err := mcpContext.Client.GetMe(mcpContext.Ctx, "")
	if err != nil {
		return "", fmt.Errorf("error getting current user: %w", err)
	}
	return user.Id, nil
}

// parseTimeMillis parses an RFC3339 timestamp into Unix milliseconds, matching the
// timestamp handling used by read_channel's `since` argument.
func parseTimeMillis(value string) (int64, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp format (expected ISO 8601 / RFC3339, e.g. 2024-01-01T15:00:00Z): %w", err)
	}
	return parsed.UnixMilli(), nil
}

// usernameFor returns a user's username, or "Unknown User" when the lookup fails.
func (p *MattermostToolProvider) usernameFor(ctx context.Context, client *model.Client4, userID string) string {
	if userID == "" {
		return "Unknown User"
	}
	user, _, err := client.GetUser(ctx, userID, "")
	if err != nil || user == nil {
		p.logger.Warn("failed to get user", "user_id", userID, "error", err)
		return "Unknown User"
	}
	return user.Username
}
