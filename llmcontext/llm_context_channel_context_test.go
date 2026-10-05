// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type fakeChannelContextProvider struct {
	result *llm.ChannelContext
	err    error
	calls  []string
}

func (p *fakeChannelContextProvider) PromptContext(channelID string) (*llm.ChannelContext, error) {
	p.calls = append(p.calls, channelID)
	return p.result, p.err
}

func TestBuildLLMContextUserRequestChannelContext(t *testing.T) {
	stored := &llm.ChannelContext{Instructions: "Deploys freeze on Fridays.", PinnedPosts: "Post ID p1 by @alice:\nhello"}

	tests := []struct {
		name          string
		channelType   model.ChannelType
		level         enterprise.Level
		canRead       bool
		noUser        bool
		providerErr   error
		want          *llm.ChannelContext
		wantProviders int
	}{
		{name: "public channel", channelType: model.ChannelTypeOpen, level: enterprise.LevelEnterpriseAdvanced, canRead: true, want: stored, wantProviders: 1},
		{name: "private channel", channelType: model.ChannelTypePrivate, level: enterprise.LevelEnterpriseAdvanced, canRead: true, want: stored, wantProviders: 1},
		{name: "direct message", channelType: model.ChannelTypeDirect, level: enterprise.LevelEnterpriseAdvanced, canRead: true},
		{name: "group message", channelType: model.ChannelTypeGroup, level: enterprise.LevelEnterpriseAdvanced, canRead: true},
		{name: "below Enterprise Advanced", channelType: model.ChannelTypeOpen, level: enterprise.LevelEnterprise, canRead: true},
		{name: "requester cannot read the channel", channelType: model.ChannelTypeOpen, level: enterprise.LevelEnterpriseAdvanced, canRead: false},
		{name: "no requesting user", channelType: model.ChannelTypeOpen, level: enterprise.LevelEnterpriseAdvanced, canRead: true, noUser: true},
		{name: "provider failure omits context", channelType: model.ChannelTypeOpen, level: enterprise.LevelEnterpriseAdvanced, canRead: true, providerErr: errors.New("db down"), wantProviders: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: tc.channelType}
			user := testUser()
			if tc.noUser {
				user = nil
			}

			mockAPI := &plugintest.API{}
			mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
			mockAPI.On("GetLicense").Return(enterprisetest.LicenseFor(tc.level)).Maybe()
			mockAPI.On("GetTeam", "team-id").Return(&model.Team{Id: "team-id"}, nil).Maybe()
			mockAPI.On("HasPermissionToChannel", "user-id", "channel-id", model.PermissionReadChannel).Return(tc.canRead).Maybe()
			mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()

			provider := &fakeChannelContextProvider{result: stored, err: tc.providerErr}
			builder := NewLLMContextBuilder(pluginapi.NewClient(mockAPI, nil), &emptyToolProvider{}, nil, &contextTestConfigProvider{})
			builder.SetChannelContextProvider(provider)

			context := builder.BuildLLMContextUserRequest(newTestBotWithConfig(llm.BotConfig{Name: "matty"}), user, channel)

			require.Equal(t, tc.want, context.ChannelContext)
			require.Len(t, provider.calls, tc.wantProviders, "the provider is only consulted when every gate passes")
		})
	}

	t.Run("no provider configured", func(t *testing.T) {
		mockAPI := &plugintest.API{}
		mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
		mockAPI.On("GetLicense").Return(enterprisetest.LicenseFor(enterprise.LevelEnterpriseAdvanced)).Maybe()
		mockAPI.On("GetTeam", "team-id").Return(&model.Team{Id: "team-id"}, nil).Maybe()
		builder := NewLLMContextBuilder(pluginapi.NewClient(mockAPI, nil), &emptyToolProvider{}, nil, &contextTestConfigProvider{})

		context := builder.BuildLLMContextUserRequest(newTestBotWithConfig(llm.BotConfig{Name: "matty"}), testUser(),
			&model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen})

		require.Nil(t, context.ChannelContext)
	})
}
