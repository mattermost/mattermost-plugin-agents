// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost/server/public/model"
)

// Agents_System markers for the one-time ABAC ID migrations (value "1").
const (
	serviceIDMigrationKey   = "abac_service_id_migration_done"
	mcpServerIDMigrationKey = "abac_mcp_server_id_migration_done"
)

// isLegacyUUID reports whether s is a canonical dashed UUID, the legacy
// service-ID format that predates model.NewId()-style IDs.
func isLegacyUUID(s string) bool {
	return len(s) == 36 && uuid.Validate(s) == nil
}

// ServiceIDMigrationReport summarizes what MigrateServiceIDs did so the
// caller (server layer) can log it; the store has no logger.
type ServiceIDMigrationReport struct {
	Migrated            bool
	ServicesRemapped    int
	AgentRowsUpdated    int64
	DanglingServiceRefs []string
}

// MigrateServiceIDs rewrites legacy UUID service IDs in the active config
// (ServiceConfig.ID, ServiceConfig.FallbackServiceID, BotConfig.ServiceID)
// and in Agents_UserAgents.ServiceID to model.NewId() values, atomically in
// one transaction. It is idempotent: a marker in Agents_System short-circuits
// re-runs, and the rewrite itself is content-based (only IDs that are UUIDs
// are touched), so a crash between the config write and the marker write is
// recovered safely. Dangling UUID references (no matching service) are left
// unchanged and reported.
func (s *Store) MigrateServiceIDs() (ServiceIDMigrationReport, error) {
	report := ServiceIDMigrationReport{}

	// Fast path; the authoritative re-check happens inside the tx under the lock.
	done, err := s.GetSystemValue(serviceIDMigrationKey)
	if err != nil {
		return report, err
	}
	if done == "1" {
		return report, nil
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return report, fmt.Errorf("failed to begin service ID migration transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Same advisory lock as SaveConfig: serializes against concurrent admin
	// saves and concurrent migration attempts from other nodes/goroutines.
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock($1, $2)", configSaveLockNamespace, configSaveLockKey); err != nil {
		return report, fmt.Errorf("failed to lock service ID migration transaction: %w", err)
	}

	done, err = getSystemValueTx(tx, serviceIDMigrationKey)
	if err != nil {
		return report, err
	}
	if done == "1" {
		// Another node/goroutine finished between the fast path and the lock.
		_ = tx.Rollback()
		return report, nil
	}

	cfg, found, err := getActiveConfigTx(tx)
	if err != nil {
		return report, err
	}
	if !found {
		// Fresh install: nothing to remap.
		if err = setSystemValueTx(tx, serviceIDMigrationKey, "1"); err != nil {
			return report, err
		}
		if err = tx.Commit(); err != nil {
			return report, fmt.Errorf("failed to commit service ID migration: %w", err)
		}
		return report, nil
	}

	idMap := make(map[string]string)
	for i := range cfg.Services {
		if isLegacyUUID(cfg.Services[i].ID) {
			newID := model.NewId()
			idMap[cfg.Services[i].ID] = newID
			cfg.Services[i].ID = newID
		}
	}

	if len(idMap) == 0 {
		// Content-based no-op (also covers re-run after a crash between the
		// config write and the marker write).
		if err = setSystemValueTx(tx, serviceIDMigrationKey, "1"); err != nil {
			return report, err
		}
		if err = tx.Commit(); err != nil {
			return report, fmt.Errorf("failed to commit service ID migration: %w", err)
		}
		return report, nil
	}

	for i := range cfg.Services {
		fb := cfg.Services[i].FallbackServiceID
		if fb == "" {
			continue
		}
		if newID, ok := idMap[fb]; ok {
			cfg.Services[i].FallbackServiceID = newID
		} else if isLegacyUUID(fb) {
			report.DanglingServiceRefs = append(report.DanglingServiceRefs,
				fmt.Sprintf("service %q fallback references unknown service %q", cfg.Services[i].ID, fb))
		}
	}

	for i := range cfg.Bots {
		sid := cfg.Bots[i].ServiceID
		if sid == "" {
			continue
		}
		if newID, ok := idMap[sid]; ok {
			cfg.Bots[i].ServiceID = newID
		} else if isLegacyUUID(sid) {
			report.DanglingServiceRefs = append(report.DanglingServiceRefs,
				fmt.Sprintf("config bot %q references unknown service %q", cfg.Bots[i].ID, sid))
		}
	}

	if err = insertActiveConfigTx(tx, *cfg); err != nil {
		return report, err
	}

	// Update every agent row, including soft-deleted ones. UpdateAt is
	// deliberately not bumped: this is a data migration, not a user edit.
	for oldID, newID := range idMap {
		var res sql.Result
		res, err = tx.Exec("UPDATE Agents_UserAgents SET ServiceID = $1 WHERE ServiceID = $2", newID, oldID)
		if err != nil {
			return report, fmt.Errorf("failed to update agent service IDs: %w", err)
		}
		var rows int64
		rows, err = res.RowsAffected()
		if err != nil {
			return report, fmt.Errorf("failed to count updated agent rows: %w", err)
		}
		report.AgentRowsUpdated += rows
	}

	// Any agent row still holding a UUID references a service that no longer
	// exists in the active config; report it, leave it unchanged.
	var remaining []string
	if err = tx.Select(&remaining, "SELECT DISTINCT ServiceID FROM Agents_UserAgents WHERE LENGTH(ServiceID) = 36"); err != nil {
		return report, fmt.Errorf("failed to check for dangling agent service IDs: %w", err)
	}
	for _, sid := range remaining {
		if isLegacyUUID(sid) {
			report.DanglingServiceRefs = append(report.DanglingServiceRefs,
				fmt.Sprintf("agent rows reference unknown service %q", sid))
		}
	}

	if err = setSystemValueTx(tx, serviceIDMigrationKey, "1"); err != nil {
		return report, err
	}
	if err = tx.Commit(); err != nil {
		return report, fmt.Errorf("failed to commit service ID migration: %w", err)
	}

	report.Migrated = true
	report.ServicesRemapped = len(idMap)
	return report, nil
}

// MigrateMCPServerIDs assigns a stable model.NewId() to every external MCP
// server entry in the active config that has no ID yet, writing one new
// active config-history row. Idempotent via marker + content check, same
// tx/lock pattern as MigrateServiceIDs. Returns whether a config write happened.
func (s *Store) MigrateMCPServerIDs() (bool, error) {
	done, err := s.GetSystemValue(mcpServerIDMigrationKey)
	if err != nil {
		return false, err
	}
	if done == "1" {
		return false, nil
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return false, fmt.Errorf("failed to begin MCP server ID migration transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec("SELECT pg_advisory_xact_lock($1, $2)", configSaveLockNamespace, configSaveLockKey); err != nil {
		return false, fmt.Errorf("failed to lock MCP server ID migration transaction: %w", err)
	}

	done, err = getSystemValueTx(tx, mcpServerIDMigrationKey)
	if err != nil {
		return false, err
	}
	if done == "1" {
		// Another node/goroutine finished between the fast path and the lock.
		_ = tx.Rollback()
		return false, nil
	}

	cfg, found, err := getActiveConfigTx(tx)
	if err != nil {
		return false, err
	}

	assigned := 0
	if found {
		for i := range cfg.MCP.Servers {
			if cfg.MCP.Servers[i].ID == "" {
				cfg.MCP.Servers[i].ID = model.NewId()
				assigned++
			}
		}
	}

	if !found || assigned == 0 {
		if err = setSystemValueTx(tx, mcpServerIDMigrationKey, "1"); err != nil {
			return false, err
		}
		if err = tx.Commit(); err != nil {
			return false, fmt.Errorf("failed to commit MCP server ID migration: %w", err)
		}
		return false, nil
	}

	if err = insertActiveConfigTx(tx, *cfg); err != nil {
		return false, err
	}
	if err = setSystemValueTx(tx, mcpServerIDMigrationKey, "1"); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit MCP server ID migration: %w", err)
	}

	return true, nil
}

// getSystemValueTx reads an Agents_System value on the transaction.
func getSystemValueTx(tx *sqlx.Tx, key string) (string, error) {
	var value string
	err := tx.Get(&value, "SELECT SValue FROM Agents_System WHERE SKey = $1", key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get system value for key %q: %w", key, err)
	}
	return value, nil
}

// setSystemValueTx upserts an Agents_System value on the transaction.
func setSystemValueTx(tx *sqlx.Tx, key, value string) error {
	_, err := tx.Exec(
		`INSERT INTO Agents_System (SKey, SValue) VALUES ($1, $2)
		 ON CONFLICT (SKey) DO UPDATE SET SValue = $2`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("failed to set system value for key %q: %w", key, err)
	}
	return nil
}

// getActiveConfigTx reads the active config row on the transaction.
// Returns found == false when no active config exists (fresh install).
func getActiveConfigTx(tx *sqlx.Tx) (*config.Config, bool, error) {
	var configJSON string
	err := tx.Get(&configJSON, "SELECT Config FROM Agents_ConfigHistory WHERE Active = true LIMIT 1")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to get active config: %w", err)
	}

	var cfg config.Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return &cfg, true, nil
}

// insertActiveConfigTx deactivates the current active config row and inserts
// cfg as the new active row, on the transaction. Same statements as SaveConfig.
func insertActiveConfigTx(tx *sqlx.Tx, cfg config.Config) error {
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

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
