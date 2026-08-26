// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	autoReplyBotUserID    = "arbot-user-id"
	autoReplyBotUsername  = "arbot"
	autoReplyBot2UserID   = "secondbot-user-id"
	autoReplyBot2Username = "secondbot"
	autoReplyUserID       = "aruser-id"
	autoReplyOtherUserID  = "arother-id"
	autoReplyForeignBotID = "arforeignbot-id"
	autoReplyChannelID    = "archannel-id"
	autoReplyTeamID       = "arteam-id"
	autoReplyRootID       = "arroot-id"
	autoReplyReplyID      = "arreply-id"
	autoReplyAgentPostID  = "aragent-post-id"
)

// fakeAutoReplySettings is a hand-rolled conversations.AutoReplySettings.
type fakeAutoReplySettings struct {
	settings map[string]autoreply.Setting
}

func (f *fakeAutoReplySettings) GetCached(channelID string) (autoreply.Setting, bool) {
	setting, ok := f.settings[channelID]
	return setting, ok
}

func (f *fakeAutoReplySettings) set(setting autoreply.Setting) {
	f.settings[setting.ChannelID] = setting
}

type autoReplyTestEnv struct {
	conversations *conversations.Conversations
	convStore     *fakeConvStore
	mmClient      *fakeMMClient
	mockAPI       *plugintest.API
	botService    *bots.MMBots
	settings      *fakeAutoReplySettings
	channel       *model.Channel
}

// autoReplyBotConfig is the default unrestricted agent configuration used by
// most cases; the recheck tests pass restricted variants.
func autoReplyBotConfig() llm.BotConfig {
	return llm.BotConfig{
		ID:                 autoReplyBotUserID,
		Name:               autoReplyBotUsername,
		DisplayName:        "AR Bot",
		ChannelAccessLevel: llm.ChannelAccessLevelAll,
		UserAccessLevel:    llm.UserAccessLevelAll,
	}
}

func autoReplySecondBotConfig() llm.BotConfig {
	return llm.BotConfig{
		ID:                 autoReplyBot2UserID,
		Name:               autoReplyBot2Username,
		DisplayName:        "Second Bot",
		ChannelAccessLevel: llm.ChannelAccessLevelAll,
		UserAccessLevel:    llm.UserAccessLevelAll,
	}
}

