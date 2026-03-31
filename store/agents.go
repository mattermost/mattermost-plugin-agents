// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/useragents"
	"github.com/mattermost/mattermost/server/public/model"
)

// agentRow is the DB-level representation of a UserAgent row.
// All JSON slice fields are stored as TEXT and scanned as strings.
// Note: db tags must be lowercase because PostgreSQL folds unquoted identifiers to lowercase.
type agentRow struct {
	ID                 string `db:"id"`
	BotUserID          string `db:"botuserid"`
	CreatorID          string `db:"creatorid"`
	DisplayName        string `db:"displayname"`
	Username           string `db:"username"`
	ServiceID          string `db:"serviceid"`
	CustomInstructions string `db:"custominstructions"`
	ChannelAccessLevel int    `db:"channelaccesslevel"`
	ChannelIDs         string `db:"channelids"`
	UserAccessLevel    int    `db:"useraccesslevel"`
	UserIDs            string `db:"userids"`
	TeamIDs            string `db:"teamids"`
	AdminUserIDs       string `db:"adminuserids"`
	EnabledTools       string `db:"enabledtools"`
	CreateAt           int64  `db:"createat"`
	UpdateAt           int64  `db:"updateat"`
	DeleteAt           int64  `db:"deleteat"`
}

// toUserAgent converts an agentRow (DB scan result) to a useragents.UserAgent.
func (r *agentRow) toUserAgent() (*useragents.UserAgent, error) {
	agent := &useragents.UserAgent{
		ID:                 r.ID,
		BotUserID:          r.BotUserID,
		CreatorID:          r.CreatorID,
		DisplayName:        r.DisplayName,
		Username:           r.Username,
		ServiceID:          r.ServiceID,
		CustomInstructions: r.CustomInstructions,
		ChannelAccessLevel: r.ChannelAccessLevel,
		UserAccessLevel:    r.UserAccessLevel,
		CreateAt:           r.CreateAt,
		UpdateAt:           r.UpdateAt,
		DeleteAt:           r.DeleteAt,
	}

	if err := agent.SetChannelIDsFromJSON(r.ChannelIDs); err != nil {
		return nil, fmt.Errorf("failed to parse ChannelIDs: %w", err)
	}
	if err := agent.SetUserIDsFromJSON(r.UserIDs); err != nil {
		return nil, fmt.Errorf("failed to parse UserIDs: %w", err)
	}
	if err := agent.SetTeamIDsFromJSON(r.TeamIDs); err != nil {
		return nil, fmt.Errorf("failed to parse TeamIDs: %w", err)
	}
	if err := agent.SetAdminUserIDsFromJSON(r.AdminUserIDs); err != nil {
		return nil, fmt.Errorf("failed to parse AdminUserIDs: %w", err)
	}
	if err := agent.SetEnabledToolsFromJSON(r.EnabledTools); err != nil {
		return nil, fmt.Errorf("failed to parse EnabledTools: %w", err)
	}

	return agent, nil
}

