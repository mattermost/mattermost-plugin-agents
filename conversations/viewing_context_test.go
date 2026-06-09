// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newViewingTestBuilder wires a real llmcontext.Builder against a plugintest API,
// so the test exercises the same code path the production handler does. The
// returned API is exposed so callers can register Team.Get expectations
// (which the builder calls through pluginapi, not mmapi.Client).
func newViewingTestBuilder(t *testing.T) (*llmcontext.Builder, *plugintest.API) {
	t.Helper()
	mockAPI := &plugintest.API{}
	siteName := "Mattermost"
	siteURL := "https://example.com"
	mockAPI.On("GetConfig").Return(&model.Config{
		TeamSettings:    model.TeamSettings{SiteName: &siteName},
		ServiceSettings: model.ServiceSettings{SiteURL: &siteURL},
	}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{}).Maybe()
	t.Cleanup(func() { mockAPI.AssertExpectations(t) })
	client := pluginapi.NewClient(mockAPI, nil)
	return llmcontext.NewLLMContextBuilder(client, nil, nil, nil), mockAPI
}

func postWithViewingProps(channelID, teamID string) *model.Post {
	p := &model.Post{Id: "post1", UserId: "user1", ChannelId: "botdm"}
	if channelID != "" {
		p.AddProp(ViewingChannelIDProp, channelID)
	}
	if teamID != "" {
		p.AddProp(ViewingTeamIDProp, teamID)
	}
	return p
}

func TestResolveViewingContextOption_NoOpCases(t *testing.T) {
	builder, _ := newViewingTestBuilder(t)

	tests := []struct {
		name             string
		post             *model.Post
		conversationID   string
		expectPermCheck  bool
		expectLoadCalled bool
	}{
		{
			name:           "no prop, no calls made",
			post:           &model.Post{Id: "p", UserId: "u"},
			conversationID: "botdm",
		},
		{
			name:           "prop matches conversation channel, skip",
			post:           postWithViewingProps("botdm", "team1"),
			conversationID: "botdm",
		},
		{
			name:           "non-string prop is ignored",
			post:           func() *model.Post { p := &model.Post{Id: "p"}; p.AddProp(ViewingChannelIDProp, 42); return p }(),
			conversationID: "botdm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := mocks.NewMockClient(t)
			ctx := &llm.Context{}
			opt := resolveViewingContextOption(client, builder, tt.post, "user1", tt.conversationID)
			opt(ctx)
			assert.Nil(t, ctx.ViewingChannel)
			assert.Nil(t, ctx.ViewingTeam)
		})
	}
}

func TestResolveViewingContextOption_PermissionDenied(t *testing.T) {
	builder, _ := newViewingTestBuilder(t)
	client := mocks.NewMockClient(t)

	client.EXPECT().
		HasPermissionToChannel("user1", "viewing-ch", model.PermissionReadChannel).
		Return(false).Once()
	client.EXPECT().LogDebug(mock.Anything, mock.Anything).Maybe()

	post := postWithViewingProps("viewing-ch", "team1")
	opt := resolveViewingContextOption(client, builder, post, "user1", "botdm")

	ctx := &llm.Context{}
	opt(ctx)

	assert.Nil(t, ctx.ViewingChannel)
	assert.Nil(t, ctx.ViewingTeam)
}

func TestResolveViewingContextOption_ChannelLoadError(t *testing.T) {
	builder, _ := newViewingTestBuilder(t)
	client := mocks.NewMockClient(t)

	client.EXPECT().
		HasPermissionToChannel("user1", "viewing-ch", model.PermissionReadChannel).
		Return(true).Once()
	client.EXPECT().GetChannel("viewing-ch").Return(nil, errors.New("not found")).Once()
	client.EXPECT().LogDebug(mock.Anything, mock.Anything).Maybe()

	post := postWithViewingProps("viewing-ch", "team1")
	opt := resolveViewingContextOption(client, builder, post, "user1", "botdm")
	ctx := &llm.Context{}
	opt(ctx)

	assert.Nil(t, ctx.ViewingChannel)
	assert.Nil(t, ctx.ViewingTeam)
}

