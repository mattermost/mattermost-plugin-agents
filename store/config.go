// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	configSaveLockNamespace = int32(12457)
	configSaveLockKey       = int32(1)
)

// GetConfig retrieves the currently active configuration from the database.
// Returns nil, nil if no active config exists (e.g., fresh install before migration).
func (s *Store) GetConfig() (*config.Config, error) {
	var configJSON string
	err := s.db.Get(&configJSON, "SELECT Config FROM Agents_ConfigHistory WHERE Active = true LIMIT 1")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get active config: %w", err)
	}

	var cfg config.Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}

// SaveConfig persists a new configuration to the database with history.
// The previous active config is deactivated and a new active row is inserted.
// All prior configs are preserved with Active = false.
//
// Under the advisory lock it reads the prior active config and returns it
// (nil when none existed) so callers can decide audit events from the
// committed transition. A read failure aborts the save with an error —
// callers must not invent a false→true transition from unknown prior state.
func (s *Store) SaveConfig(cfg config.Config) (previous *config.Config, err error) {
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Serialize SaveConfig across nodes/processes to avoid races on the partial
	// unique index for the active row.
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock($1, $2)", configSaveLockNamespace, configSaveLockKey); err != nil {
		return nil, fmt.Errorf("failed to lock config save transaction: %w", err)
	}

	var configJSON string
	readErr := tx.Get(&configJSON, "SELECT Config FROM Agents_ConfigHistory WHERE Active = true LIMIT 1")
	switch {
	case errors.Is(readErr, sql.ErrNoRows):
		previous = nil
	case readErr != nil:
		err = fmt.Errorf("failed to read previous active config: %w", readErr)
		return nil, err
	default:
		var prev config.Config
		if unmarshalErr := json.Unmarshal([]byte(configJSON), &prev); unmarshalErr != nil {
			err = fmt.Errorf("failed to unmarshal previous config: %w", unmarshalErr)
			return nil, err
		}
		previous = &prev
	}

	// Deactivate current active config (at most one row, indexed on Active)
	if _, err = tx.Exec("UPDATE Agents_ConfigHistory SET Active = false WHERE Active = true"); err != nil {
		return nil, fmt.Errorf("failed to deactivate current config: %w", err)
	}

	// Insert new active config
	if _, err = tx.Exec(
		"INSERT INTO Agents_ConfigHistory (ID, Config, CreateAt, Active) VALUES ($1, $2, $3, $4)",
		model.NewId(),
		string(configBytes),
		model.GetMillis(),
		true,
	); err != nil {
		return nil, fmt.Errorf("failed to insert new config: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit config save: %w", err)
	}

	return previous, nil
}

// IsConfigMigrated checks whether any active configuration exists in the database.
// Returns true if config has been migrated from config.json to the database.
func (s *Store) IsConfigMigrated() (bool, error) {
	var exists bool
	err := s.db.Get(&exists, "SELECT EXISTS(SELECT 1 FROM Agents_ConfigHistory WHERE Active = true)")
	if err != nil {
		return false, fmt.Errorf("failed to check config migration status: %w", err)
	}
	return exists, nil
}
