// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockConfig struct {
	bots     []llm.BotConfig
	services []llm.ServiceConfig
}

func (m *mockConfig) GetBots() []llm.BotConfig {
	return m.bots
}

func (m *mockConfig) GetServiceByID(id string) (llm.ServiceConfig, bool) {
	for _, service := range m.services {
		if service.ID == id {
			return service, true
		}
	}
	return llm.ServiceConfig{}, false
}

func (m *mockConfig) GetDefaultBotName() string {
	return "testbot"
}

func (m *mockConfig) EnableLLMLogging() bool {
	return false
}

func (m *mockConfig) EnableTokenUsageLogging() bool {
	return false
}

func (m *mockConfig) GetTranscriptGenerator() string {
	return "testbot"
}

func TestEnsureBots(t *testing.T) {
	testCases := []struct {
		name               string
		cfgBots            []llm.BotConfig
		cfgServices        []llm.ServiceConfig
		isMultiLLMLicensed bool
		numCreatedBots     int
		expectError        bool
	}{
		{
			name:               "empty bots config with unlicensed server should not crash",
			cfgBots:            []llm.BotConfig{},
			cfgServices:        []llm.ServiceConfig{},
			isMultiLLMLicensed: false,
			expectError:        false,
			numCreatedBots:     0,
		},
		{
			name:               "empty bots config with licensed server should not crash",
			cfgBots:            []llm.BotConfig{},
			cfgServices:        []llm.ServiceConfig{},
			isMultiLLMLicensed: true,
			expectError:        false,
			numCreatedBots:     0,
		},
		{
			name: "single bot config with unlicensed server should work",
			cfgBots: []llm.BotConfig{
				{
					ID:          "test1",
					Name:        "testbot1",
					DisplayName: "Test Bot 1",
					ServiceID:   "service1",
				},
			},
			cfgServices: []llm.ServiceConfig{
				{
					ID:     "service1",
					Type:   llm.ServiceTypeOpenAI,
					APIKey: "test-api-key",
				},
			},
			isMultiLLMLicensed: false,
			expectError:        false,
			numCreatedBots:     1,
		},
		{
			name: "multiple bots config with unlicensed server should limit to one",
			cfgBots: []llm.BotConfig{
				{
					ID:          "test1",
					Name:        "testbot1",
					DisplayName: "Test Bot 1",
					ServiceID:   "service1",
				},
				{
					ID:          "test2",
					Name:        "testbot2",
					DisplayName: "Test Bot 2",
					ServiceID:   "service2",
				},
			},
			cfgServices: []llm.ServiceConfig{
				{
					ID:     "service1",
					Type:   llm.ServiceTypeOpenAI,
					APIKey: "test-api-key",
				},
				{
					ID:     "service2",
					Type:   llm.ServiceTypeOpenAI,
					APIKey: "test-api-key-2",
				},
			},
			isMultiLLMLicensed: false,
			expectError:        false,
			numCreatedBots:     1,
		},
		{
			name: "multiple bots config with licensed server should not limit",
			cfgBots: []llm.BotConfig{
				{
					ID:          "test1",
					Name:        "testbot1",
					DisplayName: "Test Bot 1",
					ServiceID:   "service1",
				},
				{
					ID:          "test2",
					Name:        "testbot2",
					DisplayName: "Test Bot 2",
					ServiceID:   "service2",
				},
			},
			cfgServices: []llm.ServiceConfig{
				{
					ID:     "service1",
					Type:   llm.ServiceTypeOpenAI,
					APIKey: "test-api-key",
				},
				{
					ID:     "service2",
					Type:   llm.ServiceTypeOpenAI,
					APIKey: "test-api-key-2",
				},
			},
			isMultiLLMLicensed: true,
			expectError:        false,
			numCreatedBots:     2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			client := pluginapi.NewClient(mockAPI, nil)

			// Mock the license check
			if tc.isMultiLLMLicensed {
				config := &model.Config{}
				license := &model.License{}
				license.Features = &model.Features{}
				license.Features.SetDefaults()
				license.SkuShortName = model.LicenseShortSkuEnterprise
				mockAPI.On("GetConfig").Return(config).Maybe()
				mockAPI.On("GetLicense").Return(license).Maybe()
			} else {
				config := &model.Config{}
				mockAPI.On("GetConfig").Return(config).Maybe()
				mockAPI.On("GetLicense").Return((*model.License)(nil)).Maybe()
			}

			// Mock bot operations
			mockAPI.On("GetBots", mock.AnythingOfType("*model.BotGetOptions")).Return([]*model.Bot{}, nil).Maybe()
			if tc.numCreatedBots > 0 {
				mockAPI.On("CreateBot", mock.AnythingOfType("*model.Bot")).Return(func(bot *model.Bot) *model.Bot {
					return bot
				}, nil).Times(tc.numCreatedBots)
			}
			mockAPI.On("UpdateBotActive", mock.AnythingOfType("string"), mock.AnythingOfType("bool")).Return(&model.Bot{}, nil).Maybe()
			mockAPI.On("PatchBot", mock.AnythingOfType("string"), mock.AnythingOfType("*model.BotPatch")).Return(&model.Bot{}, nil).Maybe()

			// Mock mutex operations
			mockAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
			mockAPI.On("KVDelete", mock.AnythingOfType("string")).Return(nil).Maybe()

			// Mock logging
			mockAPI.On("LogError", mock.Anything).Return(nil).Maybe()

			licenseChecker := enterprise.NewLicenseChecker(client)
			cfg := &mockConfig{
				bots:     tc.cfgBots,
				services: tc.cfgServices,
			}
			mmBots := New(mockAPI, client, licenseChecker, cfg, &http.Client{}, nil, nil)

			defer mockAPI.AssertExpectations(t)

			err := mmBots.EnsureBots()
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
