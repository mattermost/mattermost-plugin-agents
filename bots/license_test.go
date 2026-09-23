// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func validBot(id, name, serviceID string) llm.BotConfig {
	return llm.BotConfig{
		ID:          id,
		Name:        name,
		DisplayName: name,
		ServiceID:   serviceID,
	}
}

func validOpenAIService(id string) llm.ServiceConfig {
	return llm.ServiceConfig{
		ID:     id,
		Type:   llm.ServiceTypeOpenAI,
		APIKey: "k",
	}
}

func newLicensedMMBots(t *testing.T, level enterprise.Level, cfg *mockConfig, store AgentStore) (*MMBots, *plugintest.API) {
	t.Helper()
	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	enterprisetest.StubLicense(mockAPI, level)
	allowBotsLogging(mockAPI)
	mmBots := New(mockAPI, client, enterprise.NewLicenseChecker(client), cfg, store, newPassthroughAccessChecker(), &http.Client{}, nil)
	return mmBots, mockAPI
}

func TestSnapshotBotsAppliesAgentCap(t *testing.T) {
	cfg := &mockConfig{
		bots: []llm.BotConfig{
			validBot("file-1", "filebot1", "svc1"),
			validBot("file-2", "filebot2", "svc1"),
		},
		services: []llm.ServiceConfig{validOpenAIService("svc1"), validOpenAIService("svc2")},
	}
	store := &stubAgentStore{
		agents: []llm.BotConfig{
			{ID: "db-1", Name: "dbagent1", DisplayName: "DB 1", ServiceID: "svc1", CreateAt: 20},
			{ID: "db-2", Name: "dbagent2", DisplayName: "DB 2", ServiceID: "svc1", CreateAt: 10},
		},
	}

	tests := []struct {
		level        enterprise.Level
		wantNames    []string
		wantActiveDB []string
	}{
		{enterprise.LevelUnlicensed, []string{"filebot1"}, nil},
		{enterprise.LevelProfessional, []string{"filebot1", "filebot2", "dbagent2"}, []string{"dbagent2"}},
		{enterprise.LevelEnterprise, []string{"filebot1", "filebot2", "dbagent2", "dbagent1"}, []string{"dbagent2", "dbagent1"}},
		{enterprise.LevelEnterpriseAdvanced, []string{"filebot1", "filebot2", "dbagent2", "dbagent1"}, []string{"dbagent2", "dbagent1"}},
	}

	for _, tc := range tests {
		t.Run(tc.level.String(), func(t *testing.T) {
			mmBots, _ := newLicensedMMBots(t, tc.level, cfg, store)
			bots, activeDB, _, err := mmBots.snapshotBotsAndServices()
			require.NoError(t, err)

			got := make([]string, len(bots))
			for i, b := range bots {
				got[i] = b.Name
			}
			assert.Equal(t, tc.wantNames, got)

			for _, name := range tc.wantActiveDB {
				assert.Contains(t, activeDB, name)
			}
			assert.Len(t, activeDB, len(tc.wantActiveDB))
			assert.Equal(t, cfg.bots[1].ID, cfg.GetBots()[1].ID, "config-owned slice must not be mutated")
		})
	}

	t.Run("nil license checker fails closed", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		client := pluginapi.NewClient(mockAPI, nil)
		allowBotsLogging(mockAPI)
		mmBots := New(mockAPI, client, nil, cfg, store, newPassthroughAccessChecker(), &http.Client{}, nil)
		bots, activeDB, _, err := mmBots.snapshotBotsAndServices()
		require.NoError(t, err)
		require.Len(t, bots, 1)
		assert.Equal(t, "filebot1", bots[0].Name)
		assert.Empty(t, activeDB)
	})
}

