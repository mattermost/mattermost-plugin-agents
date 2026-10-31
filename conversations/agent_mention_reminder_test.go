// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	reminderBotID       = "bot-id"
	reminderBotUsername = "test-bot"
	reminderBotDisplay  = "Test Bot"
	// Well-formed 26-char IDs: the agent access gate denies a user ID no policy
	// can be evaluated against, so the reminder never reaches its own logic.
	reminderUserID      = "user12345678901234567890ab"
	reminderOtherUserID = "othe12345678901234567890ab"
	reminderChannelID   = "channel-id"
	reminderTeamID      = "team-id"
	reminderRootID      = "root-id"
	reminderReplyID     = "reply-id"
)

type reminderFixture struct {
	conv       *conversations.Conversations
	client     *fakeMMClient
	botService *bots.MMBots
	mockAPI    *plugintest.API
}

func newReminderFixture(t *testing.T) *reminderFixture {
	t.Helper()

	return newReminderFixtureWithBotConfig(t, llm.BotConfig{
		ID:                 reminderBotID,
		Name:               reminderBotUsername,
		DisplayName:        reminderBotDisplay,
		ChannelAccessLevel: llm.ChannelAccessLevelAll,
		UserAccessLevel:    llm.UserAccessLevelAll,
	})
}

func newReminderFixtureWithBotConfig(t *testing.T, botConfig llm.BotConfig) *reminderFixture {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuProfessional}).Maybe()
	mockAPI.On("GetTeam", mock.Anything).Return(&model.Team{Id: reminderTeamID, Name: "team"}, nil).Maybe()
	mockAPI.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
	pluginClient := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginClient)

	botService := bots.New(mockAPI, pluginClient, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	fLLM := newDMTestLLM(dmMakeTextStream("canned reply"))
	bot := bots.NewBot(
		botConfig,
		llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
		&model.Bot{UserId: reminderBotID, Username: reminderBotUsername, DisplayName: reminderBotDisplay},
		fLLM,
	)
	conversations.SetBotsWithAccounts(mockAPI, botService, bot)

	contextBuilder := llmcontext.NewLLMContextBuilder(pluginClient, &testToolProvider{}, nil, &mockConfigProvider{})
	promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	client := &fakeMMClient{
		users: map[string]*model.User{
			reminderUserID:      {Id: reminderUserID, Username: "user", Locale: "en"},
			reminderOtherUserID: {Id: reminderOtherUserID, Username: "other", Locale: "en"},
			reminderBotID:       {Id: reminderBotID, Username: reminderBotUsername, IsBot: true, Locale: "en"},
		},
		channels:        map[string]*model.Channel{},
		allowCreatePost: true,
	}

	conv := conversations.New(promptsManager, client, &fakeStreamingService{}, contextBuilder, botService, nil, licenseChecker, i18n.Init(), nil, &testToolCallingConfig{}, newDMPolicyChecker())
	conv.SetConversationService(conversation.NewService(newFakeConvStore(), promptsManager, client, botService))

	return &reminderFixture{
		conv:       conv,
		client:     client,
		botService: botService,
		mockAPI:    mockAPI,
	}
}

func (f *reminderFixture) setChannel(ch *model.Channel) {
	if ch != nil && ch.TeamId == "" && ch.Type != model.ChannelTypeDirect && ch.Type != model.ChannelTypeGroup {
		ch.TeamId = reminderTeamID
	}
	f.client.channels[ch.Id] = ch
}

func (f *reminderFixture) setThread(rootID string, posts ...*model.Post) {
	if f.client.postThreads == nil {
		f.client.postThreads = map[string]*model.PostList{}
	}
	postsByID := map[string]*model.Post{}
	order := make([]string, 0, len(posts))
	for _, p := range posts {
		postsByID[p.Id] = p
		order = append(order, p.Id)
	}
	list := &model.PostList{Order: order, Posts: postsByID}
	for _, p := range posts {
		f.client.postThreads[p.Id] = list
	}
	if rootID != "" && f.client.postThreads[rootID] == nil {
		f.client.postThreads[rootID] = list
	}
}

