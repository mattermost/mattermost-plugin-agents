// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/config"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	legacyMigrationTestConnStr string
	legacyMigrationTestBaseDir string
	legacyMigrationTestDB      *embeddedpostgres.EmbeddedPostgres
	legacyMigrationSetupOnce   sync.Once
	legacyMigrationSetupErr    error
)

func TestMain(m *testing.M) {
	os.Exit(runLegacyBotMigrationTestMain(m))
}

func runLegacyBotMigrationTestMain(m *testing.M) int {
	code := m.Run()

	if legacyMigrationTestDB != nil {
		if err := legacyMigrationTestDB.Stop(); err != nil {
			fmt.Printf("Failed to stop embedded postgres: %v\n", err)
			return 1
		}
	}

	if legacyMigrationTestBaseDir != "" {
		if err := os.RemoveAll(legacyMigrationTestBaseDir); err != nil {
			fmt.Printf("Failed to remove temp dir: %v\n", err)
			return 1
		}
	}

	return code
}

func ensureLegacyMigrationPostgres(t *testing.T) {
	t.Helper()

	legacyMigrationSetupOnce.Do(func() {
		legacyMigrationSetupErr = startLegacyMigrationPostgres()
	})

	require.NoError(t, legacyMigrationSetupErr)
	require.NotEmpty(t, legacyMigrationTestConnStr)
}

func startLegacyMigrationPostgres() error {
	baseDir, err := os.MkdirTemp("", "legacy-bot-migration-testdb-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	legacyMigrationTestBaseDir = baseDir

	for attempt := 0; attempt < 10; attempt++ {
		port := legacyMigrationPortForAttempt(attempt)

		dbConfig := embeddedpostgres.DefaultConfig().
			Port(port).
			Database("testdb").
			Username("testuser").
			Password("testpass").
			StartTimeout(2 * time.Minute).
			RuntimePath(filepath.Join(baseDir, "runtime")).
			DataPath(filepath.Join(baseDir, "data")).
			BinariesPath(filepath.Join(baseDir, "binaries")).
			CachePath(filepath.Join(baseDir, "cache"))

		postgres := embeddedpostgres.NewDatabase(dbConfig)
		if err := postgres.Start(); err != nil {
			_ = postgres.Stop()
			if isPortInUseError(err) {
				continue
			}
			return fmt.Errorf("failed to start embedded postgres: %w", err)
		}

		legacyMigrationTestDB = postgres
		legacyMigrationTestConnStr = dbConfig.GetConnectionURL()
		return nil
	}

	return fmt.Errorf("failed to start embedded postgres after multiple port attempts")
}

func legacyMigrationPortForAttempt(attempt int) uint32 {
	offset := (time.Now().UnixNano() + int64(attempt*7919)) % 30000
	if offset < 0 {
		offset = -offset
	}

	return uint32(20000 + offset)
}

func isPortInUseError(err error) bool {
	return strings.Contains(err.Error(), "already listening on port") ||
		strings.Contains(err.Error(), "address already in use")
}

func setupLegacyMigrationStore(t *testing.T) *store.Store {
	t.Helper()
	ensureLegacyMigrationPostgres(t)

	setupDB, err := sqlx.Connect("postgres", legacyMigrationTestConnStr)
	require.NoError(t, err)

	schemaName := fmt.Sprintf("test_%d", time.Now().UnixNano())
	_, err = setupDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schemaName))
	require.NoError(t, err)
	setupDB.Close()

	connStr := withSearchPath(t, legacyMigrationTestConnStr, schemaName)
	db, err := sqlx.Connect("postgres", connStr)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schemaName))
		db.Close()
	})

	st := store.New(db)
	require.NoError(t, st.RunMigrations())

	return st
}

func withSearchPath(t *testing.T, connStr, schemaName string) string {
	t.Helper()

	parsed, err := url.Parse(connStr)
	require.NoError(t, err)

	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()

	return parsed.String()
}

func newLegacyBotConfig(username string) llm.BotConfig {
	return llm.BotConfig{
		ID:                      "legacy-" + username,
		Name:                    username,
		DisplayName:             "Legacy " + username,
		CustomInstructions:      "Help as " + username,
		ServiceID:               "svc-1",
		Model:                   "gpt-4o",
		EnableVision:            true,
		DisableTools:            false,
		ChannelAccessLevel:      llm.ChannelAccessLevelAllow,
		ChannelIDs:              []string{"channel-1"},
		UserAccessLevel:         llm.UserAccessLevelAll,
		TeamIDs:                 []string{"team-1"},
		EnabledNativeTools:      []string{"web_search"},
		EnabledMCPTools:         []llm.EnabledMCPTool{{ServerOrigin: "https://mcp.example.com", ToolName: "search_posts"}},
		AutoEnableNewMCPTools:   false,
		ReasoningEnabled:        true,
		ReasoningEffort:         "medium",
		ThinkingBudget:          2048,
		StructuredOutputEnabled: true,
		BotUserID:               "config-user-" + username,
		CreatorID:               "legacy-creator",
		AdminUserIDs:            []string{"legacy-admin"},
		CreateAt:                111,
		UpdateAt:                222,
		DeleteAt:                333,
	}
}