func TestSnapshotBotsSkipsInactiveServicesBelowEnterprise(t *testing.T) {
	cfg := &mockConfig{
		bots: []llm.BotConfig{
			validBot("file-1", "filebot1", "svc2"),
			validBot("file-2", "filebot2", "svc1"),
		},
		services: []llm.ServiceConfig{validOpenAIService("svc1"), validOpenAIService("svc2")},
	}

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			mmBots, _ := newLicensedMMBots(t, level, cfg, nil)
			bots, _, _, err := mmBots.snapshotBotsAndServices()
			require.NoError(t, err)
			if level >= enterprise.LevelEnterprise {
				require.Len(t, bots, 2)
				return
			}
			require.Len(t, bots, 1)
			assert.Equal(t, "filebot2", bots[0].Name)
		})
	}
}

func TestResolveServiceCfgsOmitsFallbacksBelowEnterpriseAdvanced(t *testing.T) {
	cfg := &mockConfig{
		bots: []llm.BotConfig{validBot("b1", "bot1", "svc1")},
		services: []llm.ServiceConfig{
			{
				ID: "svc1", Type: llm.ServiceTypeOpenAI, APIKey: "k",
				FallbackServiceID: "svc2",
			},
			validOpenAIService("svc2"),
		},
	}

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			mmBots, _ := newLicensedMMBots(t, level, cfg, nil)
			_, _, services, err := mmBots.snapshotBotsAndServices()
			require.NoError(t, err)
			require.Contains(t, services, "svc1")
			if level >= enterprise.LevelEnterpriseAdvanced {
				assert.Contains(t, services, "svc2")
			} else {
				assert.NotContains(t, services, "svc2")
			}
		})
	}
}

func TestEnsureBotsDeactivatesCapInactiveDBAgents(t *testing.T) {
	cfg := &mockConfig{
		bots:     []llm.BotConfig{validBot("file-1", "filebot1", "svc1")},
		services: []llm.ServiceConfig{validOpenAIService("svc1")},
	}
	store := &stubAgentStore{
		agents: []llm.BotConfig{
			{ID: "db-1", Name: "dbagent1", DisplayName: "DB 1", ServiceID: "svc1"},
		},
	}

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	enterprisetest.StubLicense(mockAPI, enterprise.LevelUnlicensed)
	allowBotsLogging(mockAPI)
	mockAPI.On("GetBots", mock.AnythingOfType("*model.BotGetOptions")).Return([]*model.Bot{
		{UserId: "mm-file", Username: "filebot1"},
		{UserId: "mm-db", Username: "dbagent1"},
	}, nil).Maybe()
	mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(func(bot *model.Bot) *model.Bot { return bot }, nil).Maybe()
	mockAPI.On("GetUser", mock.AnythingOfType("string")).Return(&model.User{LastPictureUpdate: 1}, nil).Maybe()
	mockAPI.On("PatchBot", mock.AnythingOfType("string"), mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()
	mockAPI.On("UpdateBotActive", "mm-file", true).Return(&model.Bot{}, nil).Once()
	mockAPI.On("UpdateBotActive", "mm-db", false).Return(&model.Bot{}, nil).Once()
	mockAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
	mockAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

	mmBots := New(mockAPI, client, enterprise.NewLicenseChecker(client), cfg, store, newPassthroughAccessChecker(), &http.Client{}, nil)
	require.NoError(t, mmBots.EnsureBots())
	mockAPI.AssertCalled(t, "UpdateBotActive", "mm-db", false)
}

func TestReconcileTokenUsageSinksHonorsLicense(t *testing.T) {
	cfg := &mockConfig{bots: nil, services: nil}
	cfg.enableTokenLogging = true

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			mmBots, _ := newLicensedMMBots(t, level, cfg, nil)
			mmBots.reconcileTokenUsageSinks()
			enabled := mmBots.tokenUsageSinks.LoggingEnabled()
			if level >= enterprise.LevelProfessional {
				assert.True(t, enabled)
			} else {
				assert.False(t, enabled)
			}
		})
	}
}