func TestMessageHasBeenPostedSendsReminderWhenPreviousPostIsAgent(t *testing.T) {
	cases := []struct {
		name            string
		channel         *model.Channel
		previousUserID  string
		replyMessage    string
		replyHasRootID  bool
		expectEphemeral bool
	}{
		{
			name:            "thread reply after agent post triggers reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			previousUserID:  reminderBotID,
			replyHasRootID:  true,
			expectEphemeral: true,
		},
		{
			name:            "thread reply after human post does not trigger reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			previousUserID:  reminderOtherUserID,
			replyHasRootID:  true,
			expectEphemeral: false,
		},
		{
			name:            "top-level post does not trigger reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			previousUserID:  reminderBotID,
			replyHasRootID:  false,
			expectEphemeral: false,
		},
		{
			name:            "thread reply in DM channel does not trigger reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeDirect},
			previousUserID:  reminderBotID,
			replyHasRootID:  true,
			expectEphemeral: false,
		},
		{
			name:            "thread reply in group DM channel does not trigger reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeGroup},
			previousUserID:  reminderBotID,
			replyHasRootID:  true,
			expectEphemeral: false,
		},
		{
			name:            "thread reply that already mentions the bot does not trigger reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			previousUserID:  reminderBotID,
			replyMessage:    "@" + reminderBotUsername + " thanks!",
			replyHasRootID:  true,
			expectEphemeral: false,
		},
		{
			name:            "thread reply with mixed-case bot mention does not trigger reminder",
			channel:         &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen},
			previousUserID:  reminderBotID,
			replyMessage:    "@Test-Bot thanks!",
			replyHasRootID:  true,
			expectEphemeral: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newReminderFixture(t)
			fix.setChannel(tc.channel)

			rootPost := &model.Post{
				Id:        reminderRootID,
				ChannelId: tc.channel.Id,
				UserId:    reminderUserID,
				CreateAt:  100,
				Message:   "kicking off",
			}
			previousPost := &model.Post{
				Id:        "prev-post-id",
				ChannelId: tc.channel.Id,
				UserId:    tc.previousUserID,
				RootId:    reminderRootID,
				CreateAt:  200,
				Message:   "previous message",
			}

			replyMessage := "thanks!"
			if tc.replyMessage != "" {
				replyMessage = tc.replyMessage
			}
			rootIDForReply := reminderRootID
			if !tc.replyHasRootID {
				rootIDForReply = ""
			}
			reply := &model.Post{
				Id:        reminderReplyID,
				ChannelId: tc.channel.Id,
				UserId:    reminderUserID,
				RootId:    rootIDForReply,
				CreateAt:  300,
				Message:   replyMessage,
			}

			if tc.replyHasRootID {
				fix.setThread(reminderRootID, rootPost, previousPost, reply)
			} else {
				fix.setThread(reminderReplyID, reply)
			}

			fix.conv.MessageHasBeenPosted(nil, reply)

			if tc.expectEphemeral {
				require.Len(t, fix.client.ephemeralPosts, 1, "expected one ephemeral reminder")
				require.Equal(t, reminderUserID, fix.client.ephemeralPostUserIDs[0])
				ephemeral := fix.client.ephemeralPosts[0]
				require.Equal(t, conversations.AgentMentionReminderPostType, ephemeral.GetProp("type"))
				require.Equal(t, tc.channel.Id, ephemeral.ChannelId)
				require.Equal(t, reminderRootID, ephemeral.RootId)
				require.Equal(t, reminderBotID, ephemeral.GetProp(conversations.AgentMentionReminderBotUserIDProp))
				require.Equal(t, reminderBotUsername, ephemeral.GetProp(conversations.AgentMentionReminderBotUsernameProp))
				require.Equal(t, reminderBotDisplay, ephemeral.GetProp(conversations.AgentMentionReminderBotDisplayNameProp))
				require.Equal(t, reminderReplyID, ephemeral.GetProp(conversations.AgentMentionReminderTargetPostIDProp))
				require.NotEmpty(t, ephemeral.Message)
			} else {
				for _, ephemeral := range fix.client.ephemeralPosts {
					require.NotEqual(t, conversations.AgentMentionReminderPostType, ephemeral.GetProp("type"), "expected no ephemeral reminder")
				}
			}
		})
	}
}