// setupAutoReplyTestEnv builds a Conversations service around an open channel
// with the given agents registered (all backed by the same canned-response
// LLM), a licensed server, and a fake auto-reply settings lookup wired in.
func setupAutoReplyTestEnv(t *testing.T, botConfigs []llm.BotConfig, llmResponses ...*llm.TextStreamResult) *autoReplyTestEnv {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	mockAPI.On("GetLicense").Return(&model.License{SkuShortName: model.LicenseShortSkuEnterprise}).Maybe()
	mockAPI.On("GetTeam", mock.Anything).Return(&model.Team{Id: autoReplyTeamID, Name: "team"}, nil).Maybe()
	for i := 1; i <= 10; i++ {
		args := make([]any, i)
		for j := range args {
			args[j] = mock.Anything
		}
		mockAPI.On("LogDebug", args...).Maybe()
		mockAPI.On("LogInfo", args...).Maybe()
		mockAPI.On("LogWarn", args...).Maybe()
		mockAPI.On("LogError", args...).Maybe()
	}
	pluginClient := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginClient)

	botService := bots.New(mockAPI, pluginClient, licenseChecker, nil, nil, &http.Client{}, nil)
	fLLM := newDMTestLLM(llmResponses...)
	registered := make([]*bots.Bot, 0, len(botConfigs))
	for _, cfg := range botConfigs {
		registered = append(registered, bots.NewBot(
			cfg,
			llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
			&model.Bot{UserId: cfg.ID, Username: cfg.Name, DisplayName: cfg.DisplayName},
			fLLM,
		))
	}
	botService.SetBotsForTesting(registered)

	channel := &model.Channel{Id: autoReplyChannelID, Type: model.ChannelTypeOpen, TeamId: autoReplyTeamID}
	mmClient := &fakeMMClient{
		users: map[string]*model.User{
			autoReplyUserID:       {Id: autoReplyUserID, Username: "aruser", Locale: "en"},
			autoReplyOtherUserID:  {Id: autoReplyOtherUserID, Username: "arother", Locale: "en"},
			autoReplyBotUserID:    {Id: autoReplyBotUserID, Username: autoReplyBotUsername, IsBot: true, Locale: "en"},
			autoReplyBot2UserID:   {Id: autoReplyBot2UserID, Username: autoReplyBot2Username, IsBot: true, Locale: "en"},
			autoReplyForeignBotID: {Id: autoReplyForeignBotID, Username: "arforeignbot", IsBot: true, Locale: "en"},
		},
		channels:        map[string]*model.Channel{autoReplyChannelID: channel},
		allowCreatePost: true,
	}

	contextBuilder := llmcontext.NewLLMContextBuilder(pluginClient, &testToolProvider{}, nil, &mockConfigProvider{})
	promptsManager, err := llm.NewPrompts(prompts.PromptsFolder)
	require.NoError(t, err)

	convFakeStore := newFakeConvStore()
	convSvc := conversation.NewService(convFakeStore, promptsManager, mmClient, botService)

	convs := conversations.New(
		promptsManager,
		mmClient,
		&fakeStreamingService{},
		contextBuilder,
		botService,
		nil, // db
		licenseChecker,
		i18n.Init(),
		nil, // meetings
		&testToolCallingConfig{},
	)
	convs.SetConversationService(convSvc)

	settings := &fakeAutoReplySettings{settings: map[string]autoreply.Setting{}}
	convs.SetAutoReplySettings(settings)

	return &autoReplyTestEnv{
		conversations: convs,
		convStore:     convFakeStore,
		mmClient:      mmClient,
		mockAPI:       mockAPI,
		botService:    botService,
		settings:      settings,
		channel:       channel,
	}
}

func (env *autoReplyTestEnv) setThread(rootID string, posts ...*model.Post) {
	if env.mmClient.postThreads == nil {
		env.mmClient.postThreads = map[string]*model.PostList{}
	}
	postsByID := map[string]*model.Post{}
	order := make([]string, 0, len(posts))
	for _, p := range posts {
		postsByID[p.Id] = p
		order = append(order, p.Id)
	}
	list := &model.PostList{Order: order, Posts: postsByID}
	for _, p := range posts {
		env.mmClient.postThreads[p.Id] = list
	}
	if rootID != "" && env.mmClient.postThreads[rootID] == nil {
		env.mmClient.postThreads[rootID] = list
	}
}

// overrideLicense replaces the default Enterprise license expectation.
// Testify matches the first registered expectation, so the default must be
// filtered out rather than shadowed.
func (env *autoReplyTestEnv) overrideLicense(license *model.License) {
	filtered := make([]*mock.Call, 0, len(env.mockAPI.ExpectedCalls))
	for _, call := range env.mockAPI.ExpectedCalls {
		if call.Method != "GetLicense" {
			filtered = append(filtered, call)
		}
	}
	env.mockAPI.ExpectedCalls = filtered
	env.mockAPI.On("GetLicense").Return(license).Maybe()
}

// allConversations snapshots every conversation in the fake store.
func allConversations(s *fakeConvStore) []*store.Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*store.Conversation, 0, len(s.conversations))
	for _, c := range s.conversations {
		cp := *c
		out = append(out, &cp)
	}
	return out
}

// singleConversationUserBlocks asserts exactly one conversation exists and
// returns the content blocks of its initial user turn.
func singleConversationUserBlocks(t *testing.T, env *autoReplyTestEnv) []conversation.ContentBlock {
	t.Helper()
	convs := allConversations(env.convStore)
	require.Len(t, convs, 1, "expected exactly one conversation")
	turns := env.convStore.turnsFor(convs[0].ID)
	require.NotEmpty(t, turns)
	require.Equal(t, "user", turns[0].Role)
	var blocks []conversation.ContentBlock
	require.NoError(t, json.Unmarshal(turns[0].Content, &blocks))
	require.NotEmpty(t, blocks)
	return blocks
}

