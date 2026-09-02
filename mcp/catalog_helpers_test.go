// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// GetToolsForUser is a test-only shorthand for a selected user-mode catalog.
func (m *ClientManager) GetToolsForUser(ctx context.Context, userID string, selection ToolSelection) ([]llm.Tool, *Errors) {
	return m.GetToolsWithSelection(ctx, UserCatalogRequest(userID), selection)
}

// GetTools is a test-only view of one bag's namespaced tools.
func (c *UserClients) GetTools(context.Context) []llm.Tool {
	return collectToolsFromSnapshots(c.userID, c.log, c.snapshotClients())
}