func TestMessageHasBeenPostedReminderShownOncePerThread(t *testing.T) {
	fix := newReminderFixture(t)
	channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen}
	fix.setChannel(channel)

	rootPost := &model.Post{Id: reminderRootID, ChannelId: channel.Id, UserId: reminderUserID, CreateAt: 100, Message: "start"}
	agent1 := &model.Post{Id: "agent-1", ChannelId: channel.Id, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 200, Message: "agent one"}
	reply1 := &model.Post{Id: "reply-1", ChannelId: channel.Id, UserId: reminderUserID, RootId: reminderRootID, CreateAt: 300, Message: "first reply"}
	agent2 := &model.Post{Id: "agent-2", ChannelId: channel.Id, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 400, Message: "agent two"}
	reply2 := &model.Post{Id: "reply-2", ChannelId: channel.Id, UserId: reminderUserID, RootId: reminderRootID, CreateAt: 500, Message: "second reply"}
	fix.setThread(reminderRootID, rootPost, agent1, reply1, agent2, reply2)

	fix.conv.MessageHasBeenPosted(nil, reply1)
	require.Len(t, fix.client.ephemeralPosts, 1, "the first reply after an agent post triggers the reminder")

	fix.conv.MessageHasBeenPosted(nil, reply2)
	require.Len(t, fix.client.ephemeralPosts, 1, "a second reply in the same thread must not trigger the reminder again")
}

func TestMessageHasBeenPostedReminderShownForEachUserInThread(t *testing.T) {
	fix := newReminderFixture(t)
	channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen}
	fix.setChannel(channel)

	rootPost := &model.Post{Id: reminderRootID, ChannelId: channel.Id, UserId: reminderUserID, CreateAt: 100, Message: "start"}
	agent1 := &model.Post{Id: "agent-1", ChannelId: channel.Id, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 200, Message: "agent one"}
	replyUser := &model.Post{Id: "reply-user", ChannelId: channel.Id, UserId: reminderUserID, RootId: reminderRootID, CreateAt: 300, Message: "reply from the first user"}
	agent2 := &model.Post{Id: "agent-2", ChannelId: channel.Id, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 400, Message: "agent two"}
	replyOther := &model.Post{Id: "reply-other", ChannelId: channel.Id, UserId: reminderOtherUserID, RootId: reminderRootID, CreateAt: 500, Message: "reply from the second user"}
	fix.setThread(reminderRootID, rootPost, agent1, replyUser, agent2, replyOther)

	fix.conv.MessageHasBeenPosted(nil, replyUser)
	fix.conv.MessageHasBeenPosted(nil, replyOther)

	require.Len(t, fix.client.ephemeralPosts, 2, "the per-thread marker is scoped per user, so each user's reply after an agent post is reminded")
	require.ElementsMatch(t, []string{reminderUserID, reminderOtherUserID}, fix.client.ephemeralPostUserIDs)
}

func TestMessageHasBeenPostedReminderShownAgainInDifferentThread(t *testing.T) {
	fix := newReminderFixture(t)
	channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen}
	fix.setChannel(channel)

	const otherRootID = "other-root-id"

	rootA := &model.Post{Id: reminderRootID, ChannelId: channel.Id, UserId: reminderUserID, CreateAt: 100, Message: "start a"}
	agentA := &model.Post{Id: "agent-a", ChannelId: channel.Id, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 200, Message: "agent a"}
	replyA := &model.Post{Id: "reply-a", ChannelId: channel.Id, UserId: reminderUserID, RootId: reminderRootID, CreateAt: 300, Message: "reply a"}
	fix.setThread(reminderRootID, rootA, agentA, replyA)

	rootB := &model.Post{Id: otherRootID, ChannelId: channel.Id, UserId: reminderUserID, CreateAt: 100, Message: "start b"}
	agentB := &model.Post{Id: "agent-b", ChannelId: channel.Id, UserId: reminderBotID, RootId: otherRootID, CreateAt: 200, Message: "agent b"}
	replyB := &model.Post{Id: "reply-b", ChannelId: channel.Id, UserId: reminderUserID, RootId: otherRootID, CreateAt: 300, Message: "reply b"}
	fix.setThread(otherRootID, rootB, agentB, replyB)

	fix.conv.MessageHasBeenPosted(nil, replyA)
	require.Len(t, fix.client.ephemeralPosts, 1)

	fix.conv.MessageHasBeenPosted(nil, replyB)
	require.Len(t, fix.client.ephemeralPosts, 2, "the reminder shows again in a different thread the user has not seen it in")
}