// Post builders. Every builder seeds the thread data handleMentions needs to
// build the completion request.

func (env *autoReplyTestEnv) rootPost(userID, message string) *model.Post {
	post := &model.Post{
		Id:        autoReplyRootID,
		ChannelId: autoReplyChannelID,
		UserId:    userID,
		CreateAt:  300,
		Message:   message,
	}
	env.setThread(post.Id, post)
	return post
}

// threadReply builds a thread reply; when afterAgentPost is true the
// immediately preceding post in the thread is authored by the registered
// agent (the shape that produces the mention reminder today).
func (env *autoReplyTestEnv) threadReply(userID, message string, afterAgentPost bool) *model.Post {
	root := &model.Post{
		Id:        autoReplyRootID,
		ChannelId: autoReplyChannelID,
		UserId:    autoReplyOtherUserID,
		CreateAt:  100,
		Message:   "kicking off",
	}
	reply := &model.Post{
		Id:        autoReplyReplyID,
		ChannelId: autoReplyChannelID,
		UserId:    userID,
		RootId:    autoReplyRootID,
		CreateAt:  300,
		Message:   message,
	}
	if afterAgentPost {
		agentPost := &model.Post{
			Id:        autoReplyAgentPostID,
			ChannelId: autoReplyChannelID,
			UserId:    autoReplyBotUserID,
			RootId:    autoReplyRootID,
			CreateAt:  200,
			Message:   "agent response",
		}
		env.setThread(autoReplyRootID, root, agentPost, reply)
	} else {
		env.setThread(autoReplyRootID, root, reply)
	}
	return reply
}

