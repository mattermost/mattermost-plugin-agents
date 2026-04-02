// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package useragents

import (
	"encoding/json"
	"fmt"
)

// UserAgent represents a user-created AI agent persisted in the Agents_UserAgents table.
type UserAgent struct {
	ID                      string        `json:"id" db:"ID"`
	BotUserID               string        `json:"bot_user_id" db:"BotUserID"`
	CreatorID               string        `json:"creator_id" db:"CreatorID"`
	DisplayName             string        `json:"display_name" db:"DisplayName"`
	Username                string        `json:"username" db:"Username"`
	ServiceID               string        `json:"service_id" db:"ServiceID"`
	CustomInstructions      string        `json:"custom_instructions" db:"CustomInstructions"`
	ChannelAccessLevel      int           `json:"channel_access_level" db:"ChannelAccessLevel"`
	ChannelIDs              []string      `json:"channel_ids"`
	UserAccessLevel         int           `json:"user_access_level" db:"UserAccessLevel"`
	UserIDs                 []string      `json:"user_ids"`
	TeamIDs                 []string      `json:"team_ids"`
	AdminUserIDs            []string      `json:"admin_user_ids"`
	EnabledTools            []EnabledTool `json:"enabled_tools"`
	Model                   string        `json:"model"`
	EnableVision            bool          `json:"enable_vision"`
	DisableTools            bool          `json:"disable_tools"`
	EnabledNativeTools      []string      `json:"enabled_native_tools"`
	ReasoningEnabled        bool          `json:"reasoning_enabled"`
	ReasoningEffort         string        `json:"reasoning_effort"`
	ThinkingBudget          int           `json:"thinking_budget"`
	StructuredOutputEnabled bool          `json:"structured_output_enabled"`
	CreateAt                int64         `json:"create_at" db:"CreateAt"`
	UpdateAt                int64         `json:"update_at" db:"UpdateAt"`
	DeleteAt                int64         `json:"delete_at" db:"DeleteAt"`
}

// EnabledTool identifies a single tool on a specific MCP server that this agent may use.
type EnabledTool struct {
	ServerOrigin string `json:"server_origin"`
	ToolName     string `json:"tool_name"`
}

// --- JSON helpers for DB TEXT columns ---

// ChannelIDsJSON returns the JSON-encoded string for the ChannelIDs slice.
// Returns "[]" for nil/empty slices.
func (u *UserAgent) ChannelIDsJSON() string {
	return mustMarshalSlice(u.ChannelIDs)
}

// UserIDsJSON returns the JSON-encoded string for the UserIDs slice.
func (u *UserAgent) UserIDsJSON() string {
	return mustMarshalSlice(u.UserIDs)
}

// TeamIDsJSON returns the JSON-encoded string for the TeamIDs slice.
func (u *UserAgent) TeamIDsJSON() string {
	return mustMarshalSlice(u.TeamIDs)
}

// AdminUserIDsJSON returns the JSON-encoded string for the AdminUserIDs slice.
func (u *UserAgent) AdminUserIDsJSON() string {
	return mustMarshalSlice(u.AdminUserIDs)
}

// EnabledToolsJSON returns the JSON-encoded string for the EnabledTools slice.
func (u *UserAgent) EnabledToolsJSON() string {
	b, err := json.Marshal(u.EnabledTools)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// EnabledNativeToolsJSON returns JSON for provider-native tool IDs (e.g. web_search).
func (u *UserAgent) EnabledNativeToolsJSON() string {
	if u.EnabledNativeTools == nil {
		return "[]"
	}
	b, err := json.Marshal(u.EnabledNativeTools)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// SetEnabledNativeToolsFromJSON parses EnabledNativeTools from DB JSON text.
func (u *UserAgent) SetEnabledNativeToolsFromJSON(raw string) error {
	if raw == "" || raw == "[]" {
		u.EnabledNativeTools = nil
		return nil
	}
	return json.Unmarshal([]byte(raw), &u.EnabledNativeTools)
}

// SetChannelIDsFromJSON parses a JSON string into the ChannelIDs field.
func (u *UserAgent) SetChannelIDsFromJSON(raw string) error {
	return unmarshalSlice(raw, &u.ChannelIDs)
}

// SetUserIDsFromJSON parses a JSON string into the UserIDs field.
func (u *UserAgent) SetUserIDsFromJSON(raw string) error {
	return unmarshalSlice(raw, &u.UserIDs)
}

// SetTeamIDsFromJSON parses a JSON string into the TeamIDs field.
func (u *UserAgent) SetTeamIDsFromJSON(raw string) error {
	return unmarshalSlice(raw, &u.TeamIDs)
}

// SetAdminUserIDsFromJSON parses a JSON string into the AdminUserIDs field.
func (u *UserAgent) SetAdminUserIDsFromJSON(raw string) error {
	return unmarshalSlice(raw, &u.AdminUserIDs)
}

// SetEnabledToolsFromJSON parses a JSON string into the EnabledTools field.
// Preserves nil vs empty semantics: "" → nil (all tools), "null" → nil,
// "[]" → empty slice (no tools), "[{…}]" → populated slice (specific tools).
func (u *UserAgent) SetEnabledToolsFromJSON(raw string) error {
	if raw == "" {
		u.EnabledTools = nil
		return nil
	}
	return json.Unmarshal([]byte(raw), &u.EnabledTools)
}

// mustMarshalSlice marshals a string slice to JSON, returning "[]" on nil/empty or error.
func mustMarshalSlice(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalSlice parses a JSON string into a *[]string, setting nil for empty arrays.
func unmarshalSlice(raw string, target *[]string) error {
	if raw == "" || raw == "[]" {
		*target = nil
		return nil
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("failed to unmarshal JSON slice: %w", err)
	}
	return nil
}