func TestMessageHasBeenPostedReminderNotShownWhenUserOptedOut(t *testing.T) {
	fix := newReminderFixture(t)
	channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen}
	fix.setChannel(channel)

	require.NoError(t, conversations.SaveAgentMentionReminderOptOut(fix.client, reminderUserID))

	rootPost := &model.Post{Id: reminderRootID, ChannelId: channel.Id, UserId: reminderUserID, CreateAt: 100, Message: "start"}
	agentPost := &model.Post{Id: "agent-post", ChannelId: channel.Id, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 200, Message: "agent response"}
	reply := &model.Post{Id: reminderReplyID, ChannelId: channel.Id, UserId: reminderUserID, RootId: reminderRootID, CreateAt: 300, Message: "thanks!"}
	fix.setThread(reminderRootID, rootPost, agentPost, reply)

	fix.conv.MessageHasBeenPosted(nil, reply)

	require.Empty(t, fix.client.ephemeralPosts, "a user who opted out never sees the reminder")
}

func TestMessageHasBeenPostedReminderHandlesNoPreviousPost(t *testing.T) {
	fix := newReminderFixture(t)
	channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen}
	fix.setChannel(channel)

	reply := &model.Post{
		Id:        reminderReplyID,
		ChannelId: channel.Id,
		UserId:    reminderUserID,
		RootId:    reminderRootID,
		CreateAt:  100,
		Message:   "lonely reply",
	}

	fix.setThread(reminderRootID, reply)

	fix.conv.MessageHasBeenPosted(nil, reply)

	require.Empty(t, fix.client.ephemeralPosts)
}

func TestMessageHasBeenPostedReminderUsesThreadOrderForEqualTimestamps(t *testing.T) {
	fix := newReminderFixture(t)
	channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen}
	fix.setChannel(channel)

	rootPost := &model.Post{
		Id:        reminderRootID,
		ChannelId: channel.Id,
		UserId:    reminderUserID,
		CreateAt:  100,
		Message:   "start",
	}
	humanPost := &model.Post{
		Id:        "h-prev",
		ChannelId: channel.Id,
		UserId:    reminderOtherUserID,
		RootId:    reminderRootID,
		CreateAt:  200,
		Message:   "human response",
	}
	agentPost := &model.Post{
		Id:        "a-prev",
		ChannelId: channel.Id,
		UserId:    reminderBotID,
		RootId:    reminderRootID,
		CreateAt:  200,
		Message:   "agent response",
	}
	reply := &model.Post{
		Id:        "r-reply",
		ChannelId: channel.Id,
		UserId:    reminderUserID,
		RootId:    reminderRootID,
		CreateAt:  200,
		Message:   "reply without mention",
	}

	fix.setThread(reminderRootID, rootPost, humanPost, agentPost, reply)

	fix.conv.MessageHasBeenPosted(nil, reply)

	require.Len(t, fix.client.ephemeralPosts, 1, "expected reminder based on immediate predecessor in thread order")
	ephemeral := fix.client.ephemeralPosts[0]
	require.Equal(t, reminderBotID, ephemeral.GetProp(conversations.AgentMentionReminderBotUserIDProp))
	require.Equal(t, reminderBotUsername, ephemeral.GetProp(conversations.AgentMentionReminderBotUsernameProp))
}

