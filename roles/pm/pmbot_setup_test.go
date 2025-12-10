// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package pm_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/config/pm"
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
	pmrole "github.com/mattermost/mattermost-plugin-ai/roles/pm"
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
	mmClient.On("GetBundlePath").Return("", nil).Maybe() // Return empty path for tests
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

// CreatePMBotConfig creates standard PM bot configuration with external docs
func CreatePMBotConfig(logger *roles.TestLogger) *config.Config {
	commonDataSourcesConfig := datasources.CreateEnabledConfig()
	commonDataSourcesConfig.FallbackDirectory = "../../assets/fallback-data"

	// Configure authentication for Mattermost data sources if tokens are available
	if communityToken := os.Getenv("MM_AI_COMMUNITY_FORUM_TOKEN"); communityToken != "" {
		for i := range commonDataSourcesConfig.Sources {
			if commonDataSourcesConfig.Sources[i].Name == datasources.SourceCommunityForum {
				commonDataSourcesConfig.Sources[i].Auth.Type = datasources.AuthTypeToken
				commonDataSourcesConfig.Sources[i].Auth.Key = communityToken
				logger.Debug("Configured auth for community_forum")
				break
			}
		}
	}

	if hubToken := os.Getenv("MM_AI_HUB_TOKEN"); hubToken != "" {
		for i := range commonDataSourcesConfig.Sources {
			if commonDataSourcesConfig.Sources[i].Name == datasources.SourceMattermostHub {
				commonDataSourcesConfig.Sources[i].Auth.Type = datasources.AuthTypeToken
				commonDataSourcesConfig.Sources[i].Auth.Key = hubToken
				logger.Debug("Configured auth for mattermost_hub")
				break
			}
		}
	}

	// Create shared config with data sources
	sharedCfg := sharedconfig.Config{
		DataSources: commonDataSourcesConfig,
	}
	sharedConfigJSON, _ := json.Marshal(sharedCfg)

	// Create PM config
	pmConfigData := pm.Config{
		Enabled: true, // Enable PM tools to access external docs
		MockMode: &pm.MockModeConfig{
			Enabled:   false, // Use REAL functionality to test PM bot
			Directory: "",
		},
	}

	// Marshal PM config to JSON
	pmConfigJSON, _ := json.Marshal(pmConfigData)

	testConfig := &config.Config{
		RoleConfigs: map[string]json.RawMessage{
			"pm":     pmConfigJSON,
			"shared": sharedConfigJSON,
		},
	}

	// Log enabled sources for debugging
	enabledSources := len(commonDataSourcesConfig.Sources)
	for _, src := range commonDataSourcesConfig.Sources {
		logger.Debug("Enabled external docs source", "source", src.Name, "protocol", src.Protocol)
	}

	logger.Debug("External docs config initialized", "sources_count", enabledSources)

	return testConfig
}

// SetupPMBotServices creates all the services needed for PM bot testing
func SetupPMBotServices(t *testing.T, threadData *evals.ThreadExport, mmClient *mocks.MockClient) (*pluginapi.Client, *enterprise.LicenseChecker, interface{}, *llm.Prompts, *llmcontext.Builder, *conversations.Conversations, *mmtools.MMToolProvider) {
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
	// Add LogDebug expectations to prevent mock failures - handle both 2 and 3 argument calls
	mockAPI.On("LogDebug", mock.Anything, mock.Anything).Return().Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	// Create config and tool provider
	silentLogger := roles.NewTestLogger(t, false, false, "")
	testConfig := CreatePMBotConfig(silentLogger)
	configContainer := &config.Container{}
	configContainer.Update(testConfig)

	toolProvider := mmtools.NewMMToolProvider(
		mmClient,
		nil, // search service - not needed for PM tools in this test
		&http.Client{},
		configContainer,
		nil, // database - not needed for this test
	)

	// Register PM provider with tools using the test helper
	mmtools.RegisterRoleProviderForTest(toolProvider, configContainer, "pm")

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

	// Register PM service for PM bot intent detection and template selection
	pmService := pmrole.NewService(mmClient, prompts)
	pmService.RegisterWithConversations(conv)

	return client, licenseChecker, botService, prompts, contextBuilder, conv, toolProvider
}

// CreateStandardPMBotConfig creates a standardized PM bot configuration
// Note: PM bots use template-based prompts from prompts/pm/*.tmpl, not CustomInstructions
func CreateStandardPMBotConfig(modelName, botID string) llm.BotConfig {
	return llm.BotConfig{
		ID:          botID,
		Name:        "pmbot",
		DisplayName: "PM Assistant",
		// CustomInstructions intentionally omitted - PM bots use template system via PM conversation handler
		EnableVision: false,
		DisableTools: false,
		Service: &llm.ServiceConfig{
			DefaultModel: modelName,
		},
	}
}

// CreatePMBot creates a PM bot with the specified configuration
func CreatePMBot(modelName, botID string) *bots.Bot {
	return bots.NewBot(
		CreateStandardPMBotConfig(modelName, botID),
		llm.ServiceConfig{},
		&model.Bot{
			UserId: botID,
		},
		nil,
	)
}