func newLegacyConfig(bots ...llm.BotConfig) config.Config {
	return config.Config{
		Bots:           bots,
		DefaultBotName: "legacy-default",
	}
}

func newPluginAPIForMigration(t *testing.T, mmBots []*model.Bot) (*plugintest.API, *pluginapi.Client) {
	t.Helper()

	api := &plugintest.API{}
	api.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	api.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()
	api.On("GetBots", mock.AnythingOfType("*model.BotGetOptions")).Return(mmBots, nil).Maybe()
	api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	api.On("LogInfo", mock.Anything).Return().Maybe()

	return api, pluginapi.NewClient(api, nil)
}

func saveConfigAndLoadContainer(t *testing.T, st *store.Store, cfgValue config.Config) *config.Container {
	t.Helper()

	require.NoError(t, st.SaveConfig(cfgValue))

	cfg := &config.Container{}
	cfg.Update(&cfgValue)

	return cfg
}

func agentByName(t *testing.T, st *store.Store) map[string]*llm.BotConfig {
	t.Helper()

	agents, err := st.ListAgents()
	require.NoError(t, err)

	byName := make(map[string]*llm.BotConfig, len(agents))
	for _, agent := range agents {
		byName[agent.Name] = agent
	}

	return byName
}

func TestMigrateLegacyConfigBotsToUserAgentsAlreadyMigrated(t *testing.T) {
	st := setupLegacyMigrationStore(t)
	cfgValue := newLegacyConfig(newLegacyBotConfig("alpha"))
	cfg := saveConfigAndLoadContainer(t, st, cfgValue)
	require.NoError(t, st.SetSystemValue(legacyConfigBotsMigratedKey, "true"))

	mockAPI, pluginClient := newPluginAPIForMigration(t, nil)

	migrated, err := migrateLegacyConfigBotsToUserAgents(mockAPI, pluginClient, st, cfg)
	require.NoError(t, err)
	assert.False(t, migrated)

	agents, err := st.ListAgents()
	require.NoError(t, err)
	assert.Empty(t, agents)

	reloaded, err := st.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	require.Len(t, reloaded.Bots, 1)
	assert.Equal(t, "alpha", reloaded.Bots[0].Name)

	flag, err := st.GetSystemValue(legacyConfigBotsMigratedKey)
	require.NoError(t, err)
	assert.Equal(t, "true", flag)
}

func TestMigrateLegacyConfigBotsToUserAgentsNoConfigBots(t *testing.T) {
	st := setupLegacyMigrationStore(t)
	cfgValue := newLegacyConfig()
	cfg := saveConfigAndLoadContainer(t, st, cfgValue)

	mockAPI, pluginClient := newPluginAPIForMigration(t, nil)

	migrated, err := migrateLegacyConfigBotsToUserAgents(mockAPI, pluginClient, st, cfg)
	require.NoError(t, err)
	assert.False(t, migrated)

	agents, err := st.ListAgents()
	require.NoError(t, err)
	assert.Empty(t, agents)

	reloaded, err := st.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Empty(t, reloaded.Bots)

	flag, err := st.GetSystemValue(legacyConfigBotsMigratedKey)
	require.NoError(t, err)
	assert.Empty(t, flag)
}

func TestMigrateLegacyConfigBotsToUserAgentsDefersWhenMMBotMissing(t *testing.T) {
	st := setupLegacyMigrationStore(t)
	cfgValue := newLegacyConfig(newLegacyBotConfig("alpha"))
	cfg := saveConfigAndLoadContainer(t, st, cfgValue)

	mockAPI, pluginClient := newPluginAPIForMigration(t, []*model.Bot{})

	migrated, err := migrateLegacyConfigBotsToUserAgents(mockAPI, pluginClient, st, cfg)
	require.NoError(t, err)
	assert.False(t, migrated)

	agents, err := st.ListAgents()
	require.NoError(t, err)
	assert.Empty(t, agents)

	reloaded, err := st.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	require.Len(t, reloaded.Bots, 1)
	assert.Equal(t, "alpha", reloaded.Bots[0].Name)
	require.NotNil(t, cfg.Config())
	require.Len(t, cfg.Config().Bots, 1)

	flag, err := st.GetSystemValue(legacyConfigBotsMigratedKey)
	require.NoError(t, err)
	assert.Empty(t, flag)
}

