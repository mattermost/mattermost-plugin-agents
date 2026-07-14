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

// ErrStaleLegacyServiceIDs is returned by any config write whose payload
// still contains legacy 36-char UUID service IDs after the one-time service
// ID migration has run. It indicates a stale client (e.g. a pre-upgrade
// webapp bundle) trying to write pre-migration IDs back. Enforced inside
// insertActiveConfigTx so every writer, present or future, is covered.
var ErrStaleLegacyServiceIDs = errors.New("config contains legacy UUID service IDs from before the ID migration; reload the system console and retry")

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
// SaveConfig is a blind write: it does not read the current config first, so
// it is only appropriate for bootstrap/first-write paths (config.json -> DB
// migration). Read-modify-write callers must use UpdateConfig instead, or
// they can race a concurrent writer and lose its update. The post-migration
// legacy UUID guard still applies (see insertActiveConfigTx).
func (s *Store) SaveConfig(cfg config.Config) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Serialize SaveConfig across nodes/processes to avoid races on the partial
	// unique index for the active row.
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock($1, $2)", configSaveLockNamespace, configSaveLockKey); err != nil {
		return fmt.Errorf("failed to lock config save transaction: %w", err)
	}

	if err = insertActiveConfigTx(tx, cfg); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit config save: %w", err)
	}

	return nil
}

// UpdateConfig atomically reads the active config, applies transform to
// produce the next config, and persists the result as the new active row —
// all in one transaction under the same advisory lock as SaveConfig, so no
// concurrent save or migration can interleave between the read and the write.
// transform receives nil when no active config exists; a transform error
// aborts the update with nothing written and is returned unwrapped so callers
// can map their own sentinel errors.
//
// After the one-time service ID migration has run, a transformed config that
// still contains legacy UUID service IDs is rejected with
// ErrStaleLegacyServiceIDs: a stale client must never write UUIDs back.
func (s *Store) UpdateConfig(transform func(prev *config.Config) (config.Config, error)) (config.Config, error) {
	var next config.Config

	tx, err := s.db.Beginx()
	if err != nil {
		return next, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec("SELECT pg_advisory_xact_lock($1, $2)", configSaveLockNamespace, configSaveLockKey); err != nil {
		return next, fmt.Errorf("failed to lock config update transaction: %w", err)
	}

	prev, _, err := getActiveConfigTx(tx)
	if err != nil {
		return next, err
	}
	next, err = transform(prev)
	if err != nil {
		return next, err
	}

	if err = insertActiveConfigTx(tx, next); err != nil {
		return next, err
	}

	if err = tx.Commit(); err != nil {
		return next, fmt.Errorf("failed to commit config update: %w", err)
	}

	return next, nil
}

// configHasLegacyUUIDServiceIDs reports whether any service entry carries a
// pre-migration 36-char UUID as its ID.
func configHasLegacyUUIDServiceIDs(cfg *config.Config) bool {
	for i := range cfg.Services {
		if isLegacyUUID(cfg.Services[i].ID) {
			return true
		}
	}
	return false
}

// insertActiveConfigTx is the single transactional primitive for writing a
// new active config-history row: it deactivates the current active row and
// inserts cfg as the new active one. Used by SaveConfig, UpdateConfig, and
// the ABAC ID migration.
//
// It enforces the post-migration invariant here so that no writer can
// reintroduce legacy UUID service IDs once the service ID migration marker is
// set. The ID migration itself never trips this guard: it rewrites UUIDs
// before inserting when its marker is unset, and once the marker is set the
// active config no longer contains UUIDs (they commit atomically).
func insertActiveConfigTx(tx *sqlx.Tx, cfg config.Config) error {
	migrated, err := getSystemValueTx(tx, serviceIDMigrationKey)
	if err != nil {
		return err
	}
	if migrated == "1" && configHasLegacyUUIDServiceIDs(&cfg) {
		return ErrStaleLegacyServiceIDs
	}

	configBytes, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Deactivate current active config (at most one row, indexed on Active)
	if _, err := tx.Exec("UPDATE Agents_ConfigHistory SET Active = false WHERE Active = true"); err != nil {
		return fmt.Errorf("failed to deactivate current config: %w", err)
	}
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
