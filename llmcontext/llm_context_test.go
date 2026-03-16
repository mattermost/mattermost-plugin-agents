// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

type emptyToolProvider struct{}

func (p *emptyToolProvider) GetTools(*bots.Bot) []llm.Tool {
	return nil
}

type countingMCPToolProvider struct {
	calls int
}

func (p *countingMCPToolProvider) GetToolsForUser(string) ([]llm.Tool, *mcp.Errors) {
	p.calls++
	return []llm.Tool{
		{
			Name:        "test_tool",
			Description: "test tool",
			Schema:      llm.NewJSONSchemaFromStruct[struct{}](),
		},
	}, nil
}

type contextTestConfigProvider struct{}

func (p *contextTestConfigProvider) GetEnableLLMTrace() bool {
	return false
}

func (p *contextTestConfigProvider) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}

func newTestBot() *bots.Bot {
	return bots.NewBot(
		llm.BotConfig{ID: "bot-id", Name: "matty", DisplayName: "Matty"},
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: "bot-id", Username: "matty", DisplayName: "Matty"},
		nil,
	)
}

func TestWithLLMContextDefaultToolsCallsMCPProvider(t *testing.T) {
	mockAPI := &plugintest.API{}
	siteName := "Mattermost"
	siteURL := "https://example.com"
	mockAPI.On("GetConfig").Return(&model.Config{
		TeamSettings:    model.TeamSettings{SiteName: &siteName},
		ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
	}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{}).Maybe()

	client := pluginapi.NewClient(mockAPI, nil)
	mcpProvider := &countingMCPToolProvider{}
	builder := NewLLMContextBuilder(client, &emptyToolProvider{}, mcpProvider, &contextTestConfigProvider{})

	user := &model.User{Id: "user-id", Username: "test-user", Locale: "en"}
	channel := &model.Channel{Id: "channel-id", Type: model.ChannelTypeDirect}

	context := builder.BuildLLMContextUserRequest(
		newTestBot(),
		user,
		channel,
		builder.WithLLMContextDefaultTools(newTestBot()),
	)

	require.Equal(t, 1, mcpProvider.calls)
	require.Len(t, context.Tools.GetTools(), 1)
}

func TestWithLLMContextNoToolsSkipsMCPProvider(t *testing.T) {
	mockAPI := &plugintest.API{}
	siteName := "Mattermost"
	siteURL := "https://example.com"
	mockAPI.On("GetConfig").Return(&model.Config{
		TeamSettings:    model.TeamSettings{SiteName: &siteName},
		ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
	}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{}).Maybe()

	client := pluginapi.NewClient(mockAPI, nil)
	mcpProvider := &countingMCPToolProvider{}
	builder := NewLLMContextBuilder(client, &emptyToolProvider{}, mcpProvider, &contextTestConfigProvider{})

	user := &model.User{Id: "user-id", Username: "test-user", Locale: "en"}
	channel := &model.Channel{Id: "channel-id", Type: model.ChannelTypeDirect}

	context := builder.BuildLLMContextUserRequest(
		newTestBot(),
		user,
		channel,
		builder.WithLLMContextNoTools(),
	)

	require.Equal(t, 0, mcpProvider.calls)
	require.Empty(t, context.Tools.GetTools())
}
