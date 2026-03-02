// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/embeddings"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullTestConfig returns a fully populated config for round-trip testing.
func fullTestConfig() config.Config {
	return config.Config{
		Services: []llm.ServiceConfig{
			{
				ID:                      "svc-1",
				Name:                    "OpenAI GPT-4",
				Type:                    "openai",
				APIKey:                  "sk-test-key-12345",
				OrgID:                   "org-abc",
				DefaultModel:            "gpt-4",
				APIURL:                  "https://api.openai.com/v1",
				InputTokenLimit:         32768,
				StreamingTimeoutSeconds: 30,
				SendUserID:              true,
				OutputTokenLimit:        4096,
				UseResponsesAPI:         false,
			},
			{
				ID:               "svc-2",
				Name:             "Anthropic Claude",
				Type:             "anthropic",
				APIKey:           "sk-ant-key-67890",
				DefaultModel:     "claude-3-5-sonnet-20241022",
				InputTokenLimit:  100000,
				OutputTokenLimit: 8192,
				SendUserID:       false,
				UseResponsesAPI:  false,
			},
		},
		Bots: []llm.BotConfig{
			{
				ID:                 "bot-1",
				Name:               "ai",
				DisplayName:        "AI Assistant",
				CustomInstructions: "Be helpful",
				ServiceID:          "svc-1",
				EnableVision:       true,
				DisableTools:       false,
			},
			{
				ID:          "bot-2",
				Name:        "claude",
				DisplayName: "Claude",
				ServiceID:   "svc-2",
			},
		},
		DefaultBotName:          "ai",
		TranscriptGenerator:     "openai",
		EnableLLMTrace:          true,
		EnableTokenUsageLogging: true,
		EmbeddingSearchConfig: embeddings.EmbeddingSearchConfig{
			Type: "openai",
		},
		MCP: mcp.Config{
			EmbeddedServer: mcp.EmbeddedServerConfig{
				Enabled: false,
			},
		},
		WebSearch: config.WebSearchConfig{
			Enabled:  true,
			Provider: "google",
			Google: config.WebSearchGoogleConfig{
				APIKey:         "google-api-key",
				SearchEngineID: "cx-123",
				ResultLimit:    5,
			},
		},
	}
}

