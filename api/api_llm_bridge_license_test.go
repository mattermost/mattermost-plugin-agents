// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"io"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/public/bridgeclient"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestBridgeCompletionProviderWebSearchByLevel pins that bridge completions
// attach the provider-native web search tool at Professional and above only,
// and never below (including when license state is unavailable).
func TestBridgeCompletionProviderWebSearchByLevel(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name    string
		level   *enterprise.Level
		allowed bool
	}{
		{name: "nil checker fails closed", level: nil, allowed: false},
	}
	for _, level := range enterprisetest.AllLevels {
		lvl := level
		tests = append(tests, struct {
			name    string
			level   *enterprise.Level
			allowed bool
		}{name: lvl.String(), level: &lvl, allowed: lvl >= enterprise.RequiredLevel(enterprise.CapProviderWebSearch)})
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			if tc.level == nil {
				e.api.licenseChecker = nil
			} else {
				e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
				e.OverrideLicense(enterprisetest.LicenseFor(*tc.level))
			}

			fakeLLM := NewFakeLLM("ok")
			bot := bots.NewBot(llm.BotConfig{
				Name:               "websearchbot",
				DisplayName:        "Web Search Bot",
				UserAccessLevel:    llm.UserAccessLevelAll,
				EnabledNativeTools: []string{llm.NativeToolWebSearch},
			}, llm.ServiceConfig{Type: llm.ServiceTypeAnthropic}, &model.Bot{
				UserId:      testBotUserID,
				Username:    "websearchbot",
				DisplayName: "Web Search Bot",
			}, fakeLLM)
			e.bots.SetBotsForTesting([]*bots.Bot{bot})
			require.True(t, bot.HasNativeWebSearchEnabled())
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			client := e.CreateBridgeClient()
			_, err := client.AgentCompletion(testBotUserID, bridgeclient.CompletionRequest{
				Posts: []bridgeclient.Post{{Role: "user", Message: "Hello"}},
			})
			require.NoError(t, err)

			require.Equal(t, tc.allowed, fakeLLM.LastConfig.NativeWebSearchAllowed && !fakeLLM.LastConfig.SkipNativeWebSearch)
		})
	}
}
