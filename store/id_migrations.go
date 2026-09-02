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

// Agents_System marker for the one-time ABAC ID migration (value "1").
const abacIDMigrationKey = "abac_id_migration_done"

// isLegacyUUID reports whether s is a canonical dashed UUID, the legacy
// service-ID format that predates model.NewId()-style IDs.
func isLegacyUUID(s string) bool {
	return len(s) == 36 && uuid.Validate(s) == nil
}

// ABACIDMigrationReport summarizes what MigrateABACIDs did so the caller
// (server layer) can log it; the store has no logger.
type ABACIDMigrationReport struct {
	// Migrated is true when a new active config row was written.
	Migrated bool
	// ServicesRemapped counts service entries whose ID was rewritten,
	// including each occurrence of a duplicated legacy ID.
	ServicesRemapped                int
	AgentRowsUpdated                int64
	MCPServerIDsAssigned            int
	EmbeddedPluginServerIDsAssigned int
	DanglingServiceRefs             []string
}

// MigrateABACIDs runs the one-time ABAC ID migration in a single transaction
// that writes at most one new active config row and sets the Agents_System
// marker atomically:
//
//   - service IDs: legacy UUID service IDs in the active config and in
//     Agents_UserAgents.ServiceID are rewritten to model.NewId() values.
//     Dangling UUID references are left unchanged and reported.
//   - MCP server IDs: every external MCP server entry with no ID gets a
//     stable model.NewId().
//   - embedded/plugin MCP server IDs: EmbeddedServer.ID and every
//     PluginServers entry with no ID get a stable model.NewId().
//
// Idempotent: the marker short-circuits re-runs, and the rewrite is content-based
// (only UUID service IDs / empty MCP IDs are touched). Config and marker writes
// share one Postgres transaction; on failure the transaction is rolled back.
func (s *Store) MigrateABACIDs() (ABACIDMigrationReport, error) {
	report := ABACIDMigrationReport{}

	// Fast path; the authoritative re-check happens inside the tx under the lock.
	done, err := s.GetSystemValue(abacIDMigrationKey)
	if err != nil {
		return report, err
	}
	if done == "1" {
		return report, nil
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return report, fmt.Errorf("failed to begin ABAC ID migration transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Same advisory lock as SaveConfig: serializes against concurrent admin
	// saves and migration attempts from other nodes.
	if _, err = tx.Exec("SELECT pg_advisory_xact_lock($1, $2)", configSaveLockNamespace, configSaveLockKey); err != nil {
		return report, fmt.Errorf("failed to lock ABAC ID migration transaction: %w", err)
	}

	done, err = getSystemValueTx(tx, abacIDMigrationKey)
	if err != nil {
		return report, err
	}
	if done == "1" {
		// Another node finished between the fast path and the lock.
		_ = tx.Rollback()
		return report, nil
	}

	cfg, found, err := getActiveConfigTx(tx)
	if err != nil {
		return report, err
	}

	configChanged := false
	if found {
		if err = migrateServiceIDsTx(tx, cfg, &report); err != nil {
			return report, err
		}
		configChanged = configChanged || report.ServicesRemapped > 0

		for i := range cfg.MCP.Servers {
			if cfg.MCP.Servers[i].ID == "" {
				cfg.MCP.Servers[i].ID = model.NewId()
				report.MCPServerIDsAssigned++
			}
		}
		configChanged = configChanged || report.MCPServerIDsAssigned > 0

		if cfg.MCP.EmbeddedServer.ID == "" {
			cfg.MCP.EmbeddedServer.ID = model.NewId()
			report.EmbeddedPluginServerIDsAssigned++
		}
		for i := range cfg.MCP.PluginServers {
			if cfg.MCP.PluginServers[i].ID == "" {
				cfg.MCP.PluginServers[i].ID = model.NewId()
				report.EmbeddedPluginServerIDsAssigned++
			}
		}
		configChanged = configChanged || report.EmbeddedPluginServerIDsAssigned > 0
	}

	if configChanged {
		if err = insertActiveConfigTx(tx, *cfg); err != nil {
			return report, err
		}
	}
	if err = setSystemValueTx(tx, abacIDMigrationKey, "1"); err != nil {
		return report, err
	}
	if err = tx.Commit(); err != nil {
		return report, fmt.Errorf("failed to commit ABAC ID migration: %w", err)
	}

	report.Migrated = configChanged
	return report, nil
}

// migrateServiceIDsTx rewrites legacy UUID service IDs in cfg (in place) and
// in Agents_UserAgents rows, recording what it did in report. It does not
// write the config row; the caller commits everything in one transaction.
func migrateServiceIDsTx(tx *sqlx.Tx, cfg *config.Config, report *ABACIDMigrationReport) error {
	// Duplicate legacy UUIDs: each occurrence gets its own new ID, but
	// references remap to the FIRST occurrence's, mirroring GetServiceByID.
	idMap := make(map[string]string)
	for i := range cfg.Services {
		oldID := cfg.Services[i].ID
		if !isLegacyUUID(oldID) {
			continue
		}
		newID := model.NewId()
		if _, seen := idMap[oldID]; !seen {
			idMap[oldID] = newID
		}
		cfg.Services[i].ID = newID
		report.ServicesRemapped++
	}

	// Even when no service ID was remapped, keep going: dangling UUID
	// references (fallbacks, bots, agent rows pointing at services absent
	// from the active config) must still be detected and reported, or the
	// one-time marker would permanently suppress the diagnosis.
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

	// Update every agent row, including soft-deleted ones. UpdateAt is
	// deliberately not bumped (data migration, not a user edit).
	for oldID, newID := range idMap {
		res, err := tx.Exec("UPDATE Agents_UserAgents SET ServiceID = $1 WHERE ServiceID = $2", newID, oldID)
		if err != nil {
			return fmt.Errorf("failed to update agent service IDs: %w", err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to count updated agent rows: %w", err)
		}
		report.AgentRowsUpdated += rows
	}

	// Any agent row still holding a UUID references a service that no longer
	// exists in the active config; report it, leave it unchanged.
	var remaining []string
	if err := tx.Select(&remaining, "SELECT DISTINCT ServiceID FROM Agents_UserAgents WHERE LENGTH(ServiceID) = 36"); err != nil {
		return fmt.Errorf("failed to check for dangling agent service IDs: %w", err)
	}
	for _, sid := range remaining {
		if isLegacyUUID(sid) {
			report.DanglingServiceRefs = append(report.DanglingServiceRefs,
				fmt.Sprintf("agent rows reference unknown service %q", sid))
		}
	}
	return nil
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
