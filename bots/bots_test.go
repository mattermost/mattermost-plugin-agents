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

func (m *mockConfig) EnableTokenUsageLogToPlugin() bool {
	return true
}

func (m *mockConfig) EnableTokenUsageLogToFile() bool {
	return false
}

func (m *mockConfig) GetTranscriptGenerator() string {
	return "testbot"
}

func TestGetAllBotUserIDs(t *testing.T) {
	tests := []struct {
		name     string
		bots     []*Bot
		expected []string
	}{
		{
			name:     "returns empty slice when no bots configured",
			bots:     nil,
			expected: []string{},
		},
		{
			name:     "returns empty slice when bots slice is empty",
			bots:     []*Bot{},
			expected: []string{},
		},
		{
			name: "returns all bot user IDs when bots exist",
			bots: []*Bot{
				{mmBot: &model.Bot{UserId: "bot1-user-id"}},
				{mmBot: &model.Bot{UserId: "bot2-user-id"}},
			},
			expected: []string{"bot1-user-id", "bot2-user-id"},
		},
		{
			name: "skips bots with nil mmBot",
			bots: []*Bot{
				{mmBot: &model.Bot{UserId: "bot1-user-id"}},
				{mmBot: nil},
				{mmBot: &model.Bot{UserId: "bot3-user-id"}},
			},
			expected: []string{"bot1-user-id", "bot3-user-id"},
		},
		{
			name: "returns correct count with single bot",
			bots: []*Bot{
				{mmBot: &model.Bot{UserId: "single-bot-id"}},
			},
			expected: []string{"single-bot-id"},
		},
		{
			name: "returns empty slice when all bots have nil mmBot",
			bots: []*Bot{
				{mmBot: nil},
				{mmBot: nil},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mmBots := &MMBots{}
			mmBots.SetBotsForTesting(tt.bots)

			result := mmBots.GetAllBotUserIDs()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestBotConfigsEqual(t *testing.T) {
	baseBotConfig := func() llm.BotConfig {
		return llm.BotConfig{
			ID:                 "bot1",
			Name:               "testbot",
			DisplayName:        "Test Bot",
			CustomInstructions: "Be helpful",
			ServiceID:          "svc1",
			Model:              "gpt-4o",
			EnableVision:       true,
			DisableTools:       false,
			ChannelAccessLevel: llm.ChannelAccessLevelAll,
			ChannelIDs:         []string{"ch1", "ch2"},
			UserAccessLevel:    llm.UserAccessLevelAll,
			UserIDs:            []string{"u1"},
			TeamIDs:            []string{"t1"},
			MaxFileSize:        1024,
			EnabledNativeTools: []string{"web_search"},
			ReasoningEnabled:   true,
			ReasoningEffort:    "medium",
			ThinkingBudget:     4096,
		}
	}

	testCases := []struct {
		name     string
		a        []llm.BotConfig
		b        []llm.BotConfig
		expected bool
	}{
		{
			name:     "both empty",
			a:        []llm.BotConfig{},
			b:        []llm.BotConfig{},
			expected: true,
		},
		{
			name:     "both nil",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name:     "identical configs",
			a:        []llm.BotConfig{baseBotConfig()},
			b:        []llm.BotConfig{baseBotConfig()},
			expected: true,
		},
		{
			name:     "different lengths",
			a:        []llm.BotConfig{baseBotConfig()},
			b:        []llm.BotConfig{},
			expected: false,
		},
		{
			name: "different Name",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.Name = "otherbot"
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different EnabledNativeTools",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.EnabledNativeTools = []string{}
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "EnabledNativeTools added",
			a: func() []llm.BotConfig {
				c := baseBotConfig()
				c.EnabledNativeTools = nil
				return []llm.BotConfig{c}
			}(),
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.EnabledNativeTools = []string{"web_search"}
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different CustomInstructions",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.CustomInstructions = "Be concise"
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different EnableVision",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.EnableVision = false
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different DisableTools",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.DisableTools = true
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different ChannelAccessLevel",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.ChannelAccessLevel = llm.ChannelAccessLevelBlock
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different ChannelIDs",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.ChannelIDs = []string{"ch3"}
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different UserIDs",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.UserIDs = []string{"u2", "u3"}
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different TeamIDs",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.TeamIDs = []string{"t2"}
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different MaxFileSize",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.MaxFileSize = 2048
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different ReasoningEnabled",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.ReasoningEnabled = false
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different ReasoningEffort",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.ReasoningEffort = "high"
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different ThinkingBudget",
			a:    []llm.BotConfig{baseBotConfig()},
			b: func() []llm.BotConfig {
				c := baseBotConfig()
				c.ThinkingBudget = 8192
				return []llm.BotConfig{c}
			}(),
			expected: false,
		},
		{
			name: "different order same configs",
			a: func() []llm.BotConfig {
				c1 := baseBotConfig()
				c2 := baseBotConfig()
				c2.ID = "bot2"
				c2.Name = "testbot2"
				return []llm.BotConfig{c1, c2}
			}(),
			b: func() []llm.BotConfig {
				c1 := baseBotConfig()
				c2 := baseBotConfig()
				c2.ID = "bot2"
				c2.Name = "testbot2"
				return []llm.BotConfig{c2, c1}
			}(),
			expected: true,
		},
		{
			name: "missing bot ID in second slice",
			a: func() []llm.BotConfig {
				c1 := baseBotConfig()
				c2 := baseBotConfig()
				c2.ID = "bot2"
				return []llm.BotConfig{c1, c2}
			}(),
			b: func() []llm.BotConfig {
				c1 := baseBotConfig()
				c3 := baseBotConfig()
				c3.ID = "bot3"
				return []llm.BotConfig{c1, c3}
			}(),
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := botConfigsEqual(tc.a, tc.b)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestServiceConfigsEqual(t *testing.T) {
	baseServiceConfig := func() llm.ServiceConfig {
		return llm.ServiceConfig{
			ID:                      "svc1",
			Name:                    "OpenAI Service",
			Type:                    llm.ServiceTypeOpenAI,
			APIKey:                  "sk-test",
			DefaultModel:            "gpt-4o",
			APIURL:                  "https://api.openai.com/v1",
			InputTokenLimit:         128000,
			OutputTokenLimit:        4096,
			StreamingTimeoutSeconds: 30,
			UseResponsesAPI:         true,
		}
	}

	testCases := []struct {
		name     string
		a        map[string]llm.ServiceConfig
		b        map[string]llm.ServiceConfig
		expected bool
	}{
		{
			name:     "both empty",
			a:        map[string]llm.ServiceConfig{},
			b:        map[string]llm.ServiceConfig{},
			expected: true,
		},
		{
			name:     "both nil",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name:     "identical configs",
			a:        map[string]llm.ServiceConfig{"svc1": baseServiceConfig()},
			b:        map[string]llm.ServiceConfig{"svc1": baseServiceConfig()},
			expected: true,
		},
		{
			name:     "different lengths",
			a:        map[string]llm.ServiceConfig{"svc1": baseServiceConfig()},
			b:        map[string]llm.ServiceConfig{},
			expected: false,
		},
		{
			name: "different APIURL",
			a:    map[string]llm.ServiceConfig{"svc1": baseServiceConfig()},
			b: func() map[string]llm.ServiceConfig {
				c := baseServiceConfig()
				c.APIURL = "https://custom.api.com/v1"
				return map[string]llm.ServiceConfig{"svc1": c}
			}(),
			expected: false,
		},
		{
			name: "different OutputTokenLimit",
			a:    map[string]llm.ServiceConfig{"svc1": baseServiceConfig()},
			b: func() map[string]llm.ServiceConfig {
				c := baseServiceConfig()
				c.OutputTokenLimit = 8192
				return map[string]llm.ServiceConfig{"svc1": c}
			}(),
			expected: false,
		},
		{
			name: "different UseResponsesAPI",
			a:    map[string]llm.ServiceConfig{"svc1": baseServiceConfig()},
			b: func() map[string]llm.ServiceConfig {
				c := baseServiceConfig()
				c.UseResponsesAPI = false
				return map[string]llm.ServiceConfig{"svc1": c}
			}(),
			expected: false,
		},
		{
			name: "different APIKey",
			a:    map[string]llm.ServiceConfig{"svc1": baseServiceConfig()},
			b: func() map[string]llm.ServiceConfig {
				c := baseServiceConfig()
				c.APIKey = "sk-new-key"
				return map[string]llm.ServiceConfig{"svc1": c}
			}(),
			expected: false,
		},
		{
			name: "missing service ID",
			a: map[string]llm.ServiceConfig{
				"svc1": baseServiceConfig(),
				"svc2": baseServiceConfig(),
			},
			b: map[string]llm.ServiceConfig{
				"svc1": baseServiceConfig(),
				"svc3": baseServiceConfig(),
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := serviceConfigsEqual(tc.a, tc.b)
			require.Equal(t, tc.expected, result)
		})
	}
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
				mockAPI.On("GetUser", mock.AnythingOfType("string")).Return(&model.User{LastPictureUpdate: 0}, nil).Times(tc.numCreatedBots)
				mockAPI.On("SetProfileImage", mock.AnythingOfType("string"), mock.AnythingOfType("[]uint8")).Return(nil).Times(tc.numCreatedBots)
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
			mmBots := New(mockAPI, client, licenseChecker, cfg, &http.Client{}, nil)

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
