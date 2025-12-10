// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dev_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/config/dev"
	sharedconfig "github.com/mattermost/mattermost-plugin-ai/config/shared"
	"github.com/mattermost/mattermost-plugin-ai/conversations"
	"github.com/mattermost/mattermost-plugin-ai/datasources"
	"github.com/mattermost/mattermost-plugin-ai/enterprise"
	"github.com/mattermost/mattermost-plugin-ai/evals"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/llmcontext"
	"github.com/mattermost/mattermost-plugin-ai/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-ai/mmtools"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost-plugin-ai/roles"
	devrole "github.com/mattermost/mattermost-plugin-ai/roles/dev"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// SetupMockClientWithLogging sets up a mock client with enhanced logging capabilities
func SetupMockClientWithLogging(t *testing.T, threadData *evals.ThreadExport, logger *roles.TestLogger) *mocks.MockClient {
	mmClient := mocks.NewMockClient(t)

	// Setup mock expectations
	mmClient.On("GetPostThread", threadData.LatestPost().Id).Return(threadData.PostList, nil).Maybe()
	mmClient.On("GetChannel", threadData.Channel.Id).Return(threadData.Channel, nil).Maybe()
	mmClient.On("GetBundlePath").Return("", nil).Maybe()

	// Setup PluginHTTP mock for GitHub API calls
	mmClient.On("PluginHTTP", mock.Anything).Return(&http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"id":123,"number":456,"title":"Test Issue","body":"Test issue body","state":"open","html_url":"https://github.com/test/repo/issues/456"}`)),
	}).Maybe()

	// Forward LogDebug calls to TestLogger
	logBridge := roles.CreateMockLoggerBridge(logger)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Return().Run(func(args mock.Arguments) {
		msg := args.Get(0).(string)
		var keyValuePairs []interface{}

		if len(args) > 1 {
			if fields, ok := args.Get(1).([]interface{}); ok {
				keyValuePairs = fields
			}
		}

		logBridge(msg, keyValuePairs...)
	}).Maybe()

	mmClient.On("LogWarn", mock.Anything, mock.Anything).Return().Run(func(args mock.Arguments) {
		msg := args.Get(0).(string)
		var keyValuePairs []interface{}
		if len(args) > 1 {
			if fields, ok := args.Get(1).([]interface{}); ok {
				keyValuePairs = fields
			}
		}
		logger.Warn(msg, keyValuePairs...)
	}).Maybe()

	mmClient.On("LogError", mock.Anything, mock.Anything).Return().Run(func(args mock.Arguments) {
		msg := args.Get(0).(string)
		var keyValuePairs []interface{}
		if len(args) > 1 {
			if fields, ok := args.Get(1).([]interface{}); ok {
				keyValuePairs = fields
			}
		}
		logger.Error(msg, keyValuePairs...)
	}).Maybe()

	mmClient.On("GetPluginStatus", mock.Anything).Return(&model.PluginStatus{PluginId: "test", State: model.PluginStateRunning}, nil).Maybe()

	for _, user := range threadData.Users {
		mmClient.On("GetUser", user.Id).Return(user, nil).Maybe()
	}
	for _, fileInfo := range threadData.FileInfos {
		mmClient.On("GetFileInfo", fileInfo.Id).Return(fileInfo, nil).Maybe()
	}
	for id, file := range threadData.Files {
		mmClient.On("GetFile", id).Return(io.NopCloser(bytes.NewReader(file)), nil).Maybe()
	}

	return mmClient
}

// CreateDevBotConfig creates standard DevBot configuration with external docs
func CreateDevBotConfig(logger *roles.TestLogger) *config.Config {
	commonDataSourcesConfig := datasources.CreateEnabledConfig()
	commonDataSourcesConfig.FallbackDirectory = "../../assets/fallback-data"

	// Create shared config with data sources
	sharedCfg := sharedconfig.Config{
		DataSources: commonDataSourcesConfig,
	}
	sharedConfigJSON, _ := json.Marshal(sharedCfg)

	// Create Dev config
	devConfigData := dev.Config{
		Enabled: true,
		MockMode: &dev.MockModeConfig{
			Enabled:   false,
			Directory: "",
		},
	}
	devRoleConfigJSON, _ := json.Marshal(devConfigData)

	testConfig := &config.Config{
		RoleConfigs: map[string]json.RawMessage{
			"dev":    devRoleConfigJSON,
			"shared": sharedConfigJSON,
		},
	}

	// Log enabled sources for debugging
	enabledSources := len(commonDataSourcesConfig.Sources)
	for _, src := range commonDataSourcesConfig.Sources {
		logger.Debug("Enabled external docs source", "source", src.Name, "protocol", src.Protocol)
	}

	logger.Debug("Dev role config initialized", "enabled", true)
	logger.Debug("External docs config initialized", "sources_count", enabledSources)

	return testConfig
}

// SetupDevBotServices creates all the services needed for DevBot testing
func SetupDevBotServices(t *testing.T, threadData *evals.ThreadExport, mmClient *mocks.MockClient) (*pluginapi.Client, *enterprise.LicenseChecker, interface{}, *llm.Prompts, *llmcontext.Builder, *conversations.Conversations, *mmtools.MMToolProvider) {
	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(client)
	botService := bots.New(mockAPI, client, licenseChecker, nil, &http.Client{}, nil, nil)

	prompts, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err, "Failed to load prompts")

	// Setup mock API expectations
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: "professional"}).Maybe()
	mockAPI.On("GetTeam", threadData.Team.Id).Return(threadData.Team, nil)
	mockAPI.On("GetChannel", threadData.Channel.Id).Return(threadData.Channel, nil)
	mockAPI.On("LogDebug", mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Create config and tool provider
	silentLogger := roles.NewTestLogger(t, false, false, "")
	testConfig := CreateDevBotConfig(silentLogger)
	configContainer := &config.Container{}
	configContainer.Update(testConfig)

	toolProvider := mmtools.NewMMToolProvider(
		mmClient,
		nil,
		&http.Client{},
		configContainer,
		nil,
	)

	// Register Dev provider with tools using the test helper
	// This initializes the datasources client so Dev tools get real API data (not mocks)
	mmtools.RegisterRoleProviderForTest(toolProvider, configContainer, "dev")

	mcpClientManager := &roles.MockMCPClientManager{}
	configProvider := &roles.MockConfigProvider{}

	contextBuilder := llmcontext.NewLLMContextBuilder(
		client,
		toolProvider,
		mcpClientManager,
		configProvider,
	)

	conv := conversations.New(
		prompts,
		mmClient,
		nil,
		contextBuilder,
		botService,
		nil,
		licenseChecker,
		i18n.Init(),
		nil,
	)

	// Register DevBot service for Dev bot intent detection and template selection
	devService := devrole.NewService(mmClient, prompts)
	devService.RegisterWithConversations(conv)

	return client, licenseChecker, botService, prompts, contextBuilder, conv, toolProvider
}

// CreateStandardDevBotConfig creates a standardized DevBot configuration
func CreateStandardDevBotConfig(modelName, botID string) llm.BotConfig {
	return llm.BotConfig{
		ID:           botID,
		Name:         "devbot",
		DisplayName:  "Dev Assistant",
		EnableVision: false,
		DisableTools: false,
		Service: &llm.ServiceConfig{
			DefaultModel: modelName,
		},
	}
}

// CreateDevBot creates a DevBot with the specified configuration
func CreateDevBot(modelName, botID string) *bots.Bot {
	return bots.NewBot(
		CreateStandardDevBotConfig(modelName, botID),
		llm.ServiceConfig{},
		&model.Bot{
			UserId: botID,
		},
		nil,
	)
}
