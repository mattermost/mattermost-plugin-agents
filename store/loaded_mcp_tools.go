// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"fmt"

	sq "github.com/Masterminds/squirrel"
)

// LoadedMCPTool records that a namespaced MCP tool has been loaded for a
// conversation context. Raw schemas are intentionally not stored here.
type LoadedMCPTool struct {
	ConversationID string `db:"conversationid"`
	BotID          string `db:"botid"`
	UserID         string `db:"userid"`
	ToolName       string `db:"toolname"`
	ServerOrigin   string `db:"serverorigin"`
	BareName       string `db:"barename"`
	CreatedAt      int64  `db:"createdat"`
	UpdatedAt      int64  `db:"updatedat"`
}

var loadedMCPToolColumns = []string{
	"ConversationID", "BotID", "UserID", "ToolName",
	"ServerOrigin", "BareName", "CreatedAt", "UpdatedAt",
}

// UpsertLoadedMCPTool inserts or refreshes loaded-tool state for one
// conversation/bot/user tuple. It preserves CreatedAt on refresh.
func (s *Store) UpsertLoadedMCPTool(tool LoadedMCPTool) error {
	if tool.ConversationID == "" {
		return fmt.Errorf("conversation id is required")
	}
	if tool.BotID == "" {
		return fmt.Errorf("bot id is required")
	}
	if tool.UserID == "" {
		return fmt.Errorf("user id is required")
	}
	if tool.ToolName == "" {
		return fmt.Errorf("tool name is required")
	}

	query, args, err := s.builder.Insert("LLM_LoadedMCPTools").
		Columns(loadedMCPToolColumns...).
		Values(
			tool.ConversationID,
			tool.BotID,
			tool.UserID,
			tool.ToolName,
			tool.ServerOrigin,
			tool.BareName,
			tool.CreatedAt,
			tool.UpdatedAt,
		).
		Suffix(`
ON CONFLICT (ConversationID, BotID, UserID, ToolName)
DO UPDATE SET
    ServerOrigin = EXCLUDED.ServerOrigin,
    BareName = EXCLUDED.BareName,
    UpdatedAt = EXCLUDED.UpdatedAt`).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build upsert loaded MCP tool query: %w", err)
	}
	if _, err = s.db.Exec(query, args...); err != nil {
		return fmt.Errorf("failed to upsert loaded MCP tool: %w", err)
	}
	return nil
}

// ListLoadedMCPTools returns the loaded tools for an exact conversation/bot/user
// tuple sorted by ToolName for deterministic restoration.
func (s *Store) ListLoadedMCPTools(conversationID, botID, userID string) ([]LoadedMCPTool, error) {
	query, args, err := s.builder.
		Select(loadedMCPToolColumns...).
		From("LLM_LoadedMCPTools").
		Where(sq.Eq{"ConversationID": conversationID}).
		Where(sq.Eq{"BotID": botID}).
		Where(sq.Eq{"UserID": userID}).
		OrderBy("ToolName ASC").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build list loaded MCP tools query: %w", err)
	}

	var tools []LoadedMCPTool
	if err := s.db.Select(&tools, query, args...); err != nil {
		return nil, fmt.Errorf("failed to list loaded MCP tools: %w", err)
	}
	if tools == nil {
		tools = []LoadedMCPTool{}
	}
	return tools, nil
}

// DeleteLoadedMCPTool removes one loaded-tool row. Missing rows are a no-op.
func (s *Store) DeleteLoadedMCPTool(conversationID, botID, userID, toolName string) error {
	query, args, err := s.builder.
		Delete("LLM_LoadedMCPTools").
		Where(sq.Eq{
			"ConversationID": conversationID,
			"BotID":          botID,
			"UserID":         userID,
			"ToolName":       toolName,
		}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build delete loaded MCP tool query: %w", err)
	}
	if _, err = s.db.Exec(query, args...); err != nil {
		return fmt.Errorf("failed to delete loaded MCP tool: %w", err)
	}
	return nil
}

// DeleteLoadedMCPToolsByNames removes loaded-tool rows for a single
// conversation/bot/user tuple matching any of the given tool names. An empty
// toolNames slice is a no-op so callers can pass the result of a filter
// without guarding the call site.
func (s *Store) DeleteLoadedMCPToolsByNames(conversationID, botID, userID string, toolNames []string) error {
	if len(toolNames) == 0 {
		return nil
	}

	query, args, err := s.builder.
		Delete("LLM_LoadedMCPTools").
		Where(sq.Eq{
			"ConversationID": conversationID,
			"BotID":          botID,
			"UserID":         userID,
			"ToolName":       toolNames,
		}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build batch delete loaded MCP tools query: %w", err)
	}
	if _, err = s.db.Exec(query, args...); err != nil {
		return fmt.Errorf("failed to batch delete loaded MCP tools: %w", err)
	}
	return nil
}

// DeleteLoadedMCPToolsForConversation removes all loaded-tool rows for a
// conversation.
func (s *Store) DeleteLoadedMCPToolsForConversation(conversationID string) error {
	query, args, err := s.builder.
		Delete("LLM_LoadedMCPTools").
		Where(sq.Eq{"ConversationID": conversationID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build delete conversation loaded MCP tools query: %w", err)
	}
	if _, err = s.db.Exec(query, args...); err != nil {
		return fmt.Errorf("failed to delete conversation loaded MCP tools: %w", err)
	}
	return nil
}