func TestAutoReplyTriggerMatrix(t *testing.T) {
	cases := []struct {
		name        string
		settingMode autoreply.Mode // "" means no setting row for the channel
		buildPost   func(env *autoReplyTestEnv) *model.Post

		expectFired         bool
		expectedRootID      string // placeholder RootId when fired
		expectedBotUserID   string // placeholder author when fired
		expectedConvMessage string // initial user-turn text when fired
		expectReminder      bool
	}{
		{
			name:        "no setting, root post is ignored",
			settingMode: "",
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "hello there")
			},
		},
		{
			name:        "no setting, thread reply after agent post keeps the reminder",
			settingMode: "",
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyUserID, "thanks!", true)
			},
			expectReminder: true,
		},
		{
			name:        "no setting, explicit mention fires the mention path",
			settingMode: "",
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" help me")
			},
			expectFired:         true,
			expectedRootID:      autoReplyRootID,
			expectedBotUserID:   autoReplyBotUserID,
			expectedConvMessage: "@" + autoReplyBotUsername + " help me",
		},
		{
			name:        "root_posts fires on a root post",
			settingMode: autoreply.ModeRootPosts,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "hello there")
			},
			expectFired:         true,
			expectedRootID:      autoReplyRootID,
			expectedBotUserID:   autoReplyBotUserID,
			expectedConvMessage: "@" + autoReplyBotUsername + " hello there",
		},
		{
			name:        "root_posts ignores thread replies but still sends the reminder",
			settingMode: autoreply.ModeRootPosts,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyUserID, "thanks!", true)
			},
			expectReminder: true,
		},
		{
			name:        "root_posts thread reply after a human post gets neither reply nor reminder",
			settingMode: autoreply.ModeRootPosts,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyUserID, "thanks!", false)
			},
		},
		{
			name:        "root_posts ignores a bot post without activate_ai",
			settingMode: autoreply.ModeRootPosts,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyForeignBotID, "bot announcement")
			},
		},
		{
			name:        "root_posts fires for a bot post with activate_ai",
			settingMode: autoreply.ModeRootPosts,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				post := env.rootPost(autoReplyForeignBotID, "bot announcement")
				post.AddProp(conversations.ActivateAIProp, "true")
				return post
			},
			expectFired:         true,
			expectedRootID:      autoReplyRootID,
			expectedBotUserID:   autoReplyBotUserID,
			expectedConvMessage: "@" + autoReplyBotUsername + " bot announcement",
		},
		{
			name:        "explicit mention of a different agent wins over the setting",
			settingMode: autoreply.ModeRootPosts,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "@"+autoReplyBot2Username+" ping")
			},
			expectFired:         true,
			expectedRootID:      autoReplyRootID,
			expectedBotUserID:   autoReplyBot2UserID,
			expectedConvMessage: "@" + autoReplyBot2Username + " ping",
		},
		{
			name:        "threads fires on a root post",
			settingMode: autoreply.ModeThreads,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "hello there")
			},
			expectFired:         true,
			expectedRootID:      autoReplyRootID,
			expectedBotUserID:   autoReplyBotUserID,
			expectedConvMessage: "@" + autoReplyBotUsername + " hello there",
		},
		{
			name:        "threads fires on a thread reply in the same thread",
			settingMode: autoreply.ModeThreads,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyUserID, "thanks!", false)
			},
			expectFired:         true,
			expectedRootID:      autoReplyRootID,
			expectedBotUserID:   autoReplyBotUserID,
			expectedConvMessage: "@" + autoReplyBotUsername + " thanks!",
		},
		{
			name:        "threads replying after an agent post fires without the reminder",
			settingMode: autoreply.ModeThreads,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyUserID, "thanks!", true)
			},
			expectFired:         true,
			expectedRootID:      autoReplyRootID,
			expectedBotUserID:   autoReplyBotUserID,
			expectedConvMessage: "@" + autoReplyBotUsername + " thanks!",
		},
		{
			name:        "threads ignores a bot thread reply without activate_ai",
			settingMode: autoreply.ModeThreads,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyForeignBotID, "bot follow-up", false)
			},
		},
		{
			name:        "threads defers to the mention path on explicit mention",
			settingMode: autoreply.ModeThreads,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" help me")
			},
			expectFired:         true,
			expectedRootID:      autoReplyRootID,
			expectedBotUserID:   autoReplyBotUserID,
			expectedConvMessage: "@" + autoReplyBotUsername + " help me",
		},
		{
			name:        "threads ignores webhook posts",
			settingMode: autoreply.ModeThreads,
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				post := env.rootPost(autoReplyUserID, "webhook announcement")
				post.AddProp(conversations.FromWebhookProp, "true")
				return post
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupAutoReplyTestEnv(t,
				[]llm.BotConfig{autoReplyBotConfig(), autoReplySecondBotConfig()},
				dmMakeTextStream("canned reply"),
			)
			if tc.settingMode != "" {
				env.settings.set(autoreply.Setting{
					ChannelID: autoReplyChannelID,
					BotID:     autoReplyBotUserID,
					Mode:      tc.settingMode,
				})
			}

			post := tc.buildPost(env)
			env.conversations.MessageHasBeenPosted(nil, post)

			if tc.expectFired {
				require.Len(t, env.mmClient.createdPosts, 1, "expected exactly one bot placeholder post")
				placeholder := env.mmClient.createdPosts[0]
				require.Equal(t, autoReplyChannelID, placeholder.ChannelId)
				require.Equal(t, tc.expectedRootID, placeholder.RootId)
				require.Equal(t, tc.expectedBotUserID, placeholder.UserId)

				blocks := singleConversationUserBlocks(t, env)
				require.Equal(t, tc.expectedConvMessage, blocks[0].Text)
			} else {
				require.Empty(t, env.mmClient.createdPosts, "expected no bot reply")
				require.Empty(t, allConversations(env.convStore), "expected no conversation")
			}

			if tc.expectReminder {
				require.Len(t, env.mmClient.ephemeralPosts, 1, "expected the mention reminder")
			} else {
				require.Empty(t, env.mmClient.ephemeralPosts, "expected no mention reminder")
			}
		})
	}
}