func TestMigrateLegacyConfigBotsToUserAgentsHappyPath(t *testing.T) {
	st := setupLegacyMigrationStore(t)
	cfgValue := newLegacyConfig(
		newLegacyBotConfig("alpha"),
		newLegacyBotConfig("beta"),
	)
	cfg := saveConfigAndLoadContainer(t, st, cfgValue)

	mockAPI, pluginClient := newPluginAPIForMigration(t, []*model.Bot{
		{Username: "alpha", UserId: "mm-user-alpha"},
		{Username: "beta", UserId: "mm-user-beta"},
	})

	migrated, err := migrateLegacyConfigBotsToUserAgents(mockAPI, pluginClient, st, cfg)
	require.NoError(t, err)
	assert.True(t, migrated)

	agents := agentByName(t, st)
	require.Len(t, agents, 2)

	alpha := agents["alpha"]
	require.NotNil(t, alpha)
	assert.Equal(t, "Legacy alpha", alpha.DisplayName)
	assert.Equal(t, "Help as alpha", alpha.CustomInstructions)
	assert.Equal(t, "svc-1", alpha.ServiceID)
	assert.Equal(t, "mm-user-alpha", alpha.BotUserID)
	assert.Empty(t, alpha.CreatorID)
	assert.Nil(t, alpha.AdminUserIDs)
	assert.True(t, alpha.AutoEnableNewMCPTools)
	assert.Nil(t, alpha.EnabledMCPTools)
	assert.NotEmpty(t, alpha.ID)
	assert.NotZero(t, alpha.CreateAt)
	assert.NotZero(t, alpha.UpdateAt)
	assert.Zero(t, alpha.DeleteAt)

	beta := agents["beta"]
	require.NotNil(t, beta)
	assert.Equal(t, "mm-user-beta", beta.BotUserID)
	assert.True(t, beta.AutoEnableNewMCPTools)
	assert.Nil(t, beta.EnabledMCPTools)

	reloaded, err := st.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Empty(t, reloaded.Bots)
	require.NotNil(t, cfg.Config())
	assert.Empty(t, cfg.Config().Bots)

	flag, err := st.GetSystemValue(legacyConfigBotsMigratedKey)
	require.NoError(t, err)
	assert.Equal(t, "true", flag)
}

func TestMigrateLegacyConfigBotsToUserAgentsSkipsExistingUsername(t *testing.T) {
	st := setupLegacyMigrationStore(t)
	cfgValue := newLegacyConfig(
		newLegacyBotConfig("alpha"),
		newLegacyBotConfig("beta"),
	)
	cfg := saveConfigAndLoadContainer(t, st, cfgValue)

	existing := newLegacyBotConfig("alpha")
	existing.BotUserID = "existing-mm-user-alpha"
	existing.CreatorID = "existing-creator"
	existing.AdminUserIDs = []string{"existing-admin"}
	existing.AutoEnableNewMCPTools = false
	existing.EnabledMCPTools = []llm.EnabledMCPTool{{ServerOrigin: "https://mcp.example.com", ToolName: "existing_tool"}}
	require.NoError(t, st.CreateAgent(&existing))
	existingID := existing.ID
	existingBotUserID := existing.BotUserID

	mockAPI, pluginClient := newPluginAPIForMigration(t, []*model.Bot{
		{Username: "alpha", UserId: "mm-user-alpha"},
		{Username: "beta", UserId: "mm-user-beta"},
	})

	migrated, err := migrateLegacyConfigBotsToUserAgents(mockAPI, pluginClient, st, cfg)
	require.NoError(t, err)
	assert.True(t, migrated)

	agents := agentByName(t, st)
	require.Len(t, agents, 2)

	alpha := agents["alpha"]
	require.NotNil(t, alpha)
	assert.Equal(t, existingID, alpha.ID)
	assert.Equal(t, existingBotUserID, alpha.BotUserID)
	assert.Equal(t, "existing-creator", alpha.CreatorID)
	assert.False(t, alpha.AutoEnableNewMCPTools)
	require.Len(t, alpha.EnabledMCPTools, 1)
	assert.Equal(t, "existing_tool", alpha.EnabledMCPTools[0].ToolName)

	beta := agents["beta"]
	require.NotNil(t, beta)
	assert.Equal(t, "mm-user-beta", beta.BotUserID)
	assert.Empty(t, beta.CreatorID)
	assert.True(t, beta.AutoEnableNewMCPTools)
	assert.Nil(t, beta.EnabledMCPTools)

	reloaded, err := st.GetConfig()
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Empty(t, reloaded.Bots)

	flag, err := st.GetSystemValue(legacyConfigBotsMigratedKey)
	require.NoError(t, err)
	assert.Equal(t, "true", flag)
}
