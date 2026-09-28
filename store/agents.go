// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// agentSelectColumns is the column list shared by all agent queries.
const agentSelectColumns = `ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
	CustomInstructions, ChannelAccessLevel, ChannelIDs,
	UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
	EnabledTools, AutoEnableNewMCPTools, mcp_dynamic_tool_loading,
	Model, EnableVision, DisableTools, EnabledNativeTools,
	ReasoningEnabled, ReasoningEffort, ThinkingBudget, StructuredOutputEnabled,
	MaxToolTurns, UseServiceAccountAuth,
	CreateAt, UpdateAt, DeleteAt`

// marshalJSONSlice serializes a slice for a JSON TEXT column.
// nil and [] both encode as "[]", as does a marshal error.
func marshalJSONSlice[T any](s []T) string {
	if len(s) == 0 {
		return "[]"
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalJSONSlice parses a JSON TEXT column into target. "" or "[]" → nil.
func unmarshalJSONSlice[T any](raw string, target *[]T) error {
	if raw == "" || raw == "[]" {
		*target = nil
		return nil
	}
	return json.Unmarshal([]byte(raw), target)
}

// agentRow is the DB-level representation of a UserAgent row.
// All JSON slice fields are stored as TEXT and scanned as strings.
// Note: db tags must be lowercase because PostgreSQL folds unquoted identifiers to lowercase.
type agentRow struct {
	ID                      string `db:"id"`
	BotUserID               string `db:"botuserid"`
	CreatorID               string `db:"creatorid"`
	DisplayName             string `db:"displayname"`
	Username                string `db:"username"`
	ServiceID               string `db:"serviceid"`
	CustomInstructions      string `db:"custominstructions"`
	ChannelAccessLevel      int    `db:"channelaccesslevel"`
	ChannelIDs              string `db:"channelids"`
	UserAccessLevel         int    `db:"useraccesslevel"`
	UserIDs                 string `db:"userids"`
	TeamIDs                 string `db:"teamids"`
	AdminUserIDs            string `db:"adminuserids"`
	EnabledTools            string `db:"enabledtools"`
	AutoEnableNewMCPTools   bool   `db:"autoenablenewmcptools"`
	MCPDynamicToolLoading   bool   `db:"mcp_dynamic_tool_loading"`
	Model                   string `db:"model"`
	EnableVision            bool   `db:"enablevision"`
	DisableTools            bool   `db:"disabletools"`
	EnabledNativeTools      string `db:"enablednativetools"`
	ReasoningEnabled        bool   `db:"reasoningenabled"`
	ReasoningEffort         string `db:"reasoningeffort"`
	ThinkingBudget          int    `db:"thinkingbudget"`
	StructuredOutputEnabled bool   `db:"structuredoutputenabled"`
	MaxToolTurns            int    `db:"maxtoolturns"`
	UseServiceAccountAuth   bool   `db:"useserviceaccountauth"`
	CreateAt                int64  `db:"createat"`
	UpdateAt                int64  `db:"updateat"`
	DeleteAt                int64  `db:"deleteat"`
}

// toBotConfig converts an agentRow (DB scan result) to an *llm.BotConfig.
func (r *agentRow) toBotConfig() (*llm.BotConfig, error) {
	cfg := &llm.BotConfig{
		ID:                      r.ID,
		BotUserID:               r.BotUserID,
		CreatorID:               r.CreatorID,
		DisplayName:             r.DisplayName,
		Name:                    r.Username,
		ServiceID:               r.ServiceID,
		CustomInstructions:      r.CustomInstructions,
		ChannelAccessLevel:      llm.ChannelAccessLevel(r.ChannelAccessLevel),
		UserAccessLevel:         llm.UserAccessLevel(r.UserAccessLevel),
		AutoEnableNewMCPTools:   r.AutoEnableNewMCPTools,
		MCPDynamicToolLoading:   r.MCPDynamicToolLoading,
		Model:                   r.Model,
		EnableVision:            r.EnableVision,
		DisableTools:            r.DisableTools,
		ReasoningEnabled:        r.ReasoningEnabled,
		ReasoningEffort:         r.ReasoningEffort,
		ThinkingBudget:          r.ThinkingBudget,
		StructuredOutputEnabled: r.StructuredOutputEnabled, //nolint:staticcheck // deprecated field persisted verbatim for compatibility
		MaxToolTurns:            r.MaxToolTurns,
		UseServiceAccountAuth:   r.UseServiceAccountAuth,
		CreateAt:                r.CreateAt,
		UpdateAt:                r.UpdateAt,
		DeleteAt:                r.DeleteAt,
	}

	if err := unmarshalJSONSlice(r.ChannelIDs, &cfg.ChannelIDs); err != nil {
		return nil, fmt.Errorf("failed to parse ChannelIDs: %w", err)
	}
	if err := unmarshalJSONSlice(r.UserIDs, &cfg.UserIDs); err != nil {
		return nil, fmt.Errorf("failed to parse UserIDs: %w", err)
	}
	if err := unmarshalJSONSlice(r.TeamIDs, &cfg.TeamIDs); err != nil {
		return nil, fmt.Errorf("failed to parse TeamIDs: %w", err)
	}
	if err := unmarshalJSONSlice(r.AdminUserIDs, &cfg.AdminUserIDs); err != nil {
		return nil, fmt.Errorf("failed to parse AdminUserIDs: %w", err)
	}
	if err := unmarshalJSONSlice(r.EnabledTools, &cfg.EnabledMCPTools); err != nil {
		return nil, fmt.Errorf("failed to parse EnabledTools: %w", err)
	}
	if err := unmarshalJSONSlice(r.EnabledNativeTools, &cfg.EnabledNativeTools); err != nil {
		return nil, fmt.Errorf("failed to parse EnabledNativeTools: %w", err)
	}

	return cfg, nil
}

// CreateAgent inserts a new user agent into the database.
// It generates the ID and sets CreateAt/UpdateAt timestamps automatically.
func (s *Store) CreateAgent(cfg *llm.BotConfig) error {
	cfg.ID = model.NewId()
	now := model.GetMillis()
	cfg.CreateAt = now
	cfg.UpdateAt = now
	cfg.DeleteAt = 0

	_, err := s.db.Exec(
		`INSERT INTO Agents_UserAgents (
			ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, ChannelAccessLevel, ChannelIDs,
			UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
			EnabledTools, AutoEnableNewMCPTools, mcp_dynamic_tool_loading,
			Model, EnableVision, DisableTools, EnabledNativeTools,
			ReasoningEnabled, ReasoningEffort, ThinkingBudget, StructuredOutputEnabled,
			MaxToolTurns, UseServiceAccountAuth,
			CreateAt, UpdateAt, DeleteAt
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)`,
		cfg.ID,
		cfg.BotUserID,
		cfg.CreatorID,
		cfg.DisplayName,
		cfg.Name,
		cfg.ServiceID,
		cfg.CustomInstructions,
		int(cfg.ChannelAccessLevel),
		marshalJSONSlice(cfg.ChannelIDs),
		int(cfg.UserAccessLevel),
		marshalJSONSlice(cfg.UserIDs),
		marshalJSONSlice(cfg.TeamIDs),
		marshalJSONSlice(cfg.AdminUserIDs),
		marshalJSONSlice(cfg.EnabledMCPTools),
		cfg.AutoEnableNewMCPTools,
		cfg.MCPDynamicToolLoading,
		cfg.Model,
		cfg.EnableVision,
		cfg.DisableTools,
		marshalJSONSlice(cfg.EnabledNativeTools),
		cfg.ReasoningEnabled,
		cfg.ReasoningEffort,
		cfg.ThinkingBudget,
		cfg.StructuredOutputEnabled, //nolint:staticcheck // deprecated field persisted verbatim for compatibility
		cfg.MaxToolTurns,
		cfg.UseServiceAccountAuth,
		cfg.CreateAt,
		cfg.UpdateAt,
		cfg.DeleteAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	return nil
}

// GetAgent retrieves a single active (non-deleted) agent by ID.
// Returns nil, nil if the agent does not exist or is soft-deleted.
func (s *Store) GetAgent(id string) (*llm.BotConfig, error) {
	var row agentRow
	err := s.db.Get(&row,
		`SELECT `+agentSelectColumns+`
		FROM Agents_UserAgents
		WHERE ID = $1 AND DeleteAt = 0`,
		id,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get agent %q: %w", id, err)
	}

	return row.toBotConfig()
}

// ListAgents returns all active (non-deleted) agents, ordered by creation time descending.
func (s *Store) ListAgents() ([]*llm.BotConfig, error) {
	var rows []agentRow
	err := s.db.Select(&rows,
		`SELECT `+agentSelectColumns+`
		FROM Agents_UserAgents
		WHERE DeleteAt = 0
		ORDER BY CreateAt DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}

	agents := make([]*llm.BotConfig, 0, len(rows))
	for i := range rows {
		cfg, parseErr := rows[i].toBotConfig()
		if parseErr != nil {
			return nil, parseErr
		}
		agents = append(agents, cfg)
	}

	return agents, nil
}

// CountActiveAgents returns the number of non-deleted agents.
func (s *Store) CountActiveAgents() (int, error) {
	var count int
	if err := s.db.Get(&count, `SELECT COUNT(*) FROM Agents_UserAgents WHERE DeleteAt = 0`); err != nil {
		return 0, fmt.Errorf("failed to count active agents: %w", err)
	}
	return count, nil
}

// ListAgentsByCreator returns all active agents created by the specified user.
func (s *Store) ListAgentsByCreator(creatorID string) ([]*llm.BotConfig, error) {
	var rows []agentRow
	err := s.db.Select(&rows,
		`SELECT `+agentSelectColumns+`
		FROM Agents_UserAgents
		WHERE CreatorID = $1 AND DeleteAt = 0
		ORDER BY CreateAt DESC`,
		creatorID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents by creator %q: %w", creatorID, err)
	}

	agents := make([]*llm.BotConfig, 0, len(rows))
	for i := range rows {
		cfg, parseErr := rows[i].toBotConfig()
		if parseErr != nil {
			return nil, parseErr
		}
		agents = append(agents, cfg)
	}

	return agents, nil
}

// UpdateAgent updates an existing agent's mutable fields.
// It sets UpdateAt automatically. The caller must supply the full agent struct
// (read-modify-write pattern). Does NOT update ID, CreatorID, BotUserID, CreateAt, or DeleteAt.
func (s *Store) UpdateAgent(cfg *llm.BotConfig) error {
	// Millisecond timestamps can collide when create and update run in the same ms; ensure UpdateAt advances.
	now := model.GetMillis()
	if now <= cfg.UpdateAt {
		now = cfg.UpdateAt + 1
	}
	cfg.UpdateAt = now

	result, err := s.db.Exec(
		`UPDATE Agents_UserAgents SET
			DisplayName = $1,
			Username = $2,
			ServiceID = $3,
			CustomInstructions = $4,
			ChannelAccessLevel = $5,
			ChannelIDs = $6,
			UserAccessLevel = $7,
			UserIDs = $8,
			TeamIDs = $9,
			AdminUserIDs = $10,
			EnabledTools = $11,
			AutoEnableNewMCPTools = $12,
			mcp_dynamic_tool_loading = $13,
			Model = $14,
			EnableVision = $15,
			DisableTools = $16,
			EnabledNativeTools = $17,
			ReasoningEnabled = $18,
			ReasoningEffort = $19,
			ThinkingBudget = $20,
			StructuredOutputEnabled = $21,
			MaxToolTurns = $22,
			UseServiceAccountAuth = $23,
			UpdateAt = $24
		WHERE ID = $25 AND DeleteAt = 0`,
		cfg.DisplayName,
		cfg.Name,
		cfg.ServiceID,
		cfg.CustomInstructions,
		int(cfg.ChannelAccessLevel),
		marshalJSONSlice(cfg.ChannelIDs),
		int(cfg.UserAccessLevel),
		marshalJSONSlice(cfg.UserIDs),
		marshalJSONSlice(cfg.TeamIDs),
		marshalJSONSlice(cfg.AdminUserIDs),
		marshalJSONSlice(cfg.EnabledMCPTools),
		cfg.AutoEnableNewMCPTools,
		cfg.MCPDynamicToolLoading,
		cfg.Model,
		cfg.EnableVision,
		cfg.DisableTools,
		marshalJSONSlice(cfg.EnabledNativeTools),
		cfg.ReasoningEnabled,
		cfg.ReasoningEffort,
		cfg.ThinkingBudget,
		cfg.StructuredOutputEnabled, //nolint:staticcheck // deprecated field persisted verbatim for compatibility
		cfg.MaxToolTurns,
		cfg.UseServiceAccountAuth,
		cfg.UpdateAt,
		cfg.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update agent %q: %w", cfg.ID, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected for agent %q: %w", cfg.ID, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("agent %q not found or already deleted", cfg.ID)
	}

	return nil
}

// DeleteAgent performs a soft delete by setting DeleteAt to the current timestamp.
func (s *Store) DeleteAgent(id string) error {
	result, err := s.db.Exec(
		`UPDATE Agents_UserAgents SET DeleteAt = $1 WHERE ID = $2 AND DeleteAt = 0`,
		model.GetMillis(),
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to delete agent %q: %w", id, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected for agent %q: %w", id, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("agent %q not found or already deleted", id)
	}

	return nil
}