// TestAutoReplyTriggerRechecks covers the trigger-time re-checks: every stale
// or unlicensed condition must decline quietly — no reply, no conversation, and
// nothing logged at error level (MessageHasBeenPosted swallows ErrNoResponse at
// debug level). Declining still falls through to the mention reminder, which
// applies its own guards; @mentioning an agent works even unlicensed.
func TestAutoReplyTriggerRechecks(t *testing.T) {
	cases := []struct {
		name           string
		botConfig      llm.BotConfig
		setting        autoreply.Setting
		license        *model.License // non-nil overrides the default Enterprise license
		buildPost      func(env *autoReplyTestEnv) *model.Post
		expectReminder bool
	}{
		{
			name:      "unlicensed server does not fire",
			botConfig: autoReplyBotConfig(),
			setting:   autoreply.Setting{ChannelID: autoReplyChannelID, BotID: autoReplyBotUserID, Mode: autoreply.ModeThreads},
			license:   &model.License{},
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "hello there")
			},
		},
		{
			name:      "unlicensed server still reminds a thread reply after an agent post",
			botConfig: autoReplyBotConfig(),
			setting:   autoreply.Setting{ChannelID: autoReplyChannelID, BotID: autoReplyBotUserID, Mode: autoreply.ModeThreads},
			license:   &model.License{},
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyUserID, "thanks!", true)
			},
			expectReminder: true,
		},
		{
			name:      "setting referencing a deleted bot is a no-op",
			botConfig: autoReplyBotConfig(),
			setting:   autoreply.Setting{ChannelID: autoReplyChannelID, BotID: "ghost-bot-id", Mode: autoreply.ModeThreads},
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "hello there")
			},
		},
		{
			name: "bot restricted from all channels is a no-op",
			botConfig: llm.BotConfig{
				ID:                 autoReplyBotUserID,
				Name:               autoReplyBotUsername,
				DisplayName:        "AR Bot",
				ChannelAccessLevel: llm.ChannelAccessLevelNone,
				UserAccessLevel:    llm.UserAccessLevelAll,
			},
			setting: autoreply.Setting{ChannelID: autoReplyChannelID, BotID: autoReplyBotUserID, Mode: autoreply.ModeThreads},
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "hello there")
			},
		},
		{
			name: "bot allowed only in a different channel is a no-op",
			botConfig: llm.BotConfig{
				ID:                 autoReplyBotUserID,
				Name:               autoReplyBotUsername,
				DisplayName:        "AR Bot",
				ChannelAccessLevel: llm.ChannelAccessLevelAllow,
				ChannelIDs:         []string{"some-other-channel"},
				UserAccessLevel:    llm.UserAccessLevelAll,
			},
			setting: autoreply.Setting{ChannelID: autoReplyChannelID, BotID: autoReplyBotUserID, Mode: autoreply.ModeThreads},
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "hello there")
			},
		},
		{
			name: "posting user blocked from the bot is a no-op",
			botConfig: llm.BotConfig{
				ID:                 autoReplyBotUserID,
				Name:               autoReplyBotUsername,
				DisplayName:        "AR Bot",
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelBlock,
				UserIDs:            []string{autoReplyUserID},
			},
			setting: autoreply.Setting{ChannelID: autoReplyChannelID, BotID: autoReplyBotUserID, Mode: autoreply.ModeThreads},
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, "hello there")
			},
		},
		{
			name:      "setting for a different channel does not fire and keeps the reminder",
			botConfig: autoReplyBotConfig(),
			setting:   autoreply.Setting{ChannelID: "some-other-channel", BotID: autoReplyBotUserID, Mode: autoreply.ModeThreads},
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyUserID, "thanks!", true)
			},
			expectReminder: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupAutoReplyTestEnv(t, []llm.BotConfig{tc.botConfig}, dmMakeTextStream("canned reply"))
			env.settings.set(tc.setting)
			if tc.license != nil {
				env.overrideLicense(tc.license)
			}

			post := tc.buildPost(env)
			env.conversations.MessageHasBeenPosted(nil, post)

			require.Empty(t, env.mmClient.createdPosts, "quiet no-op must not create posts")
			require.Empty(t, allConversations(env.convStore), "quiet no-op must not create conversations")
			require.Empty(t, env.mmClient.loggedErrors(), "quiet no-op must not log at error level")

			if tc.expectReminder {
				require.Len(t, env.mmClient.ephemeralPosts, 1, "expected the mention reminder")
			} else {
				require.Empty(t, env.mmClient.ephemeralPosts, "expected no ephemeral post")
			}
		})
	}
}