func TestConfigStore(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store)
	}{
		{
			name:  "get on empty DB returns nil",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				cfg, err := s.GetConfig()
				require.NoError(t, err)
				assert.Nil(t, cfg)
			},
		},
		{
			name:  "IsConfigMigrated returns false on empty DB",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				migrated, err := s.IsConfigMigrated()
				require.NoError(t, err)
				assert.False(t, migrated)
			},
		},
		{
			name: "save and get round-trip with fully populated config",
			setup: func(t *testing.T, s *Store) {
				err := s.SaveConfig(fullTestConfig())
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				cfg, err := s.GetConfig()
				require.NoError(t, err)
				require.NotNil(t, cfg)

				expected := fullTestConfig()

				// Services
				require.Len(t, cfg.Services, 2)
				assert.Equal(t, expected.Services[0].ID, cfg.Services[0].ID)
				assert.Equal(t, expected.Services[0].APIKey, cfg.Services[0].APIKey)
				assert.Equal(t, expected.Services[0].Type, cfg.Services[0].Type)
				assert.Equal(t, expected.Services[0].DefaultModel, cfg.Services[0].DefaultModel)
				assert.Equal(t, expected.Services[0].InputTokenLimit, cfg.Services[0].InputTokenLimit)
				assert.Equal(t, expected.Services[1].ID, cfg.Services[1].ID)
				assert.Equal(t, expected.Services[1].APIKey, cfg.Services[1].APIKey)

				// Bots
				require.Len(t, cfg.Bots, 2)
				assert.Equal(t, expected.Bots[0].ID, cfg.Bots[0].ID)
				assert.Equal(t, expected.Bots[0].Name, cfg.Bots[0].Name)
				assert.Equal(t, expected.Bots[0].ServiceID, cfg.Bots[0].ServiceID)
				assert.Equal(t, expected.Bots[0].CustomInstructions, cfg.Bots[0].CustomInstructions)
				assert.Equal(t, expected.Bots[0].EnableVision, cfg.Bots[0].EnableVision)

				// Top-level fields
				assert.Equal(t, expected.DefaultBotName, cfg.DefaultBotName)
				assert.Equal(t, expected.TranscriptGenerator, cfg.TranscriptGenerator)
				assert.Equal(t, expected.EnableLLMTrace, cfg.EnableLLMTrace)
				assert.Equal(t, expected.EnableTokenUsageLogging, cfg.EnableTokenUsageLogging)

				// MCP
				assert.Equal(t, expected.MCP.EmbeddedServer.Enabled, cfg.MCP.EmbeddedServer.Enabled)

				// WebSearch
				assert.Equal(t, expected.WebSearch.Enabled, cfg.WebSearch.Enabled)
				assert.Equal(t, expected.WebSearch.Provider, cfg.WebSearch.Provider)
				assert.Equal(t, expected.WebSearch.Google.APIKey, cfg.WebSearch.Google.APIKey)
			},
		},
		{
			name: "IsConfigMigrated returns true after save",
			setup: func(t *testing.T, s *Store) {
				err := s.SaveConfig(fullTestConfig())
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				migrated, err := s.IsConfigMigrated()
				require.NoError(t, err)
				assert.True(t, migrated)
			},
		},
		{
			name: "overwrite existing config",
			setup: func(t *testing.T, s *Store) {
				err := s.SaveConfig(fullTestConfig())
				require.NoError(t, err)

				// Save a different config
				newCfg := config.Config{
					DefaultBotName: "new-bot",
					Bots: []llm.BotConfig{
						{ID: "new-bot-1", Name: "new-bot", ServiceID: "svc-new"},
					},
				}
				err = s.SaveConfig(newCfg)
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				cfg, err := s.GetConfig()
				require.NoError(t, err)
				require.NotNil(t, cfg)
				assert.Equal(t, "new-bot", cfg.DefaultBotName)
				require.Len(t, cfg.Bots, 1)
				assert.Equal(t, "new-bot-1", cfg.Bots[0].ID)
			},
		},
		{
			name: "config with sensitive API keys preserved through round-trip",
			setup: func(t *testing.T, s *Store) {
				cfg := config.Config{
					Services: []llm.ServiceConfig{
						{
							ID:     "svc-secret",
							Type:   "openai",
							APIKey: "sk-very-secret-key-that-must-not-be-lost",
						},
					},
					WebSearch: config.WebSearchConfig{
						Enabled:  true,
						Provider: "google",
						Google: config.WebSearchGoogleConfig{
							APIKey: "AIzaSy-google-secret-key",
						},
						Brave: config.WebSearchBraveConfig{
							APIKey: "BSA-brave-secret-key",
						},
					},
				}
				err := s.SaveConfig(cfg)
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				cfg, err := s.GetConfig()
				require.NoError(t, err)
				require.NotNil(t, cfg)
				require.Len(t, cfg.Services, 1)
				assert.Equal(t, "sk-very-secret-key-that-must-not-be-lost", cfg.Services[0].APIKey)
				assert.Equal(t, "AIzaSy-google-secret-key", cfg.WebSearch.Google.APIKey)
				assert.Equal(t, "BSA-brave-secret-key", cfg.WebSearch.Brave.APIKey)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)

			err := s.RunMigrations()
			require.NoError(t, err)

			tt.setup(t, s)
			tt.validate(t, s)
		})
	}
}

func TestConfigHistory(t *testing.T) {
	s := setupTestStore(t)

	err := s.RunMigrations()
	require.NoError(t, err)

	// Save 3 configs sequentially
	configs := []config.Config{
		{DefaultBotName: "bot-v1"},
		{DefaultBotName: "bot-v2"},
		{DefaultBotName: "bot-v3"},
	}

	for _, cfg := range configs {
		saveErr := s.SaveConfig(cfg)
		require.NoError(t, saveErr)
	}

	// Verify only latest is active
	var activeCount int
	err = s.db.Get(&activeCount, "SELECT COUNT(*) FROM Agents_ConfigHistory WHERE Active = true")
	require.NoError(t, err)
	assert.Equal(t, 1, activeCount, "Exactly one config should be active")

	// Verify all 3 rows exist (history preserved)
	var totalCount int
	err = s.db.Get(&totalCount, "SELECT COUNT(*) FROM Agents_ConfigHistory")
	require.NoError(t, err)
	assert.Equal(t, 3, totalCount, "All 3 config versions should be preserved")

	// Verify GetConfig returns the latest
	cfg, err := s.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "bot-v3", cfg.DefaultBotName)
}

