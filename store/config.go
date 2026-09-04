// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
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
func (s *Store) SaveConfig(cfg config.Config) error {
	_, err := s.withConfigSaveLock(func(tx *sqlx.Tx) (config.Config, error) {
		return cfg, replaceConfig(tx, cfg)
	})
	return err
}

// UpdateConfig atomically loads and replaces the active configuration.
// The callback runs while the config save advisory lock is held.
func (s *Store) UpdateConfig(update func(stored *config.Config) (config.Config, error)) (config.Config, error) {
	return s.withConfigSaveLock(func(tx *sqlx.Tx) (config.Config, error) {
		stored, err := getConfig(tx)
		if err != nil {
			return config.Config{}, err
		}

		saved, err := update(stored)
		if err != nil {
			return config.Config{}, fmt.Errorf("failed to update config: %w", err)
		}

		return saved, replaceConfig(tx, saved)
	})
}

func (s *Store) withConfigSaveLock(save func(tx *sqlx.Tx) (config.Config, error)) (config.Config, error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return config.Config{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.Exec("SELECT pg_advisory_xact_lock($1, $2)", configSaveLockNamespace, configSaveLockKey); err != nil {
		return config.Config{}, fmt.Errorf("failed to lock config save transaction: %w", err)
	}

	saved, err := save(tx)
	if err != nil {
		return config.Config{}, err
	}

	if err := tx.Commit(); err != nil {
		return config.Config{}, fmt.Errorf("failed to commit config save: %w", err)
	}

	return saved, nil
}

func getConfig(tx *sqlx.Tx) (*config.Config, error) {
	var stored *config.Config
	var configJSON string
	err := tx.Get(&configJSON, "SELECT Config FROM Agents_ConfigHistory WHERE Active = true LIMIT 1")
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("failed to get active config: %w", err)
	default:
		var cfg config.Config
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %w", err)
		}
		stored = &cfg
	}

	return stored, nil
}

func replaceConfig(tx *sqlx.Tx, cfg config.Config) error {
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Deactivate current active config (at most one row, indexed on Active)
	if _, err := tx.Exec("UPDATE Agents_ConfigHistory SET Active = false WHERE Active = true"); err != nil {
		return fmt.Errorf("failed to deactivate current config: %w", err)
	}

	// Insert new active config
	if _, err := tx.Exec(
		"INSERT INTO Agents_ConfigHistory (ID, Config, CreateAt, Active) VALUES ($1, $2, $3, $4)",
		model.NewId(),
		string(configBytes),
		model.GetMillis(),
		true,
	); err != nil {
		return fmt.Errorf("failed to insert new config: %w", err)
	}

	return nil
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