// CreateAgent inserts a new user agent into the database.
// It generates the ID and sets CreateAt/UpdateAt timestamps automatically.
func (s *Store) CreateAgent(agent *useragents.UserAgent) error {
	agent.ID = model.NewId()
	now := model.GetMillis()
	agent.CreateAt = now
	agent.UpdateAt = now
	agent.DeleteAt = 0

	_, err := s.db.Exec(
		`INSERT INTO Agents_UserAgents (
			ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, ChannelAccessLevel, ChannelIDs,
			UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
			EnabledTools, CreateAt, UpdateAt, DeleteAt
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		agent.ID,
		agent.BotUserID,
		agent.CreatorID,
		agent.DisplayName,
		agent.Username,
		agent.ServiceID,
		agent.CustomInstructions,
		agent.ChannelAccessLevel,
		agent.ChannelIDsJSON(),
		agent.UserAccessLevel,
		agent.UserIDsJSON(),
		agent.TeamIDsJSON(),
		agent.AdminUserIDsJSON(),
		agent.EnabledToolsJSON(),
		agent.CreateAt,
		agent.UpdateAt,
		agent.DeleteAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	return nil
}

// GetAgent retrieves a single active (non-deleted) agent by ID.
// Returns nil, nil if the agent does not exist or is soft-deleted.
func (s *Store) GetAgent(id string) (*useragents.UserAgent, error) {
	var row agentRow
	err := s.db.Get(&row,
		`SELECT ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, ChannelAccessLevel, ChannelIDs,
			UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
			EnabledTools, CreateAt, UpdateAt, DeleteAt
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

	return row.toUserAgent()
}

// ListAgents returns all active (non-deleted) agents, ordered by creation time descending.
func (s *Store) ListAgents() ([]*useragents.UserAgent, error) {
	var rows []agentRow
	err := s.db.Select(&rows,
		`SELECT ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, ChannelAccessLevel, ChannelIDs,
			UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
			EnabledTools, CreateAt, UpdateAt, DeleteAt
		FROM Agents_UserAgents
		WHERE DeleteAt = 0
		ORDER BY CreateAt DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}

	agents := make([]*useragents.UserAgent, 0, len(rows))
	for i := range rows {
		agent, parseErr := rows[i].toUserAgent()
		if parseErr != nil {
			return nil, parseErr
		}
		agents = append(agents, agent)
	}

	return agents, nil
}

// ListAgentsByCreator returns all active agents created by the specified user.
func (s *Store) ListAgentsByCreator(creatorID string) ([]*useragents.UserAgent, error) {
	var rows []agentRow
	err := s.db.Select(&rows,
		`SELECT ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, ChannelAccessLevel, ChannelIDs,
			UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
			EnabledTools, CreateAt, UpdateAt, DeleteAt
		FROM Agents_UserAgents
		WHERE CreatorID = $1 AND DeleteAt = 0
		ORDER BY CreateAt DESC`,
		creatorID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents by creator %q: %w", creatorID, err)
	}

	agents := make([]*useragents.UserAgent, 0, len(rows))
	for i := range rows {
		agent, parseErr := rows[i].toUserAgent()
		if parseErr != nil {
			return nil, parseErr
		}
		agents = append(agents, agent)
	}

	return agents, nil
}

// UpdateAgent updates an existing agent's mutable fields.
// It sets UpdateAt automatically. The caller must supply the full agent struct
// (read-modify-write pattern). Does NOT update ID, CreatorID, BotUserID, CreateAt, or DeleteAt.
func (s *Store) UpdateAgent(agent *useragents.UserAgent) error {
	agent.UpdateAt = model.GetMillis()

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
			UpdateAt = $12
		WHERE ID = $13 AND DeleteAt = 0`,
		agent.DisplayName,
		agent.Username,
		agent.ServiceID,
		agent.CustomInstructions,
		agent.ChannelAccessLevel,
		agent.ChannelIDsJSON(),
		agent.UserAccessLevel,
		agent.UserIDsJSON(),
		agent.TeamIDsJSON(),
		agent.AdminUserIDsJSON(),
		agent.EnabledToolsJSON(),
		agent.UpdateAt,
		agent.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update agent %q: %w", agent.ID, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected for agent %q: %w", agent.ID, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("agent %q not found or already deleted", agent.ID)
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

// Compile-time check that *Store satisfies the AgentStore interface.
var _ interface {
	CreateAgent(agent *useragents.UserAgent) error
	GetAgent(id string) (*useragents.UserAgent, error)
	ListAgents() ([]*useragents.UserAgent, error)
	ListAgentsByCreator(creatorID string) ([]*useragents.UserAgent, error)
	UpdateAgent(agent *useragents.UserAgent) error
	DeleteAgent(id string) error
} = (*Store)(nil)