func TestResolveViewingContextOption_AttachesChannelAndTeam(t *testing.T) {
	builder, mockAPI := newViewingTestBuilder(t)
	client := mocks.NewMockClient(t)

	channel := &model.Channel{Id: "viewing-ch", Name: "town-square", DisplayName: "Town Square", Type: model.ChannelTypeOpen, TeamId: "team1"}
	team := &model.Team{Id: "team1", Name: "engineering", DisplayName: "Engineering"}

	client.EXPECT().
		HasPermissionToChannel("user1", "viewing-ch", model.PermissionReadChannel).
		Return(true).Once()
	client.EXPECT().GetChannel("viewing-ch").Return(channel, nil).Once()
	client.EXPECT().GetTeam("team1").Return(team, nil).Once()
	// The builder's WithLLMContextViewingChannel option additionally resolves
	// the team via pluginapi for non-DM/non-group channels.
	mockAPI.On("GetTeam", "team1").Return(team, nil).Maybe()

	post := postWithViewingProps("viewing-ch", "team1")
	opt := resolveViewingContextOption(client, builder, post, "user1", "botdm")
	ctx := &llm.Context{}
	opt(ctx)

	require.NotNil(t, ctx.ViewingChannel)
	assert.Equal(t, "town-square", ctx.ViewingChannel.Name)
	require.NotNil(t, ctx.ViewingTeam)
	assert.Equal(t, "engineering", ctx.ViewingTeam.Name)
}

func TestResolveViewingContextOption_TeamMismatchIgnored(t *testing.T) {
	builder, mockAPI := newViewingTestBuilder(t)
	client := mocks.NewMockClient(t)

	channel := &model.Channel{Id: "viewing-ch", Type: model.ChannelTypeOpen, TeamId: "real-team"}

	client.EXPECT().
		HasPermissionToChannel("user1", "viewing-ch", model.PermissionReadChannel).
		Return(true).Once()
	client.EXPECT().GetChannel("viewing-ch").Return(channel, nil).Once()

	// Caller claims team "other-team" but channel is actually on "real-team"; team
	// is not attached via the explicit prop path. The builder's
	// WithLLMContextViewingChannel still looks up the channel's real team via
	// pluginapi.
	mockAPI.On("GetTeam", "real-team").Return(&model.Team{Id: "real-team", Name: "real"}, nil).Maybe()

	post := postWithViewingProps("viewing-ch", "other-team")
	opt := resolveViewingContextOption(client, builder, post, "user1", "botdm")
	ctx := &llm.Context{}
	opt(ctx)

	require.NotNil(t, ctx.ViewingChannel)
	assert.Equal(t, "viewing-ch", ctx.ViewingChannel.Id)
}

func TestResolveViewingContextOption_DMChannelHasNoTeam(t *testing.T) {
	builder, _ := newViewingTestBuilder(t)
	client := mocks.NewMockClient(t)

	channel := &model.Channel{Id: "viewing-ch", Type: model.ChannelTypeDirect}

	client.EXPECT().
		HasPermissionToChannel("user1", "viewing-ch", model.PermissionReadChannel).
		Return(true).Once()
	client.EXPECT().GetChannel("viewing-ch").Return(channel, nil).Once()

	post := postWithViewingProps("viewing-ch", "")
	opt := resolveViewingContextOption(client, builder, post, "user1", "botdm")
	ctx := &llm.Context{}
	opt(ctx)

	require.NotNil(t, ctx.ViewingChannel)
	assert.Equal(t, model.ChannelTypeDirect, ctx.ViewingChannel.Type)
	assert.Nil(t, ctx.ViewingTeam)
}

func TestResolveViewingContextOption_NilArgs(t *testing.T) {
	post := postWithViewingProps("viewing-ch", "team1")
	opt := resolveViewingContextOption(nil, nil, post, "user1", "botdm")
	ctx := &llm.Context{}
	opt(ctx)
	assert.Nil(t, ctx.ViewingChannel)
}
