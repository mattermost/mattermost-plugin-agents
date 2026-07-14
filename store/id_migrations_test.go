// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testUUIDA = "550e8400-e29b-41d4-a716-446655440000"
	testUUIDB = "550e8400-e29b-41d4-a716-446655440001"
	testUUIDC = "550e8400-e29b-41d4-a716-446655440002"
	// Valid UUID deliberately absent from every seeded service list.
	testUUIDDangling = "550e8400-e29b-41d4-a716-446655449999"
)

// seedConfigRow inserts a config history row directly, bypassing SaveConfig.
func seedConfigRow(t *testing.T, s *Store, cfg config.Config, active bool) {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	_, err = s.db.Exec(
		"INSERT INTO Agents_ConfigHistory (ID, Config, CreateAt, Active) VALUES ($1, $2, $3, $4)",
		model.NewId(), string(data), model.GetMillis(), active,
	)
	require.NoError(t, err)
}

// seedAgentRow inserts an agent row directly so arbitrary ServiceIDs,
// soft-deletion, and timestamps can be seeded without CreateAgent's ID reset.
func seedAgentRow(t *testing.T, s *Store, id, serviceID string, updateAt, deleteAt int64) {
	t.Helper()
	_, err := s.db.Exec(
		`INSERT INTO Agents_UserAgents (
			ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CreateAt, UpdateAt, DeleteAt
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, "bot-"+id, "creator", "Agent "+id, "agent-"+id, serviceID,
		int64(1), updateAt, deleteAt,
	)
	require.NoError(t, err)
}

func configHistoryCount(t *testing.T, s *Store) int {
	t.Helper()
	var count int
	require.NoError(t, s.db.Get(&count, "SELECT COUNT(*) FROM Agents_ConfigHistory"))
	return count
}

func requireMarker(t *testing.T, s *Store, key string) {
	t.Helper()
	marker, err := s.GetSystemValue(key)
	require.NoError(t, err)
	assert.Equal(t, "1", marker, "marker %q must be set", key)
}

type agentServiceRow struct {
	ServiceID string `db:"serviceid"`
	UpdateAt  int64  `db:"updateat"`
}

func getAgentServiceRow(t *testing.T, s *Store, id string) agentServiceRow {
	t.Helper()
	var row agentServiceRow
	require.NoError(t, s.db.Get(&row, "SELECT ServiceID, UpdateAt FROM Agents_UserAgents WHERE ID = $1", id))
	return row
}

func TestMigrateABACIDs(t *testing.T) {
	tests := []struct {
		name     string
		seed     func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store, report ABACIDMigrationReport)
	}{
		{
			name: "all legacy UUIDs remapped",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDA, Name: "A"},
						{ID: testUUIDB, Name: "B"},
					},
				}, true)
				seedAgentRow(t, s, "agent1", testUUIDA, 100, 0)
				seedAgentRow(t, s, "agent2", testUUIDB, 200, 0)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)
				assert.Equal(t, 2, report.ServicesRemapped)
				assert.Equal(t, int64(2), report.AgentRowsUpdated)
				assert.Empty(t, report.DanglingServiceRefs)

				cfg, err := s.GetConfig()
				require.NoError(t, err)
				byName := map[string]string{}
				for _, svc := range cfg.Services {
					assert.True(t, model.IsValidId(svc.ID), "service %q ID %q should be a valid Mattermost ID", svc.Name, svc.ID)
					byName[svc.Name] = svc.ID
				}
				assert.Equal(t, byName["A"], getAgentServiceRow(t, s, "agent1").ServiceID)
				assert.Equal(t, byName["B"], getAgentServiceRow(t, s, "agent2").ServiceID)
			},
		},
		{
			name: "mixed IDs only UUIDs rewritten",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDA, Name: "legacy"},
						{ID: "kept26charidkept26charidke", Name: "modern"},
					},
				}, true)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)
				assert.Equal(t, 1, report.ServicesRemapped)

				cfg, err := s.GetConfig()
				require.NoError(t, err)
				byName := map[string]string{}
				for _, svc := range cfg.Services {
					byName[svc.Name] = svc.ID
				}
				assert.Equal(t, "kept26charidkept26charidke", byName["modern"], "non-UUID ID must be byte-identical")
				assert.NotEqual(t, testUUIDA, byName["legacy"])
				assert.True(t, model.IsValidId(byName["legacy"]))
			},
		},
		{
			name: "fallback reference chain remapped",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDA, Name: "A", FallbackServiceID: testUUIDB},
						{ID: testUUIDB, Name: "B", FallbackServiceID: testUUIDC},
						{ID: testUUIDC, Name: "C"},
					},
				}, true)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)
				assert.Equal(t, 3, report.ServicesRemapped)
				assert.Empty(t, report.DanglingServiceRefs)

				cfg, err := s.GetConfig()
				require.NoError(t, err)
				byName := map[string]llm.ServiceConfig{}
				for _, svc := range cfg.Services {
					byName[svc.Name] = svc
				}
				assert.Equal(t, byName["B"].ID, byName["A"].FallbackServiceID)
				assert.Equal(t, byName["C"].ID, byName["B"].FallbackServiceID)
				assert.Empty(t, byName["C"].FallbackServiceID)
			},
		},
		{
			name: "config bot references remapped",
			seed: func(t *testing.T, s *Store) {
				// Pre-legacy-bot-migration state: bots still live in config.
				// Proves the migration handles the state reachable only when it
				// runs before migrateLegacyConfigBotsToUserAgents.
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDA, Name: "A"},
					},
					Bots: []llm.BotConfig{
						{ID: "bot1", Name: "ai", ServiceID: testUUIDA},
					},
				}, true)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)

				cfg, err := s.GetConfig()
				require.NoError(t, err)
				require.Len(t, cfg.Bots, 1)
				assert.Equal(t, cfg.Services[0].ID, cfg.Bots[0].ServiceID)
				assert.True(t, model.IsValidId(cfg.Bots[0].ServiceID))
			},
		},
		{
			name: "duplicate legacy IDs: unique new IDs, references keep first occurrence",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDA, Name: "first"},
						{ID: testUUIDA, Name: "second"},
						{ID: testUUIDB, Name: "other", FallbackServiceID: testUUIDA},
					},
					Bots: []llm.BotConfig{
						{ID: "bot1", Name: "ai", ServiceID: testUUIDA},
					},
				}, true)
				seedAgentRow(t, s, "agent1", testUUIDA, 100, 0)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)
				assert.Equal(t, 3, report.ServicesRemapped, "every occurrence counts, including duplicates")
				assert.Empty(t, report.DanglingServiceRefs)

				cfg, err := s.GetConfig()
				require.NoError(t, err)
				byName := map[string]llm.ServiceConfig{}
				for _, svc := range cfg.Services {
					assert.True(t, model.IsValidId(svc.ID))
					byName[svc.Name] = svc
				}
				assert.NotEqual(t, byName["first"].ID, byName["second"].ID,
					"duplicated services must each get their own unique new ID")

				// GetServiceByID first-match semantics: all references follow
				// the FIRST occurrence's new ID.
				firstID := byName["first"].ID
				assert.Equal(t, firstID, byName["other"].FallbackServiceID)
				assert.Equal(t, firstID, cfg.Bots[0].ServiceID)
				assert.Equal(t, firstID, getAgentServiceRow(t, s, "agent1").ServiceID)
			},
		},
		{
			name: "active and soft-deleted agents both updated without UpdateAt bump",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDA, Name: "A"},
					},
				}, true)
				seedAgentRow(t, s, "active-agent", testUUIDA, 111, 0)
				seedAgentRow(t, s, "deleted-agent", testUUIDA, 222, 999)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)
				assert.Equal(t, int64(2), report.AgentRowsUpdated)

				cfg, err := s.GetConfig()
				require.NoError(t, err)
				newID := cfg.Services[0].ID

				activeRow := getAgentServiceRow(t, s, "active-agent")
				assert.Equal(t, newID, activeRow.ServiceID)
				assert.Equal(t, int64(111), activeRow.UpdateAt)

				deletedRow := getAgentServiceRow(t, s, "deleted-agent")
				assert.Equal(t, newID, deletedRow.ServiceID)
				assert.Equal(t, int64(222), deletedRow.UpdateAt)
			},
		},
		{
			name: "dangling references left unchanged and reported",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDA, Name: "A", FallbackServiceID: testUUIDDangling},
					},
					Bots: []llm.BotConfig{
						{ID: "bot1", Name: "ai", ServiceID: testUUIDDangling},
					},
				}, true)
				seedAgentRow(t, s, "dangling-agent", testUUIDDangling, 100, 0)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)
				assert.Equal(t, 1, report.ServicesRemapped)
				assert.Equal(t, int64(0), report.AgentRowsUpdated)
				assert.Len(t, report.DanglingServiceRefs, 3)

				cfg, err := s.GetConfig()
				require.NoError(t, err)
				assert.Equal(t, testUUIDDangling, cfg.Services[0].FallbackServiceID)
				assert.Equal(t, testUUIDDangling, cfg.Bots[0].ServiceID)
				assert.Equal(t, testUUIDDangling, getAgentServiceRow(t, s, "dangling-agent").ServiceID)
			},
		},
		{
			name: "MCP servers get IDs only when missing, other fields preserved",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					MCP: config.MCPConfig{
						Enabled: true,
						Servers: []config.MCPServerConfig{
							{
								Name:     "no-id",
								Enabled:  true,
								BaseURL:  "https://one.example.com",
								Headers:  map[string]string{"Authorization": "Bearer x"},
								ClientID: "client-1",
								ToolConfigs: []config.MCPToolConfig{
									{Name: "get_issue", Policy: config.MCPToolPolicyAsk, Enabled: true},
								},
							},
							{
								ID:      "existing26charidexisting26",
								Name:    "has-id",
								Enabled: false,
								BaseURL: "https://two.example.com",
							},
						},
						PluginServers: []config.PluginServerConfig{
							{PluginID: "com.example.plugin", Name: "Plugin Server", Enabled: true},
						},
						EmbeddedServer: config.MCPEmbeddedServerConfig{Enabled: true},
					},
				}, true)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)
				assert.Equal(t, 1, report.MCPServerIDsAssigned)

				cfg, err := s.GetConfig()
				require.NoError(t, err)
				require.Len(t, cfg.MCP.Servers, 2)

				noID := cfg.MCP.Servers[0]
				assert.True(t, model.IsValidId(noID.ID))
				assert.Equal(t, "no-id", noID.Name)
				assert.Equal(t, "https://one.example.com", noID.BaseURL)
				assert.Equal(t, map[string]string{"Authorization": "Bearer x"}, noID.Headers)
				assert.Equal(t, "client-1", noID.ClientID)
				require.Len(t, noID.ToolConfigs, 1)
				assert.Equal(t, "get_issue", noID.ToolConfigs[0].Name)

				hasID := cfg.MCP.Servers[1]
				assert.Equal(t, "existing26charidexisting26", hasID.ID, "pre-existing ID must be preserved")

				require.Len(t, cfg.MCP.PluginServers, 1)
				assert.Equal(t, "com.example.plugin", cfg.MCP.PluginServers[0].PluginID)
				assert.True(t, cfg.MCP.EmbeddedServer.Enabled)
			},
		},
		{
			name: "service and MCP migrations commit as one config row",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDA, Name: "A"},
					},
					MCP: config.MCPConfig{
						Servers: []config.MCPServerConfig{
							{Name: "srv", BaseURL: "https://one.example.com"},
						},
					},
				}, true)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)
				assert.Equal(t, 1, report.ServicesRemapped)
				assert.Equal(t, 1, report.MCPServerIDsAssigned)
				assert.Equal(t, 2, configHistoryCount(t, s),
					"both migrations must write exactly one new config row together")

				cfg, err := s.GetConfig()
				require.NoError(t, err)
				assert.True(t, model.IsValidId(cfg.Services[0].ID))
				assert.True(t, model.IsValidId(cfg.MCP.Servers[0].ID))
				requireMarker(t, s, serviceIDMigrationKey)
				requireMarker(t, s, mcpServerIDMigrationKey)
			},
		},
		{
			name: "idempotency second run is a no-op",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDA, Name: "A"},
					},
					MCP: config.MCPConfig{
						Servers: []config.MCPServerConfig{
							{Name: "srv", BaseURL: "https://one.example.com"},
						},
					},
				}, true)
				report, err := s.MigrateABACIDs()
				require.NoError(t, err)
				require.True(t, report.Migrated)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.False(t, report.Migrated)
				assert.Zero(t, report.ServicesRemapped)
				assert.Zero(t, report.MCPServerIDsAssigned)
				assert.Equal(t, 2, configHistoryCount(t, s), "second run must not write another config row")
			},
		},
		{
			name: "partial markers: only the unfinished migration runs",
			seed: func(t *testing.T, s *Store) {
				// Service migration already done: its marker is set and the
				// active config carries a migrated (modern) service ID, per
				// the invariant the atomic migration commit guarantees.
				require.NoError(t, s.SetSystemValue(serviceIDMigrationKey, "1"))
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: "migrated26charidmigrated26", Name: "A"},
					},
					MCP: config.MCPConfig{
						Servers: []config.MCPServerConfig{
							{Name: "srv", BaseURL: "https://one.example.com"},
						},
					},
				}, true)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)
				assert.Zero(t, report.ServicesRemapped, "service migration already marked done")
				assert.Equal(t, 1, report.MCPServerIDsAssigned)

				cfg, err := s.GetConfig()
				require.NoError(t, err)
				assert.Equal(t, "migrated26charidmigrated26", cfg.Services[0].ID, "service IDs untouched when marker already set")
				assert.True(t, model.IsValidId(cfg.MCP.Servers[0].ID))
				requireMarker(t, s, mcpServerIDMigrationKey)
			},
		},
		{
			name: "content-based no-op sets markers without config write",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: model.NewId(), Name: "modern"},
					},
					MCP: config.MCPConfig{
						Servers: []config.MCPServerConfig{
							{ID: model.NewId(), Name: "srv", BaseURL: "https://one.example.com"},
						},
					},
				}, true)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.False(t, report.Migrated)
				assert.Equal(t, 1, configHistoryCount(t, s))
				requireMarker(t, s, serviceIDMigrationKey)
				requireMarker(t, s, mcpServerIDMigrationKey)
			},
		},
		{
			name: "no active config sets markers without error",
			seed: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.False(t, report.Migrated)
				assert.Zero(t, configHistoryCount(t, s))
				requireMarker(t, s, serviceIDMigrationKey)
				requireMarker(t, s, mcpServerIDMigrationKey)
			},
		},
		{
			name: "inactive history rows unchanged",
			seed: func(t *testing.T, s *Store) {
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDB, Name: "old-snapshot"},
					},
				}, false)
				seedConfigRow(t, s, config.Config{
					Services: []llm.ServiceConfig{
						{ID: testUUIDA, Name: "A"},
					},
				}, true)
			},
			validate: func(t *testing.T, s *Store, report ABACIDMigrationReport) {
				assert.True(t, report.Migrated)

				var inactiveConfigs []string
				require.NoError(t, s.db.Select(&inactiveConfigs, "SELECT Config FROM Agents_ConfigHistory WHERE Active = false ORDER BY CreateAt"))
				// The pre-seeded inactive row plus the row deactivated by the migration.
				require.Len(t, inactiveConfigs, 2)
				assert.Contains(t, inactiveConfigs[0], testUUIDB, "pre-existing inactive row must keep its UUIDs")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())

			tt.seed(t, s)

			report, err := s.MigrateABACIDs()
			require.NoError(t, err)

			tt.validate(t, s, report)
		})
	}
}

func TestMigrateABACIDsAtomicRollback(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(t *testing.T, s *Store)
	}{
		{
			name: "agent table missing rolls back config write and markers",
			corrupt: func(t *testing.T, s *Store) {
				_, err := s.db.Exec("DROP TABLE Agents_UserAgents")
				require.NoError(t, err)
			},
		},
		{
			name: "corrupt active config JSON fails without writes",
			corrupt: func(t *testing.T, s *Store) {
				_, err := s.db.Exec("UPDATE Agents_ConfigHistory SET Config = 'not-json' WHERE Active = true")
				require.NoError(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())
			seedConfigRow(t, s, config.Config{
				Services: []llm.ServiceConfig{
					{ID: testUUIDA, Name: "A"},
				},
				MCP: config.MCPConfig{
					Servers: []config.MCPServerConfig{
						{Name: "srv", BaseURL: "https://one.example.com"},
					},
				},
			}, true)
			seedAgentRow(t, s, "agent1", testUUIDA, 100, 0)

			tt.corrupt(t, s)

			_, err := s.MigrateABACIDs()
			require.Error(t, err)

			for _, key := range []string{serviceIDMigrationKey, mcpServerIDMigrationKey} {
				marker, markerErr := s.GetSystemValue(key)
				require.NoError(t, markerErr)
				assert.Empty(t, marker, "marker %q must not be set on failure", key)
			}

			assert.Equal(t, 1, configHistoryCount(t, s), "no new config row on failure")

			var activeConfig string
			require.NoError(t, s.db.Get(&activeConfig, "SELECT Config FROM Agents_ConfigHistory WHERE Active = true"))
			if activeConfig != "not-json" {
				assert.Contains(t, activeConfig, testUUIDA, "active config must keep its UUIDs on rollback")
			}
		})
	}
}

func TestMigrateABACIDsConcurrentIdempotent(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())
	seedConfigRow(t, s, config.Config{
		Services: []llm.ServiceConfig{
			{ID: testUUIDA, Name: "A"},
			{ID: testUUIDB, Name: "B"},
		},
		MCP: config.MCPConfig{
			Servers: []config.MCPServerConfig{
				{Name: "srv", BaseURL: "https://one.example.com"},
			},
		},
	}, true)
	seedAgentRow(t, s, "agent1", testUUIDA, 100, 0)

	const goroutines = 8
	reports := make([]ABACIDMigrationReport, goroutines)
	errs := make([]error, goroutines)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reports[idx], errs[idx] = s.MigrateABACIDs()
		}(i)
	}
	wg.Wait()

	migratedCount := 0
	for i := 0; i < goroutines; i++ {
		require.NoError(t, errs[i])
		if reports[i].Migrated {
			migratedCount++
		}
	}
	assert.Equal(t, 1, migratedCount, "exactly one goroutine must perform the migration")
	assert.Equal(t, 2, configHistoryCount(t, s), "exactly one new config row")

	requireMarker(t, s, serviceIDMigrationKey)
	requireMarker(t, s, mcpServerIDMigrationKey)
}

// TestUpdateConfigRejectsStaleLegacyServiceIDs is the interleaving regression
// for the migration/stale-save race: once the service ID migration has run, a
// save echoing pre-migration UUID service IDs (stale webapp bundle) must be
// rejected, while a fresh payload goes through.
func TestUpdateConfigRejectsStaleLegacyServiceIDs(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())

	// Stale client loads the pre-migration config...
	seedConfigRow(t, s, config.Config{
		Services: []llm.ServiceConfig{
			{ID: testUUIDA, Name: "A"},
		},
	}, true)
	staleSnapshot, err := s.GetConfig()
	require.NoError(t, err)

	// ...then the migration runs.
	report, err := s.MigrateABACIDs()
	require.NoError(t, err)
	require.True(t, report.Migrated)
	migratedCfg, err := s.GetConfig()
	require.NoError(t, err)
	migratedID := migratedCfg.Services[0].ID
	require.True(t, model.IsValidId(migratedID))

	// Stale save replays the UUID payload: rejected, active config untouched.
	rowsBefore := configHistoryCount(t, s)
	_, err = s.UpdateConfig(func(prev *config.Config) (config.Config, error) {
		return *staleSnapshot, nil
	})
	require.ErrorIs(t, err, ErrStaleLegacyServiceIDs)
	assert.Equal(t, rowsBefore, configHistoryCount(t, s), "rejected save must not write a config row")
	current, err := s.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, migratedID, current.Services[0].ID, "migrated ID must survive the stale save")

	// A fresh payload (post-migration IDs) is accepted.
	fresh := *migratedCfg
	fresh.Services[0].Name = "A-renamed"
	saved, err := s.UpdateConfig(func(prev *config.Config) (config.Config, error) {
		require.NotNil(t, prev)
		assert.Equal(t, migratedID, prev.Services[0].ID, "transform must see the migrated config")
		return fresh, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "A-renamed", saved.Services[0].Name)
	current, err = s.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, "A-renamed", current.Services[0].Name)
	assert.Equal(t, migratedID, current.Services[0].ID)
}

// Before the migration marker is set, UpdateConfig must accept UUID service
// IDs: pre-migration saves are legitimate.
func TestUpdateConfigAllowsUUIDsBeforeMigration(t *testing.T) {
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())

	saved, err := s.UpdateConfig(func(prev *config.Config) (config.Config, error) {
		assert.Nil(t, prev, "no active config yet")
		return config.Config{
			Services: []llm.ServiceConfig{{ID: testUUIDA, Name: "A"}},
		}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, testUUIDA, saved.Services[0].ID)

	current, err := s.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, testUUIDA, current.Services[0].ID)
}