func TestMessageHasBeenPostedReminderUsesLastThreadPostWhenReplyMissingFromOrder(t *testing.T) {
	fix := newReminderFixture(t)
	channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen}
	fix.setChannel(channel)

	rootPost := &model.Post{
		Id:        reminderRootID,
		ChannelId: channel.Id,
		UserId:    reminderUserID,
		CreateAt:  100,
		Message:   "start",
	}
	agentPost := &model.Post{
		Id:        "a-prev",
		ChannelId: channel.Id,
		UserId:    reminderBotID,
		RootId:    reminderRootID,
		CreateAt:  200,
		Message:   "agent response",
	}
	reply := &model.Post{
		Id:        reminderReplyID,
		ChannelId: channel.Id,
		UserId:    reminderUserID,
		RootId:    reminderRootID,
		CreateAt:  300,
		Message:   "reply without mention",
	}
	fix.setThread(reminderRootID, rootPost, agentPost)

	fix.conv.MessageHasBeenPosted(nil, reply)

	require.Len(t, fix.client.ephemeralPosts, 1)
	ephemeral := fix.client.ephemeralPosts[0]
	require.Equal(t, reminderBotID, ephemeral.GetProp(conversations.AgentMentionReminderBotUserIDProp))
	require.Equal(t, reminderReplyID, ephemeral.GetProp(conversations.AgentMentionReminderTargetPostIDProp))
}

func TestMessageHasBeenPostedReminderSkipsRestrictedBot(t *testing.T) {
	fix := newReminderFixtureWithBotConfig(t, llm.BotConfig{
		ID:                 reminderBotID,
		Name:               reminderBotUsername,
		DisplayName:        reminderBotDisplay,
		ChannelAccessLevel: llm.ChannelAccessLevelNone,
		UserAccessLevel:    llm.UserAccessLevelAll,
	})
	channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen}
	fix.setChannel(channel)

	rootPost := &model.Post{
		Id:        reminderRootID,
		ChannelId: channel.Id,
		UserId:    reminderUserID,
		CreateAt:  100,
		Message:   "start",
	}
	previousPost := &model.Post{
		Id:        "prev-post-id",
		ChannelId: channel.Id,
		UserId:    reminderBotID,
		RootId:    reminderRootID,
		CreateAt:  200,
		Message:   "agent response",
	}
	reply := &model.Post{
		Id:        reminderReplyID,
		ChannelId: channel.Id,
		UserId:    reminderUserID,
		RootId:    reminderRootID,
		CreateAt:  300,
		Message:   "reply without mention",
	}

	fix.setThread(reminderRootID, rootPost, previousPost, reply)

	fix.conv.MessageHasBeenPosted(nil, reply)

	require.Empty(t, fix.client.ephemeralPosts)
}

// TestMessageHasBeenPostedReminderByLicenseLevel pins that the reminder, which
// points at channel mentions and loop-in, appears only where multiplayer
// agents in channels are available.
func TestMessageHasBeenPostedReminderByLicenseLevel(t *testing.T) {
	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			fix := newReminderFixture(t)
			calls := fix.mockAPI.ExpectedCalls[:0]
			for _, call := range fix.mockAPI.ExpectedCalls {
				if call.Method != "GetLicense" {
					calls = append(calls, call)
				}
			}
			fix.mockAPI.ExpectedCalls = calls
			fix.mockAPI.On("GetLicense").Return(enterprisetest.LicenseFor(level)).Maybe()

			channel := &model.Channel{Id: reminderChannelID, Type: model.ChannelTypeOpen}
			fix.setChannel(channel)
			root := &model.Post{Id: reminderRootID, ChannelId: channel.Id, UserId: reminderUserID, CreateAt: 100, Message: "kicking off"}
			previous := &model.Post{Id: "prev-post-id", ChannelId: channel.Id, UserId: reminderBotID, RootId: reminderRootID, CreateAt: 200, Message: "previous message"}
			reply := &model.Post{Id: reminderReplyID, ChannelId: channel.Id, UserId: reminderUserID, RootId: reminderRootID, CreateAt: 300, Message: "thanks!"}
			fix.setThread(reminderRootID, root, previous, reply)

			fix.conv.MessageHasBeenPosted(nil, reply)

			reminders := 0
			for _, ephemeral := range fix.client.ephemeralPosts {
				if ephemeral.GetProp("type") == conversations.AgentMentionReminderPostType {
					reminders++
				}
			}
			want := 0
			if level >= enterprise.RequiredLevel(enterprise.CapMultiplayerChannels) {
				want = 1
			}
			require.Equal(t, want, reminders)
		})
	}
}
