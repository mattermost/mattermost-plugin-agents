// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func applyLanguageModelOptions(opts []llm.LanguageModelOption) llm.LanguageModelConfig {
	cfg := llm.LanguageModelConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

type nativeWebSearchChannelConfig struct{}

func (nativeWebSearchChannelConfig) EnableChannelMentionToolCalling() bool { return true }
func (nativeWebSearchChannelConfig) AllowNativeWebSearchInChannels() bool  { return true }
func (nativeWebSearchChannelConfig) MCP() mcp.Config                       { return mcp.Config{} }

func TestWithProviderWebSearchLicense(t *testing.T) {
	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			c := &Conversations{licenseChecker: enterprisetest.CheckerAt(level)}
			cfg := applyLanguageModelOptions(c.withProviderWebSearchLicense(nil))
			if level >= enterprise.LevelProfessional {
				require.False(t, cfg.SkipNativeWebSearch)
				return
			}
			require.True(t, cfg.SkipNativeWebSearch)
		})
	}

	t.Run("nil checker fails closed", func(t *testing.T) {
		c := &Conversations{}
		cfg := applyLanguageModelOptions(c.withProviderWebSearchLicense(nil))
		require.True(t, cfg.SkipNativeWebSearch)
	})
}

func TestToolsDisabledLLMOptionsProviderWebSearch(t *testing.T) {
	bot := bots.NewBot(
		llm.BotConfig{
			ID:                 "bot-id",
			Name:               "matty",
			EnabledNativeTools: []string{llm.NativeToolWebSearch},
		},
		llm.ServiceConfig{Type: llm.ServiceTypeOpenAI, DefaultModel: "gpt-4o"},
		&model.Bot{UserId: "bot-id", Username: "matty"},
		nil,
	)

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			c := &Conversations{
				licenseChecker: enterprisetest.CheckerAt(level),
				configProvider: nativeWebSearchChannelConfig{},
			}
			cfg := applyLanguageModelOptions(c.toolsDisabledLLMOptions(bot, true))
			require.True(t, cfg.ToolsDisabled)
			if level >= enterprise.LevelProfessional {
				require.True(t, cfg.NativeWebSearchAllowed)
				return
			}
			require.False(t, cfg.NativeWebSearchAllowed)
		})
	}

	t.Run("nil checker fails closed", func(t *testing.T) {
		c := &Conversations{configProvider: nativeWebSearchChannelConfig{}}
		cfg := applyLanguageModelOptions(c.toolsDisabledLLMOptions(bot, true))
		require.True(t, cfg.ToolsDisabled)
		require.False(t, cfg.NativeWebSearchAllowed)
	})
}