func TestConfigDataMigration(t *testing.T) {
	s := setupTestStore(t)

	err := s.RunMigrations()
	require.NoError(t, err)

	// Step 1: Verify IsConfigMigrated = false on fresh install
	migrated, err := s.IsConfigMigrated()
	require.NoError(t, err)
	assert.False(t, migrated, "Fresh install should not be migrated")

	// Step 2: Simulate writing config (as the activation code would)
	cfg := fullTestConfig()
	err = s.SaveConfig(cfg)
	require.NoError(t, err)

	// Step 3: Verify IsConfigMigrated = true
	migrated, err = s.IsConfigMigrated()
	require.NoError(t, err)
	assert.True(t, migrated, "Should be migrated after save")

	// Step 4: Verify GetConfig returns correct data
	loaded, err := s.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, cfg.DefaultBotName, loaded.DefaultBotName)
	require.Len(t, loaded.Services, 2)
	require.Len(t, loaded.Bots, 2)

	// Step 5: Re-running the check should skip migration
	migrated, err = s.IsConfigMigrated()
	require.NoError(t, err)
	assert.True(t, migrated, "Should still be migrated on re-check")
}

func setupSchemaBoundStore(t *testing.T, schemaName string) *Store {
	t.Helper()

	db, err := sqlx.Connect("postgres", testConnStr)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)

	_, err = db.Exec(fmt.Sprintf("SET search_path TO %s", schemaName))
	require.NoError(t, err)

	t.Cleanup(func() {
		db.Close()
	})

	return New(db)
}

func TestSaveConfigConcurrent(t *testing.T) {
	baseStore := setupTestStore(t)

	err := baseStore.RunMigrations()
	require.NoError(t, err)

	var schemaName string
	err = baseStore.db.Get(&schemaName, "SELECT current_schema()")
	require.NoError(t, err)

	const workerCount = 8
	workerStores := make([]*Store, workerCount)
	for i := 0; i < workerCount; i++ {
		workerStores[i] = setupSchemaBoundStore(t, schemaName)
	}

	start := make(chan struct{})
	errCh := make(chan error, workerCount)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func(index int, workerStore *Store) {
			defer wg.Done()
			<-start

			errCh <- workerStore.SaveConfig(config.Config{DefaultBotName: fmt.Sprintf("bot-%d", index)})
		}(i, workerStores[i])
	}

	close(start)
	wg.Wait()
	close(errCh)

	for saveErr := range errCh {
		require.NoError(t, saveErr)
	}

	var activeCount int
	err = baseStore.db.Get(&activeCount, "SELECT COUNT(*) FROM Agents_ConfigHistory WHERE Active = true")
	require.NoError(t, err)
	assert.Equal(t, 1, activeCount)

	var totalCount int
	err = baseStore.db.Get(&totalCount, "SELECT COUNT(*) FROM Agents_ConfigHistory")
	require.NoError(t, err)
	assert.Equal(t, workerCount, totalCount)
}

func TestSaveConfigWaitsForConfigLock(t *testing.T) {
	baseStore := setupTestStore(t)

	err := baseStore.RunMigrations()
	require.NoError(t, err)

	var schemaName string
	err = baseStore.db.Get(&schemaName, "SELECT current_schema()")
	require.NoError(t, err)

	lockStore := setupSchemaBoundStore(t, schemaName)
	saveStore := setupSchemaBoundStore(t, schemaName)

	lockTx, err := lockStore.db.Beginx()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = lockTx.Rollback()
	})

	_, err = lockTx.Exec("SELECT pg_advisory_xact_lock($1, $2)", configSaveLockNamespace, configSaveLockKey)
	require.NoError(t, err)

	saveDone := make(chan error, 1)
	go func() {
		saveDone <- saveStore.SaveConfig(config.Config{DefaultBotName: "bot-after-lock"})
	}()

	select {
	case saveErr := <-saveDone:
		require.FailNow(t, "SaveConfig should wait for advisory lock", "got early result: %v", saveErr)
	case <-time.After(150 * time.Millisecond):
	}

	err = lockTx.Commit()
	require.NoError(t, err)

	select {
	case saveErr := <-saveDone:
		require.NoError(t, saveErr)
	case <-time.After(2 * time.Second):
		t.Fatal("SaveConfig did not complete after advisory lock was released")
	}

	cfg, err := baseStore.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "bot-after-lock", cfg.DefaultBotName)
}