// TestAutoReplySynthesizedMention pins the exact shape of the cloned post the
// auto-reply hands to the mention path: trimmed original text behind the
// synthesized mention, and attachments preserved.
func TestAutoReplySynthesizedMention(t *testing.T) {
	cases := []struct {
		name         string
		buildPost    func(env *autoReplyTestEnv) *model.Post
		fileInfos    map[string]*model.FileInfo
		expectedText string
		expectedFile string // FileID expected as an attachment block; "" for none
	}{
		{
			name: "thread reply text is trimmed behind the mention",
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyUserID, "  what about this?  ", false)
			},
			expectedText: "@" + autoReplyBotUsername + " what about this?",
		},
		{
			name: "file-only post synthesizes a bare mention and keeps the attachment",
			buildPost: func(env *autoReplyTestEnv) *model.Post {
				post := env.rootPost(autoReplyUserID, "")
				post.FileIds = model.StringArray{"arfile-1"}
				return post
			},
			fileInfos: map[string]*model.FileInfo{
				"arfile-1": {Id: "arfile-1", Name: "notes.txt", MimeType: "text/plain"},
			},
			expectedText: "@" + autoReplyBotUsername,
			expectedFile: "arfile-1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupAutoReplyTestEnv(t, []llm.BotConfig{autoReplyBotConfig()}, dmMakeTextStream("canned reply"))
			env.mmClient.fileInfos = tc.fileInfos
			env.settings.set(autoreply.Setting{
				ChannelID: autoReplyChannelID,
				BotID:     autoReplyBotUserID,
				Mode:      autoreply.ModeThreads,
			})

			post := tc.buildPost(env)
			env.conversations.MessageHasBeenPosted(nil, post)

			require.Len(t, env.mmClient.createdPosts, 1, "expected the auto-reply to fire")
			blocks := singleConversationUserBlocks(t, env)
			require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
			require.Equal(t, tc.expectedText, blocks[0].Text)

			if tc.expectedFile != "" {
				require.Len(t, blocks, 2, "expected the attachment block to be preserved")
				require.Equal(t, conversation.BlockTypeFile, blocks[1].Type)
				require.Equal(t, tc.expectedFile, blocks[1].FileID)
			} else {
				require.Len(t, blocks, 1)
			}
		})
	}
}

// TestAutoReplyNilServiceIsNoop guards the nil-safe default: every other test
// environment in the repo constructs Conversations without the auto-reply
// dependency, and behavior must be exactly as before this feature existed.
func TestAutoReplyNilServiceIsNoop(t *testing.T) {
	env := setupAutoReplyTestEnv(t, []llm.BotConfig{autoReplyBotConfig()})
	env.conversations.SetAutoReplySettings(nil)

	post := env.threadReply(autoReplyUserID, "thanks!", true)
	env.conversations.MessageHasBeenPosted(nil, post)

	require.Empty(t, env.mmClient.createdPosts, "no auto-reply must fire without the settings lookup")
	require.Len(t, env.mmClient.ephemeralPosts, 1, "the mention reminder must behave exactly as today")
	require.Empty(t, env.mmClient.loggedErrors())
}
