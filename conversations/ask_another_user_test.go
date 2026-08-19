// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// askAnotherUserBuilder builds a context builder whose built-in tool provider
// registers the AskAnotherUser tool, mirroring production MMToolProvider.
func askAnotherUserBuilder(t *testing.T) *llmcontext.Builder {
	t.Helper()

	mockAPI := &plugintest.API{}
	mockLicenseState(mockAPI, true)
	mockAPI.On("GetTeam", "team-id").Return(&model.Team{Id: "team-id", Name: "team"}, nil).Maybe()

	return llmcontext.NewLLMContextBuilder(
		pluginapi.NewClient(mockAPI, nil),
		&toolLicenseBuiltinProvider{tools: []llm.Tool{mmtools.NewAskAnotherUserTool()}},
		&channelFollowUpTestMCPToolProvider{tools: []llm.Tool{loadedStateTool()}},
		&channelFollowUpTestConfig{},
	)
}

func newAskAnotherUserBotsService(t *testing.T, bot *bots.Bot) *bots.MMBots {
	t.Helper()

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{bot})
	return botsService
}

func TestDispatchAskAnotherUserValidation(t *testing.T) {
	validArgs := `{"username":"bob","question":"Which environment?","options":[{"label":"Prod"},{"label":"Staging"}],"context":"Deciding where to deploy"}`
	rootPostID := "root-post-id"

	cases := []struct {
		name               string
		rawArgs            string
		anchorPostID       string
		rootPostID         *string
		restrictedAccess   bool
		target             *model.User
		targetLookupErr    bool
		requester          *model.User
		requesterLookupErr bool
		dmErr              error
		wantErrContains    string
		wantDM             bool
		wantRequesterProp  string
		wantSourceProp     string
		wantLookupUsername string
	}{
		{
			name:              "happy path sends card with all props",
			rawArgs:           validArgs,
			anchorPostID:      "anchor-post-id",
			target:            &model.User{Id: "bob-id", Username: "bob"},
			requester:         &model.User{Id: "user-id", Username: "user"},
			wantDM:            true,
			wantRequesterProp: "user-id",
			wantSourceProp:    "anchor-post-id",
		},
		{
			name:              "empty anchor falls back to conversation root post",
			rawArgs:           validArgs,
			anchorPostID:      "",
			rootPostID:        &rootPostID,
			target:            &model.User{Id: "bob-id", Username: "bob"},
			requester:         &model.User{Id: "user-id", Username: "user"},
			wantDM:            true,
			wantRequesterProp: "user-id",
			wantSourceProp:    "root-post-id",
		},
		{
			name:            "target user not found",
			rawArgs:         validArgs,
			targetLookupErr: true,
			wantErrContains: "not found",
		},
		{
			name:            "target is a bot",
			rawArgs:         validArgs,
			target:          &model.User{Id: "bob-id", Username: "bob", IsBot: true},
			wantErrContains: "is a bot",
		},
		{
			name:            "target deactivated",
			rawArgs:         validArgs,
			target:          &model.User{Id: "bob-id", Username: "bob", DeleteAt: 1},
			wantErrContains: "is deactivated",
		},
		{
			name:            "target is the requesting user",
			rawArgs:         `{"username":"user","question":"Which environment?"}`,
			target:          &model.User{Id: "user-id", Username: "user"},
			wantErrContains: "use the AskUserQuestion tool",
		},
		{
			name:             "target lacks access to the agent",
			rawArgs:          validArgs,
			restrictedAccess: true,
			target:           &model.User{Id: "bob-id", Username: "bob"},
			wantErrContains:  "does not have access",
		},
		{
			name:            "DM creation failure",
			rawArgs:         validArgs,
			target:          &model.User{Id: "bob-id", Username: "bob"},
			requester:       &model.User{Id: "user-id", Username: "user"},
			dmErr:           errors.New("boom"),
			wantErrContains: "failed to open a direct message",
			wantDM:          true,
		},
		{
			name:            "invalid arguments",
			rawArgs:         `{"username":"bob","question":" "}`,
			wantErrContains: "question must not be empty",
		},
		{
			name:              "bot requester gets empty attribution",
			rawArgs:           validArgs,
			anchorPostID:      "anchor-post-id",
			target:            &model.User{Id: "bob-id", Username: "bob"},
			requester:         &model.User{Id: "user-id", Username: "flowbot", IsBot: true},
			wantDM:            true,
			wantRequesterProp: "",
			wantSourceProp:    "anchor-post-id",
		},
		{
			// A failed requester lookup must not send a half-attributed
			// card: both the requester prop and the fallback attribution
			// stay empty, like an autonomous dispatch.
			name:               "requester lookup failure sends an unattributed card",
			rawArgs:            validArgs,
			anchorPostID:       "anchor-post-id",
			target:             &model.User{Id: "bob-id", Username: "bob"},
			requesterLookupErr: true,
			wantDM:             true,
			wantRequesterProp:  "",
			wantSourceProp:     "anchor-post-id",
		},
		{
			name:               "whitespace-and-@ username is canonicalized for the lookup",
			rawArgs:            `{"username":"  @bob  ","question":"Which environment?","options":[{"label":"Prod"},{"label":"Staging"}],"context":"Deciding where to deploy"}`,
			anchorPostID:       "anchor-post-id",
			target:             &model.User{Id: "bob-id", Username: "bob"},
			requester:          &model.User{Id: "user-id", Username: "user"},
			wantDM:             true,
			wantRequesterProp:  "user-id",
			wantSourceProp:     "anchor-post-id",
			wantLookupUsername: "bob",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			botConfig := llm.BotConfig{
				ID:                 "bot-id",
				Name:               "matty",
				DisplayName:        "Matty",
				UserAccessLevel:    llm.UserAccessLevelAll,
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
			}
			if tc.restrictedAccess {
				// Allow-list with no users: every target is denied without
				// needing team lookups.
				botConfig.UserAccessLevel = llm.UserAccessLevelAllow
				botConfig.UserIDs = nil
			}
			bot := bots.NewBot(
				botConfig,
				llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
				&model.Bot{UserId: "bot-id", Username: "matty", DisplayName: "Matty"},
				nil,
			)

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			// Destination resolution degrades to the generic channel claim
			// when the anchor post cannot be read; the destination matrix
			// has its own table (TestDispatchAskAnotherUserDestinationProps).
			mmClient.On("GetPost", mock.Anything).Maybe().Return(nil, errors.New("not found"))
			mmClient.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			var lookedUpUsername string
			if tc.targetLookupErr {
				mmClient.On("GetUserByUsername", mock.Anything).Return(nil, errors.New("store miss")).Once()
			} else if tc.target != nil {
				mmClient.On("GetUserByUsername", mock.Anything).
					Run(func(args mock.Arguments) { lookedUpUsername = args.String(0) }).
					Return(tc.target, nil).Once()
			}
			if tc.requesterLookupErr {
				mmClient.On("GetUser", "user-id").Return(nil, errors.New("requester lookup down")).Once()
			} else if tc.requester != nil {
				mmClient.On("GetUser", "user-id").Return(tc.requester, nil).Once()
			}
			var sentPost *model.Post
			if tc.wantDM {
				mmClient.On("DM", "bot-id", tc.target.Id, mock.AnythingOfType("*model.Post")).
					Run(func(args mock.Arguments) {
						sentPost = args.Get(2).(*model.Post)
					}).Return(tc.dmErr).Once()
			}

			conv := &store.Conversation{ID: "conv-id", UserID: "user-id", BotID: "bot-id", RootPostID: tc.rootPostID}
			c := &Conversations{
				mmClient:       mmClient,
				bots:           newAskAnotherUserBotsService(t, bot),
				configProvider: &channelFollowUpTestConfig{enableAskAnotherUser: true},
			}

			err := c.dispatchAskAnotherUser(context.Background(), bot, conv, tc.anchorPostID, "ask-1", json.RawMessage(tc.rawArgs))

			if tc.wantErrContains != "" {
				require.ErrorContains(t, err, tc.wantErrContains)
				if !tc.wantDM {
					assert.Nil(t, sentPost, "no card may be sent on validation failure")
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, sentPost)

			assert.Equal(t, AskUserPostType, sentPost.Type)
			assert.Contains(t, sentPost.Message, "Which environment?")
			assert.Contains(t, sentPost.Message, "Interactive answer card")
			// F-001: a human requester is named in the server-authored
			// fallback so plaintext/mobile clients see attribution too, and
			// the attribution precedes the LLM-authored question so injected
			// question text cannot spoof it. Autonomous (bot) requesters
			// stay unattributed.
			if tc.wantRequesterProp != "" {
				attribution := "Asked on behalf of @" + tc.requester.Username
				require.Contains(t, sentPost.Message, attribution)
				assert.Less(t,
					strings.Index(sentPost.Message, attribution),
					strings.Index(sentPost.Message, "Which environment?"),
					"attribution must come before the question")
			} else {
				assert.NotContains(t, sentPost.Message, "Asked on behalf of")
			}
			assert.Equal(t, AskUserStatusPending, sentPost.GetProp(AskUserStatusProp))
			assert.Equal(t, "Which environment?", sentPost.GetProp(AskUserQuestionProp))
			assert.Equal(t, "Deciding where to deploy", sentPost.GetProp(AskUserContextProp))
			assert.Equal(t, []any{
				map[string]any{"label": "Prod", "description": ""},
				map[string]any{"label": "Staging", "description": ""},
			}, sentPost.GetProp(AskUserOptionsProp))
			assert.Equal(t, false, sentPost.GetProp(AskUserMultiSelectProp))
			assert.Equal(t, true, sentPost.GetProp(AskUserAllowFreeFormProp))
			assert.Equal(t, tc.wantRequesterProp, sentPost.GetProp(AskUserRequesterIDProp))
			assert.Equal(t, "bob-id", sentPost.GetProp(AskUserTargetIDProp))
			if tc.wantLookupUsername != "" {
				assert.Equal(t, tc.wantLookupUsername, lookedUpUsername,
					"the user lookup must use the canonical username")
			}
			assert.Equal(t, "conv-id", sentPost.GetProp(AskUserConversationIDProp))
			assert.Equal(t, "ask-1", sentPost.GetProp(AskUserToolUseIDProp))
			assert.Equal(t, tc.wantSourceProp, sentPost.GetProp(AskUserSourcePostIDProp))
		})
	}
}

// TestDispatchAskAnotherUserDisabled pins the F1 dispatch backstop (V2-C1):
// with the master toggle off — or no config provider at all — a pending
// AskAnotherUser block resolves to an error result with zero side effects:
// no target lookup, no DM, no KV card pointer. The strict mock client is the
// no-side-effects assertion.
func TestDispatchAskAnotherUserDisabled(t *testing.T) {
	cases := []struct {
		name           string
		configProvider ConfigProvider
	}{
		{
			name:           "toggle off resolves to an error result",
			configProvider: &channelFollowUpTestConfig{enableAskAnotherUser: false},
		},
		{
			name:           "nil config provider fails closed",
			configProvider: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bot := loadedStateBot(&loadedStateLLM{})
			mmClient := mocks.NewMockClient(t)

			c := &Conversations{
				mmClient:       mmClient,
				bots:           newAskAnotherUserBotsService(t, bot),
				configProvider: tc.configProvider,
			}
			conv := &store.Conversation{ID: "conv-id", UserID: "user-id", BotID: "bot-id"}

			err := c.dispatchAskAnotherUser(context.Background(), bot, conv, "anchor-post-id", "ask-1",
				json.RawMessage(`{"username":"bob","question":"Which environment?"}`))

			require.ErrorContains(t, err, "disabled by the administrator")
		})
	}
}

// TestDispatchWritesCardPointer pins the V2-C4 reverse pointer: a successful
// dispatch stores askcard_<toolUseID> → card post id so the cancel path can
// find the target's card, and a KV failure only degrades the future card
// patch — the dispatch itself still succeeds.
func TestDispatchWritesCardPointer(t *testing.T) {
	cases := []struct {
		name  string
		kvErr error
	}{
		{name: "successful dispatch stores the card pointer"},
		{name: "pointer write failure does not fail the dispatch", kvErr: errors.New("kv down")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bot := loadedStateBot(&loadedStateLLM{})

			mmClient := mocks.NewMockClient(t)
			for i := 1; i <= 7; i++ {
				args := make([]interface{}, i)
				for j := range args {
					args[j] = mock.Anything
				}
				mmClient.On("LogWarn", args...).Maybe().Return()
			}
			mmClient.On("GetUserByUsername", "bob").Return(&model.User{Id: "bob-id", Username: "bob"}, nil).Once()
			mmClient.On("GetUser", "user-id").Return(&model.User{Id: "user-id", Username: "user"}, nil).Once()
			mmClient.On("GetPost", mock.Anything).Maybe().Return(nil, errors.New("not found"))
			mmClient.On("DM", "bot-id", "bob-id", mock.AnythingOfType("*model.Post")).
				Run(func(args mock.Arguments) {
					// Production DM fills the created post's ID in place.
					args.Get(2).(*model.Post).Id = "card-post-id"
				}).Return(nil).Once()
			mmClient.On("KVSetWithExpiry", askCardKVPrefix+"ask-1", "card-post-id", askCardPointerTTL).
				Return(tc.kvErr).Once()

			c := &Conversations{
				mmClient:       mmClient,
				bots:           newAskAnotherUserBotsService(t, bot),
				configProvider: &channelFollowUpTestConfig{enableAskAnotherUser: true},
			}
			conv := &store.Conversation{ID: "conv-id", UserID: "user-id", BotID: "bot-id"}

			err := c.dispatchAskAnotherUser(context.Background(), bot, conv, "anchor-post-id", "ask-1",
				json.RawMessage(`{"username":"bob","question":"Which environment?"}`))

			require.NoError(t, err, "a lost card pointer must never fail the dispatch")
		})
	}
}

// TestDispatchAskAnotherUserDestinationProps pins the V2-C2
// destination-resolution matrix: the card's destination props and the
// fallback's disclosure line reflect where the answer may end up, and every
// lookup failure degrades toward the BROADER audience claim (generic
// channel), never a narrower one.
func TestDispatchAskAnotherUserDestinationProps(t *testing.T) {
	cases := []struct {
		name           string
		convChannelID  string // conversation-level channel; "" means nil (DM/thread conv)
		anchorChannel  string // channel resolved via the anchor post when convChannelID is ""
		channel        *model.Channel
		channelErr     bool
		statsCount     int64
		statsErr       bool
		wantType       string
		wantName       string
		wantCount      int64
		wantPolicy     bool
		wantMsgContain string
	}{
		{
			name:           "DM conversation discloses the dm destination",
			convChannelID:  "dm-chan",
			channel:        &model.Channel{Id: "dm-chan", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"},
			wantType:       AskUserDestinationTypeDM,
			wantMsgContain: "Your answer will be shared with @user.",
		},
		{
			name:           "public channel carries name and member count",
			convChannelID:  "town-square",
			channel:        &model.Channel{Id: "town-square", Type: model.ChannelTypeOpen, DisplayName: "Town Square"},
			statsCount:     42,
			wantType:       AskUserDestinationTypeChannel,
			wantName:       "Town Square",
			wantCount:      42,
			wantMsgContain: "Your answer may be shared with the 42 members of ~Town Square.",
		},
		{
			name:           "private channel carries name and member count",
			convChannelID:  "secret-chan",
			channel:        &model.Channel{Id: "secret-chan", Type: model.ChannelTypePrivate, DisplayName: "Secret Plans"},
			statsCount:     5,
			wantType:       AskUserDestinationTypeChannel,
			wantName:       "Secret Plans",
			wantCount:      5,
			wantMsgContain: "Your answer may be shared with the 5 members of ~Secret Plans.",
		},
		{
			name:           "group message gets its own gm type",
			convChannelID:  "gm-chan",
			channel:        &model.Channel{Id: "gm-chan", Type: model.ChannelTypeGroup, DisplayName: "alice, bob, carol"},
			statsCount:     3,
			wantType:       AskUserDestinationTypeGM,
			wantName:       "alice, bob, carol",
			wantCount:      3,
			wantMsgContain: "Your answer may be shared with the 3 members of a group message.",
		},
		{
			name:           "member-count failure degrades to an unknown count",
			convChannelID:  "town-square",
			channel:        &model.Channel{Id: "town-square", Type: model.ChannelTypeOpen, DisplayName: "Town Square"},
			statsErr:       true,
			wantType:       AskUserDestinationTypeChannel,
			wantName:       "Town Square",
			wantCount:      0,
			wantMsgContain: "Your answer may be shared with the members of ~Town Square.",
		},
		{
			name:           "channel lookup failure degrades to the generic channel claim",
			convChannelID:  "town-square",
			channelErr:     true,
			wantType:       AskUserDestinationTypeChannel,
			wantMsgContain: "Your answer may be shared in the channel where the agent was asked.",
		},
		{
			name:           "nil conversation channel resolves via the anchor post",
			anchorChannel:  "chan-x",
			channel:        &model.Channel{Id: "chan-x", Type: model.ChannelTypeOpen, DisplayName: "Anchor Channel"},
			statsCount:     7,
			wantType:       AskUserDestinationTypeChannel,
			wantName:       "Anchor Channel",
			wantCount:      7,
			wantMsgContain: "Your answer may be shared with the 7 members of ~Anchor Channel.",
		},
		{
			name:           "no channel anywhere claims the broadest audience",
			wantType:       AskUserDestinationTypeChannel,
			wantMsgContain: "Your answer may be shared in the channel where the agent was asked.",
		},
		{
			name:          "policy-enforced channel sets the policy prop and line",
			convChannelID: "abac-chan",
			channel: &model.Channel{
				Id: "abac-chan", Type: model.ChannelTypePrivate,
				DisplayName: "Compliance", PolicyEnforced: true,
			},
			statsCount:     9,
			wantType:       AskUserDestinationTypeChannel,
			wantName:       "Compliance",
			wantCount:      9,
			wantPolicy:     true,
			wantMsgContain: "Access to ~Compliance is restricted by an attribute-based access policy.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bot := loadedStateBot(&loadedStateLLM{})

			mmClient := mocks.NewMockClient(t)
			mmClient.On("GetUserByUsername", "bob").Return(&model.User{Id: "bob-id", Username: "bob"}, nil).Once()
			mmClient.On("GetUser", "user-id").Return(&model.User{Id: "user-id", Username: "user"}, nil).Once()
			mmClient.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)

			channelID := tc.convChannelID
			if tc.anchorChannel != "" {
				channelID = tc.anchorChannel
				mmClient.On("GetPost", "anchor-post-id").
					Return(&model.Post{Id: "anchor-post-id", ChannelId: tc.anchorChannel}, nil).Once()
			} else if tc.convChannelID == "" {
				mmClient.On("GetPost", "anchor-post-id").Return(nil, errors.New("not found")).Once()
			}
			switch {
			case tc.channelErr:
				mmClient.On("GetChannel", channelID).Return(nil, errors.New("channel gone")).Once()
			case tc.channel != nil:
				mmClient.On("GetChannel", channelID).Return(tc.channel, nil).Once()
			}
			// The DM destination and failed lookups must never trigger a
			// stats read; the strict mock enforces it by omission.
			if tc.channel != nil && tc.channel.Type != model.ChannelTypeDirect {
				if tc.statsErr {
					mmClient.On("GetChannelStats", channelID).Return(nil, errors.New("stats down")).Once()
				} else {
					mmClient.On("GetChannelStats", channelID).
						Return(&model.ChannelStats{ChannelId: channelID, MemberCount: tc.statsCount}, nil).Once()
				}
			}
			var sentPost *model.Post
			mmClient.On("DM", "bot-id", "bob-id", mock.AnythingOfType("*model.Post")).
				Run(func(args mock.Arguments) { sentPost = args.Get(2).(*model.Post) }).
				Return(nil).Once()

			conv := &store.Conversation{ID: "conv-id", UserID: "user-id", BotID: "bot-id"}
			if tc.convChannelID != "" {
				conv.ChannelID = &tc.convChannelID
			}
			c := &Conversations{
				mmClient:       mmClient,
				bots:           newAskAnotherUserBotsService(t, bot),
				configProvider: &channelFollowUpTestConfig{enableAskAnotherUser: true},
			}

			err := c.dispatchAskAnotherUser(context.Background(), bot, conv, "anchor-post-id", "ask-1",
				json.RawMessage(`{"username":"bob","question":"Which environment?"}`))
			require.NoError(t, err)
			require.NotNil(t, sentPost)

			assert.Equal(t, tc.wantType, sentPost.GetProp(AskUserDestinationTypeProp))
			assert.Equal(t, tc.wantName, sentPost.GetProp(AskUserDestinationChannelDisplayNameProp))
			assert.Equal(t, tc.wantCount, sentPost.GetProp(AskUserDestinationMemberCountProp))
			assert.Equal(t, tc.wantPolicy, sentPost.GetProp(AskUserDestinationPolicyEnforcedProp))
			assert.Contains(t, sentPost.Message, tc.wantMsgContain,
				"the plaintext fallback must carry the same destination disclosure as the card")
			if !tc.wantPolicy {
				assert.NotContains(t, sentPost.Message, "attribute-based access policy",
					"unreachable access data must be omitted, never rendered as unrestricted")
			}
		})
	}
}

// TestDispatchAskAnotherUserRequesterKind pins the V2-C2 requester
// attribution props: a human requester is fully identified, an autonomous
// bot invoker and a failed lookup stay consistently unattributed (kind bot /
// unknown, empty identity props), and the agent display name prop is always
// set — falling back to the bot username when the display name is empty.
func TestDispatchAskAnotherUserRequesterKind(t *testing.T) {
	cases := []struct {
		name            string
		requester       *model.User
		requesterErr    bool
		botDisplayName  string // defaults to "Matty"
		wantKind        string
		wantRequesterID string
		wantUsername    string
		wantDisplay     string
		wantPosition    string
		wantAgent       string
		wantMsgContain  string
	}{
		{
			name: "human requester is fully identified",
			requester: &model.User{
				Id: "user-id", Username: "user",
				FirstName: "Ursula", LastName: "Example", Position: "SRE",
			},
			wantKind:        AskUserRequesterKindUser,
			wantRequesterID: "user-id",
			wantUsername:    "user",
			wantDisplay:     "Ursula Example",
			wantPosition:    "SRE",
			wantAgent:       "Matty",
			wantMsgContain:  "Asked on behalf of @user:",
		},
		{
			name:            "display name equal to the username is dropped",
			requester:       &model.User{Id: "user-id", Username: "user"},
			wantKind:        AskUserRequesterKindUser,
			wantRequesterID: "user-id",
			wantUsername:    "user",
			wantDisplay:     "",
			wantAgent:       "Matty",
			wantMsgContain:  "Asked on behalf of @user:",
		},
		{
			name:           "bot requester is kind bot with no identity props",
			requester:      &model.User{Id: "flow-id", Username: "flowbot", IsBot: true},
			wantKind:       AskUserRequesterKindBot,
			wantAgent:      "Matty",
			wantMsgContain: "Asked by the Matty agent running unattended (no human requester):",
		},
		{
			name:           "requester lookup failure is kind unknown",
			requesterErr:   true,
			wantKind:       AskUserRequesterKindUnknown,
			wantAgent:      "Matty",
			wantMsgContain: "Asked via the Matty agent (requester identity unavailable):",
		},
		{
			name:           "empty agent display name falls back to the bot username",
			requesterErr:   true,
			botDisplayName: "-", // sentinel for "explicitly empty"
			wantKind:       AskUserRequesterKindUnknown,
			wantAgent:      "matty",
			wantMsgContain: "Asked via the matty agent (requester identity unavailable):",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			displayName := "Matty"
			if tc.botDisplayName == "-" {
				displayName = ""
			}
			bot := bots.NewBot(
				llm.BotConfig{
					ID: "bot-id", Name: "matty", DisplayName: displayName,
					UserAccessLevel:    llm.UserAccessLevelAll,
					ChannelAccessLevel: llm.ChannelAccessLevelAll,
				},
				llm.ServiceConfig{DefaultModel: "test-model", Type: llm.ServiceTypeOpenAI},
				&model.Bot{UserId: "bot-id", Username: "matty", DisplayName: displayName},
				nil,
			)

			mmClient := mocks.NewMockClient(t)
			mmClient.On("GetUserByUsername", "bob").Return(&model.User{Id: "bob-id", Username: "bob"}, nil).Once()
			if tc.requesterErr {
				mmClient.On("GetUser", "user-id").Return(nil, errors.New("lookup down")).Once()
			} else {
				mmClient.On("GetUser", "user-id").Return(tc.requester, nil).Once()
			}
			mmClient.On("GetPost", mock.Anything).Maybe().Return(nil, errors.New("not found"))
			mmClient.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			var sentPost *model.Post
			mmClient.On("DM", "bot-id", "bob-id", mock.AnythingOfType("*model.Post")).
				Run(func(args mock.Arguments) { sentPost = args.Get(2).(*model.Post) }).
				Return(nil).Once()

			conv := &store.Conversation{ID: "conv-id", UserID: "user-id", BotID: "bot-id"}
			c := &Conversations{
				mmClient:       mmClient,
				bots:           newAskAnotherUserBotsService(t, bot),
				configProvider: &channelFollowUpTestConfig{enableAskAnotherUser: true},
			}

			err := c.dispatchAskAnotherUser(context.Background(), bot, conv, "anchor-post-id", "ask-1",
				json.RawMessage(`{"username":"bob","question":"Which environment?"}`))
			require.NoError(t, err)
			require.NotNil(t, sentPost)

			assert.Equal(t, tc.wantKind, sentPost.GetProp(AskUserRequesterKindProp))
			assert.Equal(t, tc.wantRequesterID, sentPost.GetProp(AskUserRequesterIDProp),
				"only kind user may carry the requester_id prop")
			assert.Equal(t, tc.wantUsername, sentPost.GetProp(AskUserRequesterUsernameProp))
			assert.Equal(t, tc.wantDisplay, sentPost.GetProp(AskUserRequesterDisplayNameProp))
			assert.Equal(t, tc.wantPosition, sentPost.GetProp(AskUserRequesterPositionProp))
			assert.Equal(t, tc.wantAgent, sentPost.GetProp(AskUserAgentDisplayNameProp))
			assert.Contains(t, sentPost.Message, tc.wantMsgContain,
				"the fallback attribution must match the props")
		})
	}
}

func TestNewDeferredDispatcherRejectsUnknownTool(t *testing.T) {
	c := &Conversations{}
	dispatcher := c.newDeferredDispatcherForConversation(nil, &store.Conversation{}, "")

	err := dispatcher(context.Background(), llm.ToolCall{ID: "x", Name: "SomeOtherTool"})

	require.ErrorContains(t, err, "no deferred dispatch implemented for tool SomeOtherTool")
}

// TestHandleToolCallDeferredAccept covers the deferred-dispatch flow in
// HandleToolCall: accepting an AskAnotherUser call sends the card and parks
// the block in waiting (no tool result, no follow-up), dispatch failures
// convert to error results, unaccepted deferred calls reject like any other
// tool, a policy-marked block dispatches on resume without a click, and a
// mixed batch executes the normal tool but gates the follow-up on the
// waiting block.
func TestHandleToolCallDeferredAccept(t *testing.T) {
	askInput := json.RawMessage(`{"username":"bob","question":"Which environment?","options":[{"label":"Prod"},{"label":"Staging"}]}`)
	dmChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

	cases := []struct {
		name             string
		acceptedIDs      []string
		wouldAutoExecute bool
		policyChecker    mcp.ToolPolicyChecker
		includeNormal    bool
		inChannel        bool
		dmErr            error
		wantDMCalls      int
		wantBlockStatus  string
		wantResultTurn   bool
		wantResultText   string
		wantResultErr    bool
		wantFollowUp     bool
		wantPublish      bool
	}{
		{
			name:            "accept dispatches card and parks waiting",
			acceptedIDs:     []string{"ask-1"},
			wantDMCalls:     1,
			wantBlockStatus: conversation.StatusWaiting,
			wantPublish:     true,
		},
		{
			name:            "dispatch failure records error result and streams follow-up",
			acceptedIDs:     []string{"ask-1"},
			dmErr:           errors.New("boom"),
			wantDMCalls:     1,
			wantBlockStatus: conversation.StatusError,
			wantResultTurn:  true,
			wantResultText:  "failed to open a direct message",
			wantResultErr:   true,
			wantFollowUp:    true,
		},
		{
			// Rejected-only batches never stream a follow-up: nothing
			// executed, matching the behavior for normal tools.
			name:            "unaccepted deferred call is rejected",
			acceptedIDs:     []string{},
			wantBlockStatus: conversation.StatusRejected,
			wantResultTurn:  true,
			wantResultText:  "Tool call rejected by user",
			wantResultErr:   true,
		},
		{
			name:             "auto-exec resume dispatches without an accept click",
			acceptedIDs:      []string{},
			wouldAutoExecute: true,
			policyChecker: mapPolicyChecker{
				"": {"AskAnotherUser": {policy: mcp.ToolPolicyAutoRunInDM, enabled: true}},
			},
			wantDMCalls:     1,
			wantBlockStatus: conversation.StatusWaiting,
			wantPublish:     true,
		},
		{
			name:            "mixed batch executes normal tool but gates follow-up on waiting",
			acceptedIDs:     []string{"tool-use-1", "ask-1"},
			includeNormal:   true,
			wantDMCalls:     1,
			wantBlockStatus: conversation.StatusWaiting,
			wantResultTurn:  true,
			wantResultText:  "restored-result",
			wantPublish:     true,
		},
		{
			// Channel-anchored mixed batch: the normal tool's result stays
			// undecided (share stage pending in HandleToolResult) and the
			// waiting question gates the follow-up exactly as in DMs.
			name:            "channel mixed batch stages the share decision and gates follow-up on waiting",
			acceptedIDs:     []string{"tool-use-1", "ask-1"},
			includeNormal:   true,
			inChannel:       true,
			wantDMCalls:     1,
			wantBlockStatus: conversation.StatusWaiting,
			wantResultTurn:  true,
			wantResultText:  "restored-result",
			wantPublish:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			nextSeq := 1
			if tc.includeNormal {
				seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)
			}

			var blocks []conversation.ContentBlock
			if tc.includeNormal {
				blocks = append(blocks, conversation.ContentBlock{
					Type:   conversation.BlockTypeToolUse,
					ID:     "tool-use-1",
					Name:   "jira__get_issue",
					Input:  json.RawMessage(`{}`),
					Status: conversation.StatusPending,
					Shared: conversation.BoolPtr(false),
				})
			}
			blocks = append(blocks, conversation.ContentBlock{
				Type:             conversation.BlockTypeToolUse,
				ID:               "ask-1",
				Name:             "AskAnotherUser",
				Input:            askInput,
				Status:           conversation.StatusPending,
				DeferredResult:   true,
				WouldAutoExecute: tc.wouldAutoExecute,
				Shared:           conversation.BoolPtr(false),
			})
			content, err := json.Marshal(blocks)
			require.NoError(t, err)
			approvalPostID := "approval-post-id"
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &approvalPostID,
				Role:           "assistant",
				Content:        content,
				Sequence:       nextSeq,
			}))
			seededTurns := nextSeq

			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(true, nil)
			mmClient.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			// Dispatch-time destination resolution degrades gracefully; the
			// destination matrix has its own table.
			mmClient.On("GetPost", mock.Anything).Maybe().Return(nil, errors.New("not found"))

			dmCalls := 0
			if tc.wantDMCalls > 0 {
				mmClient.On("GetUserByUsername", "bob").Return(&model.User{Id: "bob-id", Username: "bob"}, nil)
				mmClient.On("DM", "bot-id", "bob-id", mock.AnythingOfType("*model.Post")).
					Run(func(mock.Arguments) { dmCalls++ }).
					Return(tc.dmErr).
					Times(tc.wantDMCalls)
			}
			publishes := 0
			if tc.wantPublish {
				mmClient.On("PublishWebSocketEvent", "conversation_updated",
					map[string]interface{}{"conversation_id": conv.ID}, mock.Anything).
					Run(func(mock.Arguments) { publishes++ }).
					Return().
					Once()
			}

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				mmClient:          mmClient,
				contextBuilder:    askAnotherUserBuilder(t),
				bots:              newAskAnotherUserBotsService(t, bot),
				convService:       conversation.NewService(convStore, nil, nil, nil),
				streamingService:  streamingService,
				toolPolicyChecker: tc.policyChecker,
				configProvider:    &channelFollowUpTestConfig{enableAskAnotherUser: true},
			}

			approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)

			channel := dmChannel
			if tc.inChannel {
				channel = &model.Channel{Id: "town-square", Type: model.ChannelTypeOpen, Name: "town-square", TeamId: "team-id"}
			}

			require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, channel, tc.acceptedIDs, nil))
			streamingService.waitForStreaming()

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			wantTurns := seededTurns
			if tc.wantResultTurn {
				wantTurns++
			}
			require.Len(t, turns, wantTurns, "no empty tool_result turn may be written for waiting-only batches")

			var updated []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[seededTurns-1].Content, &updated))
			askBlock := updated[len(updated)-1]
			assert.Equal(t, tc.wantBlockStatus, askBlock.Status)
			assert.True(t, askBlock.DeferredResult, "deferred flag must survive the persist")
			if tc.wantBlockStatus == conversation.StatusWaiting {
				require.NotNil(t, askBlock.Shared)
				assert.False(t, *askBlock.Shared, "waiting blocks stay unshared until answered")
			}
			if tc.includeNormal {
				assert.Equal(t, conversation.StatusSuccess, updated[0].Status, "normal tool executes despite the deferred sibling")
			}

			if tc.wantResultTurn {
				var resultBlocks []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(turns[len(turns)-1].Content, &resultBlocks))
				require.Len(t, resultBlocks, 1, "waiting calls contribute no result; only the resolved call may appear")
				assert.Contains(t, resultBlocks[0].Content, tc.wantResultText)
				if tc.wantResultErr {
					assert.Equal(t, conversation.StatusError, resultBlocks[0].Status)
				} else {
					assert.Equal(t, conversation.StatusSuccess, resultBlocks[0].Status)
				}
				if tc.includeNormal {
					assert.Equal(t, "tool-use-1", resultBlocks[0].ToolUseID)
				}
				if tc.inChannel {
					// Channel results stay undecided until the requester's
					// Share/Keep-Private click in HandleToolResult.
					assert.Nil(t, resultBlocks[0].DecidedAt, "channel results must await the share decision")
					require.NotNil(t, resultBlocks[0].Shared)
					assert.False(t, *resultBlocks[0].Shared, "channel results stay unshared until the share decision")
				}
			}

			assert.Equal(t, tc.wantDMCalls, dmCalls)
			if tc.wantPublish {
				assert.Equal(t, 1, publishes)
			}
			if tc.wantFollowUp {
				assert.Len(t, lm.requests, 1, "expected a follow-up LLM request")
			} else {
				assert.Empty(t, lm.requests, "waiting/rejected-only batches must not stream a follow-up")
			}
		})
	}
}

// TestHandleToolCallDeferredDoubleAccept pins the idempotency guarantee: the
// waiting flip is persisted before the card is sent, so a second Accept click
// finds no pending block and fails with ErrStaleToolClick — exactly one card
// is ever delivered.
func TestHandleToolCallDeferredDoubleAccept(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	blocks := []conversation.ContentBlock{{
		Type:           conversation.BlockTypeToolUse,
		ID:             "ask-1",
		Name:           "AskAnotherUser",
		Input:          json.RawMessage(`{"username":"bob","question":"Which environment?"}`),
		Status:         conversation.StatusPending,
		DeferredResult: true,
		Shared:         conversation.BoolPtr(false),
	}}
	content, err := json.Marshal(blocks)
	require.NoError(t, err)
	approvalPostID := "approval-post-id"
	require.NoError(t, convStore.CreateTurn(&store.Turn{
		ID:             "assistant-turn",
		ConversationID: conv.ID,
		PostID:         &approvalPostID,
		Role:           "assistant",
		Content:        content,
		Sequence:       1,
	}))

	lm := &loadedStateLLM{}
	bot := loadedStateBot(lm)

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
	mmClient.On("GetConfig").Maybe().Return(&model.Config{})
	mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
	mmClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(true, nil)
	mmClient.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	mmClient.On("GetPost", mock.Anything).Maybe().Return(nil, errors.New("not found"))
	mmClient.On("GetUserByUsername", "bob").Return(&model.User{Id: "bob-id", Username: "bob"}, nil).Once()
	dmCalls := 0
	mmClient.On("DM", "bot-id", "bob-id", mock.AnythingOfType("*model.Post")).
		Run(func(mock.Arguments) { dmCalls++ }).
		Return(nil).
		Once()
	mmClient.On("PublishWebSocketEvent", "conversation_updated", mock.Anything, mock.Anything).Maybe().Return()

	c := &Conversations{
		mmClient:         mmClient,
		contextBuilder:   askAnotherUserBuilder(t),
		bots:             newAskAnotherUserBotsService(t, bot),
		convService:      conversation.NewService(convStore, nil, nil, nil),
		streamingService: &loadedStateStreamingService{},
		configProvider:   &channelFollowUpTestConfig{enableAskAnotherUser: true},
	}

	approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
	approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
	dmChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

	require.NoError(t, c.HandleToolCall(context.Background(), "user-id", approvalPost, dmChannel, []string{"ask-1"}, nil))

	err = c.HandleToolCall(context.Background(), "user-id", approvalPost, dmChannel, []string{"ask-1"}, nil)
	require.ErrorIs(t, err, ErrStaleToolClick)
	assert.Equal(t, 1, dmCalls, "a second Accept must never send a second card")
}

// TestAskToolUseClaimRaces pins the atomic KV claim that closes the
// concurrent duplicate-submission window (two tabs/devices or two HA nodes)
// which the plain read-check-write status guards cannot: the claim loser
// must fail with the same sentinel as a sequential duplicate and write
// nothing — no second card, no second tool_result turn. A KV failure also
// blocks the transition (fail closed) but surfaces as a plain error rather
// than a misleading "already handled" sentinel.
func TestAskToolUseClaimRaces(t *testing.T) {
	askInput := json.RawMessage(`{"username":"bob","question":"Which environment?","options":[{"label":"Prod"},{"label":"Staging"}]}`)

	cases := []struct {
		name      string
		action    string // "dispatch" (HandleToolCall accept) | "answer" (HandleAskUserResponse)
		claimErr  error
		wantErrIs error // nil means any non-sentinel error is acceptable
	}{
		{
			name:      "concurrent duplicate accept loses the dispatch claim and sends no card",
			action:    "dispatch",
			wantErrIs: ErrStaleToolClick,
		},
		{
			name:     "KV failure on the dispatch claim fails the accept without sending a card",
			action:   "dispatch",
			claimErr: errors.New("kv down"),
		},
		{
			name:      "concurrent duplicate answer loses the answer claim and writes no result turn",
			action:    "answer",
			wantErrIs: ErrAskNotPending,
		},
		{
			name:     "KV failure on the answer claim fails the answer without writing",
			action:   "answer",
			claimErr: errors.New("kv down"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()

			seedStatus := conversation.StatusWaiting
			if tc.action == "dispatch" {
				seedStatus = conversation.StatusPending
			}
			blocks := []conversation.ContentBlock{{
				Type:           conversation.BlockTypeToolUse,
				ID:             "ask-1",
				Name:           "AskAnotherUser",
				Input:          askInput,
				Status:         seedStatus,
				DeferredResult: true,
				Shared:         conversation.BoolPtr(false),
			}}
			content, err := json.Marshal(blocks)
			require.NoError(t, err)
			anchorPostID := "anchor-post-id"
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &anchorPostID,
				Role:           "assistant",
				Content:        content,
				Sequence:       1,
			}))

			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)

			// The claim loses; the mockery strict mock doubles as the
			// "no side effects" assertion — any DM, card patch, result
			// write, or publish after a lost claim fails the test.
			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
			mmClient.On("GetUser", "bob-id").Maybe().Return(&model.User{Id: "bob-id", Username: "bob"}, nil)
			mmClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).
				Return(false, tc.claimErr).Once()

			c := &Conversations{
				mmClient:         mmClient,
				contextBuilder:   askAnotherUserBuilder(t),
				bots:             newAskAnotherUserBotsService(t, bot),
				convService:      conversation.NewService(convStore, nil, nil, nil),
				streamingService: &loadedStateStreamingService{},
			}

			switch tc.action {
			case "dispatch":
				approvalPost := &model.Post{Id: anchorPostID, UserId: "bot-id"}
				approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
				dmChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
				err = c.HandleToolCall(context.Background(), "user-id", approvalPost, dmChannel, []string{"ask-1"}, nil)
			case "answer":
				cardPost := &model.Post{Id: "card-post-id", UserId: "bot-id", Type: AskUserPostType}
				cardPost.AddProp(AskUserTargetIDProp, "bob-id")
				cardPost.AddProp(AskUserConversationIDProp, conv.ID)
				cardPost.AddProp(AskUserToolUseIDProp, "ask-1")
				cardChannel := &model.Channel{Id: "card-dm", Type: model.ChannelTypeDirect, Name: "bob-id__bot-id"}
				_, err = c.HandleAskUserResponse(context.Background(), "bob-id", cardPost, cardChannel, AskUserResponse{
					Action:   AskUserActionAnswer,
					Selected: []string{"Prod"},
				})
			}

			require.Error(t, err)
			if tc.wantErrIs != nil {
				require.ErrorIs(t, err, tc.wantErrIs)
			} else {
				// Infra failures must not masquerade as the duplicate
				// sentinels the webapp treats as benign.
				require.NotErrorIs(t, err, ErrStaleToolClick)
				require.NotErrorIs(t, err, ErrAskNotPending)
			}

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			require.Len(t, turns, 1, "a lost claim must not write a tool_result turn")

			assert.Empty(t, lm.requests, "a lost claim must not stream a follow-up")
		})
	}
}

// TestHandleAskUserResponse covers the target-side answer endpoint logic:
// answers and declines resolve the waiting call into a C7 tool result and
// resume the conversation; authorization, staleness, and validation failures
// map to the documented sentinels without consuming the question; and the
// follow-up is gated while other tool calls remain unresolved.
func TestHandleAskUserResponse(t *testing.T) {
	askInput := `{"username":"bob","question":"Which environment?","options":[{"label":"Prod"},{"label":"Staging"}]}`

	cases := []struct {
		name                   string
		caller                 string
		action                 string
		selected               []string
		freeForm               string
		seedStatus             string
		blockID                string
		notACard               bool
		cardConvID             string
		extraPending           bool
		channelAnchor          bool
		extraExecutedUndecided bool
		cardPatchFails         bool
		wantErr                error
		wantBlockStatus        string
		wantResultJSON         string
		wantFollowUp           bool
		wantCardStatus         string
		wantPreview            string
	}{
		{
			name:            "answer resumes the conversation",
			action:          AskUserActionAnswer,
			selected:        []string{"Prod"},
			wantBlockStatus: conversation.StatusSuccess,
			wantResultJSON:  `{"status":"answered","target_username":"bob","selected":["Prod"],"free_form":""}`,
			wantFollowUp:    true,
			wantCardStatus:  AskUserStatusAnswered,
			wantPreview:     "Prod",
		},
		{
			name:            "free-form answer round-trips into the result",
			action:          AskUserActionAnswer,
			freeForm:        "Use staging please",
			wantBlockStatus: conversation.StatusSuccess,
			wantResultJSON:  `{"status":"answered","target_username":"bob","selected":[],"free_form":"Use staging please"}`,
			wantFollowUp:    true,
			wantCardStatus:  AskUserStatusAnswered,
			wantPreview:     "Use staging please",
		},
		{
			name:            "decline resumes with the decline marker",
			action:          AskUserActionDecline,
			wantBlockStatus: conversation.StatusRejected,
			wantResultJSON:  `{"status":"declined","target_username":"bob"}`,
			wantFollowUp:    true,
			wantCardStatus:  AskUserStatusDeclined,
			wantPreview:     "",
		},
		{
			name:     "non-target caller is forbidden",
			caller:   "mallory-id",
			action:   AskUserActionAnswer,
			selected: []string{"Prod"},
			wantErr:  ErrNotAskTarget,
		},
		{
			name:       "already answered question conflicts",
			action:     AskUserActionAnswer,
			selected:   []string{"Prod"},
			seedStatus: conversation.StatusSuccess,
			wantErr:    ErrAskNotPending,
		},
		{
			name:       "conversation gone",
			action:     AskUserActionAnswer,
			selected:   []string{"Prod"},
			cardConvID: "missing-conv",
			wantErr:    ErrAskConversationGone,
		},
		{
			name:     "tool call superseded by regenerate",
			action:   AskUserActionAnswer,
			selected: []string{"Prod"},
			blockID:  "other-block",
			wantErr:  ErrAskConversationGone,
		},
		{
			name:     "invalid answer leaves the question waiting",
			action:   AskUserActionAnswer,
			selected: []string{"NotAnOption"},
			wantErr:  ErrInvalidAskAnswer,
		},
		{
			name:    "unknown action is invalid",
			action:  "shrug",
			wantErr: ErrInvalidAskAnswer,
		},
		{
			name:     "post that is not a card is invalid",
			action:   AskUserActionAnswer,
			selected: []string{"Prod"},
			notACard: true,
			wantErr:  ErrInvalidAskAnswer,
		},
		{
			name:            "mixed batch gates the follow-up",
			action:          AskUserActionAnswer,
			selected:        []string{"Prod"},
			extraPending:    true,
			wantBlockStatus: conversation.StatusSuccess,
			wantResultJSON:  `{"status":"answered","target_username":"bob","selected":["Prod"],"free_form":""}`,
			wantFollowUp:    false,
			wantCardStatus:  AskUserStatusAnswered,
			wantPreview:     "Prod",
		},
		{
			name:            "card patch failure still records the answer",
			action:          AskUserActionAnswer,
			selected:        []string{"Prod"},
			cardPatchFails:  true,
			wantBlockStatus: conversation.StatusSuccess,
			wantResultJSON:  `{"status":"answered","target_username":"bob","selected":["Prod"],"free_form":""}`,
			wantFollowUp:    true,
		},
		{
			// C6: channel-anchored answers are user-authored, so they are
			// shared+decided immediately (no Share/Keep-Private stage) and
			// the follow-up streams with isDM=false.
			name:            "channel anchor answer is shared and streams with no share stage",
			action:          AskUserActionAnswer,
			selected:        []string{"Prod"},
			channelAnchor:   true,
			wantBlockStatus: conversation.StatusSuccess,
			wantResultJSON:  `{"status":"answered","target_username":"bob","selected":["Prod"],"free_form":""}`,
			wantFollowUp:    true,
			wantCardStatus:  AskUserStatusAnswered,
			wantPreview:     "Prod",
		},
		{
			name:            "channel anchor decline is shared and streams with no share stage",
			action:          AskUserActionDecline,
			channelAnchor:   true,
			wantBlockStatus: conversation.StatusRejected,
			wantResultJSON:  `{"status":"declined","target_username":"bob"}`,
			wantFollowUp:    true,
			wantCardStatus:  AskUserStatusDeclined,
			wantPreview:     "",
		},
		{
			// MAJOR-1 regression (answer-first ordering): while a sibling
			// tool's channel result still awaits its Share/Keep-Private
			// decision, the answer is recorded but the resume belongs to
			// HandleToolResult — streaming now would demote the anchor turn
			// and orphan the pending share click.
			name:                   "channel anchor undecided share decision defers the follow-up",
			action:                 AskUserActionAnswer,
			selected:               []string{"Prod"},
			channelAnchor:          true,
			extraExecutedUndecided: true,
			wantBlockStatus:        conversation.StatusSuccess,
			wantResultJSON:         `{"status":"answered","target_username":"bob","selected":["Prod"],"free_form":""}`,
			wantFollowUp:           false,
			wantCardStatus:         AskUserStatusAnswered,
			wantPreview:            "Prod",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()

			blockID := "ask-1"
			if tc.blockID != "" {
				blockID = tc.blockID
			}
			seedStatus := conversation.StatusWaiting
			if tc.seedStatus != "" {
				seedStatus = tc.seedStatus
			}
			blocks := []conversation.ContentBlock{{
				Type:           conversation.BlockTypeToolUse,
				ID:             blockID,
				Name:           "AskAnotherUser",
				Input:          json.RawMessage(askInput),
				Status:         seedStatus,
				DeferredResult: true,
				Shared:         conversation.BoolPtr(false),
			}}
			if tc.extraPending {
				blocks = append(blocks, conversation.ContentBlock{
					Type:   conversation.BlockTypeToolUse,
					ID:     "tool-use-2",
					Name:   "jira__get_issue",
					Input:  json.RawMessage(`{}`),
					Status: conversation.StatusPending,
					Shared: conversation.BoolPtr(false),
				})
			}
			if tc.extraExecutedUndecided {
				blocks = append(blocks, conversation.ContentBlock{
					Type:   conversation.BlockTypeToolUse,
					ID:     "tool-use-2",
					Name:   "jira__get_issue",
					Input:  json.RawMessage(`{}`),
					Status: conversation.StatusSuccess,
					Shared: conversation.BoolPtr(false),
				})
			}
			content, err := json.Marshal(blocks)
			require.NoError(t, err)
			anchorPostID := "anchor-post-id"
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &anchorPostID,
				Role:           "assistant",
				Content:        content,
				Sequence:       1,
			}))
			seededTurns := 1
			if tc.extraExecutedUndecided {
				// The executed sibling's channel result has no share decision
				// yet (DecidedAt unset), mirroring HandleToolCall's output
				// for a channel-anchored mixed batch.
				undecided := []conversation.ContentBlock{{
					Type:      conversation.BlockTypeToolResult,
					ToolUseID: "tool-use-2",
					Content:   "restored-result",
					Status:    conversation.StatusSuccess,
					Shared:    conversation.BoolPtr(false),
				}}
				undecidedContent, marshalErr := json.Marshal(undecided)
				require.NoError(t, marshalErr)
				require.NoError(t, convStore.CreateTurn(&store.Turn{
					ID:             "undecided-result-turn",
					ConversationID: conv.ID,
					Role:           "tool_result",
					Content:        undecidedContent,
					Sequence:       2,
				}))
				seededTurns = 2
			}

			cardPost := &model.Post{Id: "card-post-id", UserId: "bot-id", Type: AskUserPostType}
			if tc.notACard {
				cardPost.Type = ""
			}
			cardConvID := conv.ID
			if tc.cardConvID != "" {
				cardConvID = tc.cardConvID
			}
			cardPost.AddProp(AskUserTargetIDProp, "bob-id")
			cardPost.AddProp(AskUserConversationIDProp, cardConvID)
			cardPost.AddProp(AskUserToolUseIDProp, "ask-1")

			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)

			anchorChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
			if tc.channelAnchor {
				anchorChannel = &model.Channel{Id: "town-square", Type: model.ChannelTypeOpen, Name: "town-square", TeamId: "team-id"}
			}
			anchorPost := &model.Post{Id: anchorPostID, UserId: "bot-id", ChannelId: anchorChannel.Id}

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("LogError", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(true, nil)
			mmClient.On("GetUser", "bob-id").Maybe().Return(&model.User{Id: "bob-id", Username: "bob"}, nil)
			mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
			mmClient.On("GetPost", anchorPostID).Maybe().Return(anchorPost, nil)
			mmClient.On("GetChannel", anchorChannel.Id).Maybe().Return(anchorChannel, nil)
			if tc.cardPatchFails {
				mmClient.On("GetPost", "card-post-id").Maybe().Return(nil, errors.New("card gone"))
			} else {
				mmClient.On("GetPost", "card-post-id").Maybe().Return(cardPost, nil)
			}
			var patchedPost *model.Post
			mmClient.On("UpdatePost", mock.AnythingOfType("*model.Post")).Maybe().
				Run(func(args mock.Arguments) { patchedPost = args.Get(0).(*model.Post) }).
				Return(nil)
			publishes := 0
			mmClient.On("PublishWebSocketEvent", "conversation_updated",
				map[string]interface{}{"conversation_id": conv.ID}, mock.Anything).Maybe().
				Run(func(mock.Arguments) { publishes++ }).
				Return()

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				mmClient:         mmClient,
				contextBuilder:   askAnotherUserBuilder(t),
				bots:             newAskAnotherUserBotsService(t, bot),
				convService:      conversation.NewService(convStore, nil, nil, nil),
				streamingService: streamingService,
			}

			caller := "bob-id"
			if tc.caller != "" {
				caller = tc.caller
			}
			cardChannel := &model.Channel{Id: "card-dm", Type: model.ChannelTypeDirect, Name: "bob-id__bot-id"}
			req := AskUserResponse{Action: tc.action, Selected: tc.selected, FreeForm: tc.freeForm}

			_, err = c.HandleAskUserResponse(context.Background(), caller, cardPost, cardChannel, req)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				// Failure must leave the question in its seeded state with
				// no tool_result turn.
				turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
				require.NoError(t, turnsErr)
				require.Len(t, turns, seededTurns)
				var unchanged []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(turns[0].Content, &unchanged))
				assert.Equal(t, seedStatus, unchanged[0].Status)
				assert.Zero(t, publishes)
				return
			}
			require.NoError(t, err)
			streamingService.waitForStreaming()

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			require.Len(t, turns, seededTurns+1)

			var updated []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[0].Content, &updated))
			assert.Equal(t, tc.wantBlockStatus, updated[0].Status)
			require.NotNil(t, updated[0].Shared)
			assert.True(t, *updated[0].Shared)

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[len(turns)-1].Content, &resultBlocks))
			require.Len(t, resultBlocks, 1)
			assert.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
			assert.Equal(t, "ask-1", resultBlocks[0].ToolUseID)
			// Declines are valid results the model must consume, not errors.
			assert.Equal(t, conversation.StatusSuccess, resultBlocks[0].Status)
			require.NotNil(t, resultBlocks[0].Shared)
			assert.True(t, *resultBlocks[0].Shared)
			assert.NotNil(t, resultBlocks[0].DecidedAt)
			assert.JSONEq(t, tc.wantResultJSON, resultBlocks[0].Content)

			if tc.cardPatchFails {
				assert.Nil(t, patchedPost, "patch failure must not update the card")
			} else {
				require.NotNil(t, patchedPost)
				assert.Equal(t, tc.wantCardStatus, patchedPost.GetProp(AskUserStatusProp))
				assert.NotNil(t, patchedPost.GetProp(AskUserAnsweredAtProp))
				assert.Equal(t, tc.wantPreview, patchedPost.GetProp(AskUserAnswerPreviewProp))
			}

			assert.Equal(t, 1, publishes, "answer must refresh the initiator's conversation view")

			if tc.wantFollowUp {
				assert.Len(t, lm.requests, 1, "expected a follow-up LLM request")
			} else {
				assert.Empty(t, lm.requests, "follow-up must wait for the remaining unresolved tool calls")
			}
		})
	}
}

func TestHandleAskUserResponseTerminalOutcomes(t *testing.T) {
	askInput := json.RawMessage(`{"username":"bob","question":"Which environment?","options":[{"label":"Prod"}]}`)

	cases := []struct {
		name          string
		seedStatus    string
		seedResult    string
		cancelAtClaim bool
		wantStatus    string
		wantErr       error
	}{
		{
			name:       "cancel then late answer is a successful no-op",
			seedStatus: conversation.StatusSuccess,
			seedResult: `{"status":"canceled","target_username":"bob"}`,
			wantStatus: AskUserStatusCanceled,
		},
		{
			name:          "cancel winning after the answer read is a successful no-op",
			seedStatus:    conversation.StatusWaiting,
			seedResult:    `{"status":"canceled","target_username":"bob"}`,
			cancelAtClaim: true,
			wantStatus:    AskUserStatusCanceled,
		},
		{
			name:       "duplicate answer remains a conflict",
			seedStatus: conversation.StatusSuccess,
			seedResult: `{"status":"answered","target_username":"bob","selected":["Prod"],"free_form":""}`,
			wantErr:    ErrAskNotPending,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			blocks := []conversation.ContentBlock{{
				Type:           conversation.BlockTypeToolUse,
				ID:             "ask-1",
				Name:           mmtools.AskAnotherUserToolName,
				Input:          askInput,
				Status:         tc.seedStatus,
				DeferredResult: true,
				Shared:         conversation.BoolPtr(true),
			}}
			content, err := json.Marshal(blocks)
			require.NoError(t, err)
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				Role:           "assistant",
				Content:        content,
				Sequence:       1,
			}))

			writeSeedResult := func() {
				resultContent, marshalErr := json.Marshal([]conversation.ContentBlock{{
					Type:      conversation.BlockTypeToolResult,
					ToolUseID: "ask-1",
					Content:   tc.seedResult,
					Status:    conversation.StatusSuccess,
				}})
				require.NoError(t, marshalErr)
				require.NoError(t, convStore.CreateTurn(&store.Turn{
					ID:             "canceled-result-turn",
					ConversationID: conv.ID,
					Role:           "tool_result",
					Content:        resultContent,
					Sequence:       2,
				}))
			}
			if !tc.cancelAtClaim {
				writeSeedResult()
			}

			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)
			mmClient := mocks.NewMockClient(t)
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})
			mmClient.On("GetUser", "bob-id").Maybe().Return(&model.User{Id: "bob-id", Username: "bob"}, nil)
			if tc.cancelAtClaim {
				mmClient.On("KVCompareAndSet", "askclaim_answer_ask-1", mock.Anything, []byte(AskUserActionAnswer)).
					Return(false, nil).
					Once()
				mmClient.On("KVGet", "askclaim_answer_ask-1", mock.AnythingOfType("*[]uint8")).
					Run(func(args mock.Arguments) {
						resolution := args.Get(1).(*[]byte)
						*resolution = []byte(AskUserStatusCanceled)
					}).
					Return(nil).
					Once()
			}

			c := &Conversations{
				mmClient:         mmClient,
				bots:             newAskAnotherUserBotsService(t, bot),
				convService:      conversation.NewService(convStore, nil, nil, nil),
				streamingService: &loadedStateStreamingService{},
			}
			cardPost := &model.Post{Id: "card-post-id", UserId: "bot-id", Type: AskUserPostType}
			cardPost.AddProp(AskUserTargetIDProp, "bob-id")
			cardPost.AddProp(AskUserConversationIDProp, conv.ID)
			cardPost.AddProp(AskUserToolUseIDProp, "ask-1")

			status, responseErr := c.HandleAskUserResponse(
				context.Background(),
				"bob-id",
				cardPost,
				&model.Channel{Id: "card-dm"},
				AskUserResponse{Action: AskUserActionAnswer, Selected: []string{"Prod"}},
			)

			if tc.wantErr != nil {
				require.ErrorIs(t, responseErr, tc.wantErr)
			} else {
				require.NoError(t, responseErr)
			}
			require.Equal(t, tc.wantStatus, status)

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			wantTurns := 2
			if tc.cancelAtClaim {
				wantTurns = 1
			}
			require.Len(t, turns, wantTurns, "the stale response must not append another tool_result turn")
			assert.Empty(t, lm.requests, "the stale response must not stream a second follow-up")
		})
	}
}

// TestHandleAskUserCancel covers the initiator-side cancel endpoint logic
// (V2-C4): a cancel resolves the waiting block into the canceled tool result,
// patches the target's card via the dispatch-time KV pointer, and resumes the
// conversation under the same gates as the answer path; authorization and
// staleness failures map to the documented sentinels with zero writes.
func TestHandleAskUserCancel(t *testing.T) {
	askInput := `{"username":"bob","question":"Which environment?","options":[{"label":"Prod"},{"label":"Staging"}]}`

	cases := []struct {
		name                   string
		caller                 string
		toolUseID              string
		seedStatus             string
		blockName              string
		notDeferred            bool
		turnPostID             string
		missingConvProp        bool
		convDeleted            bool
		claimTaken             bool
		pointerMissing         bool
		extraWaiting           bool
		channelAnchor          bool
		extraExecutedUndecided bool
		wantErr                error
		wantFollowUp           bool
	}{
		{
			name:         "happy path cancels and resumes the DM conversation",
			wantFollowUp: true,
		},
		{
			// Channel semantics: the cancel result itself is decided at
			// creation, but a sibling's undecided Share/Keep-Private stage
			// still defers the resume to HandleToolResult.
			name:                   "channel anchor with an undecided sibling share defers the follow-up",
			channelAnchor:          true,
			extraExecutedUndecided: true,
			wantFollowUp:           false,
		},
		{
			name:    "non-initiator is forbidden",
			caller:  "mallory-id",
			wantErr: ErrNotRequester,
		},
		{
			name:            "post without a conversation reference is invalid",
			missingConvProp: true,
			wantErr:         ErrPostMissingConversationID,
		},
		{
			name:        "deleted conversation is gone",
			convDeleted: true,
			wantErr:     ErrAskConversationGone,
		},
		{
			name:      "unknown tool_use is gone",
			toolUseID: "never-heard-of-it",
			wantErr:   ErrAskConversationGone,
		},
		{
			name:       "already resolved block conflicts",
			seedStatus: conversation.StatusSuccess,
			wantErr:    ErrAskNotPending,
		},
		{
			name:       "block anchored on a different post conflicts",
			turnPostID: "other-post-id",
			wantErr:    ErrAskNotPending,
		},
		{
			name:      "waiting block of another tool conflicts",
			blockName: "jira__get_issue",
			wantErr:   ErrAskNotPending,
		},
		{
			name:        "waiting non-deferred block conflicts",
			blockName:   "AskAnotherUser",
			notDeferred: true,
			wantErr:     ErrAskNotPending,
		},
		{
			name:       "lost claim conflicts with zero writes",
			claimTaken: true,
			wantErr:    ErrAskNotPending,
		},
		{
			name:           "missing card pointer still resolves the block",
			pointerMissing: true,
			wantFollowUp:   true,
		},
		{
			name:         "mixed batch with another waiting block defers the follow-up",
			extraWaiting: true,
			wantFollowUp: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			if tc.convDeleted {
				conv.DeleteAt = 1
				require.NoError(t, convStore.CreateConversation(conv)) // overwrite the seeded row
			}

			blockName := "AskAnotherUser"
			if tc.blockName != "" {
				blockName = tc.blockName
			}
			seedStatus := conversation.StatusWaiting
			if tc.seedStatus != "" {
				seedStatus = tc.seedStatus
			}
			blocks := []conversation.ContentBlock{{
				Type:           conversation.BlockTypeToolUse,
				ID:             "ask-1",
				Name:           blockName,
				Input:          json.RawMessage(askInput),
				Status:         seedStatus,
				DeferredResult: !tc.notDeferred,
				Shared:         conversation.BoolPtr(false),
			}}
			if tc.extraWaiting {
				blocks = append(blocks, conversation.ContentBlock{
					Type:           conversation.BlockTypeToolUse,
					ID:             "ask-2",
					Name:           "AskAnotherUser",
					Input:          json.RawMessage(askInput),
					Status:         conversation.StatusWaiting,
					DeferredResult: true,
					Shared:         conversation.BoolPtr(false),
				})
			}
			if tc.extraExecutedUndecided {
				blocks = append(blocks, conversation.ContentBlock{
					Type:   conversation.BlockTypeToolUse,
					ID:     "tool-use-2",
					Name:   "jira__get_issue",
					Input:  json.RawMessage(`{}`),
					Status: conversation.StatusSuccess,
					Shared: conversation.BoolPtr(false),
				})
			}
			content, err := json.Marshal(blocks)
			require.NoError(t, err)
			anchorPostID := "anchor-post-id"
			turnPostID := anchorPostID
			if tc.turnPostID != "" {
				turnPostID = tc.turnPostID
			}
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &turnPostID,
				Role:           "assistant",
				Content:        content,
				Sequence:       1,
			}))
			seededTurns := 1
			if tc.extraExecutedUndecided {
				undecided := []conversation.ContentBlock{{
					Type:      conversation.BlockTypeToolResult,
					ToolUseID: "tool-use-2",
					Content:   "restored-result",
					Status:    conversation.StatusSuccess,
					Shared:    conversation.BoolPtr(false),
				}}
				undecidedContent, marshalErr := json.Marshal(undecided)
				require.NoError(t, marshalErr)
				require.NoError(t, convStore.CreateTurn(&store.Turn{
					ID:             "undecided-result-turn",
					ConversationID: conv.ID,
					Role:           "tool_result",
					Content:        undecidedContent,
					Sequence:       2,
				}))
				seededTurns = 2
			}

			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)

			anchorChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
			if tc.channelAnchor {
				anchorChannel = &model.Channel{Id: "town-square", Type: model.ChannelTypeOpen, Name: "town-square", TeamId: "team-id"}
			}
			anchorPost := &model.Post{Id: anchorPostID, UserId: "bot-id", ChannelId: anchorChannel.Id}
			if !tc.missingConvProp {
				anchorPost.AddProp(streaming.ConversationIDProp, conv.ID)
			}

			cardPost := &model.Post{Id: "card-post-id", UserId: "bot-id", Type: AskUserPostType}
			cardPost.AddProp(AskUserTargetIDProp, "bob-id")
			cardPost.AddProp(AskUserStatusProp, AskUserStatusPending)

			mmClient := mocks.NewMockClient(t)
			for i := 1; i <= 7; i++ {
				logArgs := make([]interface{}, i)
				for j := range logArgs {
					logArgs[j] = mock.Anything
				}
				mmClient.On("LogDebug", logArgs...).Maybe().Return()
				mmClient.On("LogError", logArgs...).Maybe().Return()
				mmClient.On("LogWarn", logArgs...).Maybe().Return()
			}
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})
			mmClient.On("KVCompareAndSet", "askclaim_answer_ask-1", mock.Anything, []byte(AskUserStatusCanceled)).
				Maybe().Return(!tc.claimTaken, nil)
			mmClient.On("KVGet", askCardKVPrefix+"ask-1", mock.Anything).Maybe().
				Run(func(args mock.Arguments) {
					if !tc.pointerMissing {
						*(args.Get(1).(*string)) = "card-post-id"
					}
				}).Return(nil)
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("GetUser", "bob-id").Maybe().Return(&model.User{Id: "bob-id", Username: "bob"}, nil)
			mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
			mmClient.On("GetPost", anchorPostID).Maybe().Return(anchorPost, nil)
			mmClient.On("GetPost", "card-post-id").Maybe().Return(cardPost, nil)
			mmClient.On("GetChannel", anchorChannel.Id).Maybe().Return(anchorChannel, nil)
			var patchedPost *model.Post
			mmClient.On("UpdatePost", mock.AnythingOfType("*model.Post")).Maybe().
				Run(func(args mock.Arguments) { patchedPost = args.Get(0).(*model.Post) }).
				Return(nil)
			publishes := 0
			mmClient.On("PublishWebSocketEvent", "conversation_updated",
				map[string]interface{}{"conversation_id": conv.ID}, mock.Anything).Maybe().
				Run(func(mock.Arguments) { publishes++ }).
				Return()

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				mmClient:         mmClient,
				contextBuilder:   askAnotherUserBuilder(t),
				bots:             newAskAnotherUserBotsService(t, bot),
				convService:      conversation.NewService(convStore, nil, nil, nil),
				streamingService: streamingService,
				configProvider:   &channelFollowUpTestConfig{enableAskAnotherUser: true},
			}

			caller := "user-id"
			if tc.caller != "" {
				caller = tc.caller
			}
			toolUseID := "ask-1"
			if tc.toolUseID != "" {
				toolUseID = tc.toolUseID
			}

			err = c.HandleAskUserCancel(context.Background(), caller, anchorPost, anchorChannel, toolUseID)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				// Failure must leave the block in its seeded state with no
				// tool_result turn, no card patch, and no publish.
				turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
				require.NoError(t, turnsErr)
				require.Len(t, turns, seededTurns, "a failed cancel must not write a tool_result turn")
				var unchanged []conversation.ContentBlock
				require.NoError(t, json.Unmarshal(turns[0].Content, &unchanged))
				assert.Equal(t, seedStatus, unchanged[0].Status)
				assert.Nil(t, patchedPost, "a failed cancel must not patch the card")
				assert.Zero(t, publishes)
				assert.Empty(t, lm.requests)
				return
			}
			require.NoError(t, err)
			streamingService.waitForStreaming()

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			require.Len(t, turns, seededTurns+1)

			var updated []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[0].Content, &updated))
			// V2-C5: no new content-block status — the canceled call
			// completed with a valid result.
			assert.Equal(t, conversation.StatusSuccess, updated[0].Status)
			require.NotNil(t, updated[0].Shared)
			assert.True(t, *updated[0].Shared)

			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[len(turns)-1].Content, &resultBlocks))
			require.Len(t, resultBlocks, 1)
			assert.Equal(t, conversation.BlockTypeToolResult, resultBlocks[0].Type)
			assert.Equal(t, "ask-1", resultBlocks[0].ToolUseID)
			assert.Equal(t, conversation.StatusSuccess, resultBlocks[0].Status)
			require.NotNil(t, resultBlocks[0].Shared)
			assert.True(t, *resultBlocks[0].Shared)
			assert.NotNil(t, resultBlocks[0].DecidedAt)
			assert.JSONEq(t, `{"status":"canceled","target_username":"bob"}`, resultBlocks[0].Content)

			if tc.pointerMissing {
				assert.Nil(t, patchedPost, "a lost pointer degrades to an unpatched card, not an error")
			} else {
				require.NotNil(t, patchedPost, "the target's card must be patched to canceled")
				assert.Equal(t, AskUserStatusCanceled, patchedPost.GetProp(AskUserStatusProp))
				assert.Equal(t, "This question is no longer needed.", patchedPost.Message)
			}

			assert.Equal(t, 1, publishes, "cancel must refresh the initiator's conversation view")

			if tc.wantFollowUp {
				assert.Len(t, lm.requests, 1, "expected exactly one follow-up LLM request")
			} else {
				assert.Empty(t, lm.requests, "follow-up must wait for the remaining unresolved work")
			}
		})
	}
}

// TestCancelAnswerRace pins V2-C4's single-resolution guarantee: answer and
// cancel contend for the same one-shot claim, so whichever lands first wins
// and exactly one tool_result turn is written. A late answer after cancel is a
// successful canceled no-op; a late cancel after answer remains a conflict.
func TestCancelAnswerRace(t *testing.T) {
	askInput := `{"username":"bob","question":"Which environment?","options":[{"label":"Prod"},{"label":"Staging"}]}`

	cases := []struct {
		name        string
		cancelFirst bool
		wantResult  string
	}{
		{
			name:        "cancel wins and a late answer is a graceful no-op",
			cancelFirst: true,
			wantResult:  `{"status":"canceled","target_username":"bob"}`,
		},
		{
			name:        "answer wins and a late cancel is a graceful no-op",
			cancelFirst: false,
			wantResult:  `{"status":"answered","target_username":"bob","selected":["Prod"],"free_form":""}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			blocks := []conversation.ContentBlock{{
				Type:           conversation.BlockTypeToolUse,
				ID:             "ask-1",
				Name:           "AskAnotherUser",
				Input:          json.RawMessage(askInput),
				Status:         conversation.StatusWaiting,
				DeferredResult: true,
				Shared:         conversation.BoolPtr(false),
			}}
			content, err := json.Marshal(blocks)
			require.NoError(t, err)
			anchorPostID := "anchor-post-id"
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &anchorPostID,
				Role:           "assistant",
				Content:        content,
				Sequence:       1,
			}))

			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)

			anchorChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
			anchorPost := &model.Post{Id: anchorPostID, UserId: "bot-id", ChannelId: anchorChannel.Id}
			anchorPost.AddProp(streaming.ConversationIDProp, conv.ID)

			cardPost := &model.Post{Id: "card-post-id", UserId: "bot-id", Type: AskUserPostType}
			cardPost.AddProp(AskUserTargetIDProp, "bob-id")
			cardPost.AddProp(AskUserConversationIDProp, conv.ID)
			cardPost.AddProp(AskUserToolUseIDProp, "ask-1")

			mmClient := mocks.NewMockClient(t)
			for i := 1; i <= 7; i++ {
				logArgs := make([]interface{}, i)
				for j := range logArgs {
					logArgs[j] = mock.Anything
				}
				mmClient.On("LogDebug", logArgs...).Maybe().Return()
				mmClient.On("LogError", logArgs...).Maybe().Return()
				mmClient.On("LogWarn", logArgs...).Maybe().Return()
			}
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})
			// The one-shot claim: the winner takes it, any later contender
			// loses. The status guard usually fires first for the loser;
			// the Maybe'd false return covers the pure-race window.
			mmClient.On("KVCompareAndSet", "askclaim_answer_ask-1", mock.Anything, mock.Anything).
				Return(true, nil).Once()
			mmClient.On("KVCompareAndSet", "askclaim_answer_ask-1", mock.Anything, mock.Anything).
				Maybe().Return(false, nil)
			mmClient.On("KVGet", askCardKVPrefix+"ask-1", mock.Anything).Maybe().
				Run(func(args mock.Arguments) { *(args.Get(1).(*string)) = "card-post-id" }).
				Return(nil)
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("GetUser", "bob-id").Maybe().Return(&model.User{Id: "bob-id", Username: "bob"}, nil)
			mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
			mmClient.On("GetPost", anchorPostID).Maybe().Return(anchorPost, nil)
			mmClient.On("GetPost", "card-post-id").Maybe().Return(cardPost, nil)
			mmClient.On("GetChannel", anchorChannel.Id).Maybe().Return(anchorChannel, nil)
			mmClient.On("UpdatePost", mock.AnythingOfType("*model.Post")).Maybe().Return(nil)
			mmClient.On("PublishWebSocketEvent", "conversation_updated", mock.Anything, mock.Anything).Maybe().Return()

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				mmClient:         mmClient,
				contextBuilder:   askAnotherUserBuilder(t),
				bots:             newAskAnotherUserBotsService(t, bot),
				convService:      conversation.NewService(convStore, nil, nil, nil),
				streamingService: streamingService,
				configProvider:   &channelFollowUpTestConfig{enableAskAnotherUser: true},
			}

			cancel := func() error {
				return c.HandleAskUserCancel(context.Background(), "user-id", anchorPost, anchorChannel, "ask-1")
			}
			answer := func() (string, error) {
				cardChannel := &model.Channel{Id: "card-dm", Type: model.ChannelTypeDirect, Name: "bob-id__bot-id"}
				return c.HandleAskUserResponse(context.Background(), "bob-id", cardPost, cardChannel, AskUserResponse{
					Action:   AskUserActionAnswer,
					Selected: []string{"Prod"},
				})
			}

			if tc.cancelFirst {
				require.NoError(t, cancel())
				streamingService.waitForStreaming()

				status, responseErr := answer()
				require.NoError(t, responseErr)
				require.Equal(t, AskUserStatusCanceled, status)
			} else {
				status, responseErr := answer()
				require.NoError(t, responseErr)
				require.Equal(t, AskUserStatusAnswered, status)
				streamingService.waitForStreaming()

				require.ErrorIs(t, cancel(), ErrAskNotPending)
			}

			turns, turnsErr := convStore.GetTurnsForConversation(conv.ID)
			require.NoError(t, turnsErr)
			require.Len(t, turns, 2, "exactly one resolution may write a tool_result turn")
			var resultBlocks []conversation.ContentBlock
			require.NoError(t, json.Unmarshal(turns[1].Content, &resultBlocks))
			require.Len(t, resultBlocks, 1)
			assert.JSONEq(t, tc.wantResult, resultBlocks[0].Content)

			assert.Len(t, lm.requests, 1, "the resume must stream exactly once, for the winner")
		})
	}
}

// TestChannelMixedBatchShareAnswerOrdering is the MAJOR-1 regression: a
// channel conversation with a mixed batch (normal tool + AskAnotherUser)
// must stream the resume exactly once, only after BOTH the requester's
// Share/Keep-Private decision and the target's answer are in — in either
// order. A premature stream would feed the model a dangling waiting
// tool_use and demote the anchor turn, orphaning the other half's resume.
func TestChannelMixedBatchShareAnswerOrdering(t *testing.T) {
	askInput := json.RawMessage(`{"username":"bob","question":"Which environment?","options":[{"label":"Prod"},{"label":"Staging"}]}`)

	cases := []struct {
		name       string
		shareFirst bool
		firstMsg   string
	}{
		{
			name:       "share before answer defers the stream to the answer",
			shareFirst: true,
			firstMsg:   "the Share click must not stream while the question is still waiting",
		},
		{
			name:       "answer before share defers the stream to the share click",
			shareFirst: false,
			firstMsg:   "the answer must not stream while the share decision is still outstanding",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			nextSeq := 1
			seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)

			blocks := []conversation.ContentBlock{
				{
					Type:   conversation.BlockTypeToolUse,
					ID:     "tool-use-1",
					Name:   "jira__get_issue",
					Input:  json.RawMessage(`{}`),
					Status: conversation.StatusPending,
					Shared: conversation.BoolPtr(false),
				},
				{
					Type:           conversation.BlockTypeToolUse,
					ID:             "ask-1",
					Name:           "AskAnotherUser",
					Input:          askInput,
					Status:         conversation.StatusPending,
					DeferredResult: true,
					Shared:         conversation.BoolPtr(false),
				},
			}
			content, err := json.Marshal(blocks)
			require.NoError(t, err)
			anchorPostID := "approval-post-id"
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &anchorPostID,
				Role:           "assistant",
				Content:        content,
				Sequence:       nextSeq,
			}))

			lm := &loadedStateLLM{}
			bot := loadedStateBot(lm)

			channel := &model.Channel{Id: "town-square", Type: model.ChannelTypeOpen, Name: "town-square", TeamId: "team-id"}
			anchorPost := &model.Post{Id: anchorPostID, UserId: "bot-id", ChannelId: channel.Id}
			anchorPost.AddProp(streaming.ConversationIDProp, conv.ID)

			cardPost := &model.Post{Id: "card-post-id", UserId: "bot-id", Type: AskUserPostType}
			cardPost.AddProp(AskUserTargetIDProp, "bob-id")
			cardPost.AddProp(AskUserConversationIDProp, conv.ID)
			cardPost.AddProp(AskUserToolUseIDProp, "ask-1")

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("LogError", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(true, nil)
			mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
			mmClient.On("GetUser", "bob-id").Maybe().Return(&model.User{Id: "bob-id", Username: "bob"}, nil)
			mmClient.On("GetUserByUsername", "bob").Return(&model.User{Id: "bob-id", Username: "bob"}, nil).Once()
			mmClient.On("DM", "bot-id", "bob-id", mock.AnythingOfType("*model.Post")).Return(nil).Once()
			mmClient.On("GetPost", anchorPostID).Maybe().Return(anchorPost, nil)
			mmClient.On("GetPost", "card-post-id").Maybe().Return(cardPost, nil)
			mmClient.On("GetChannel", channel.Id).Maybe().Return(channel, nil)
			mmClient.On("GetChannelStats", channel.Id).Maybe().Return(nil, errors.New("stats unavailable"))
			mmClient.On("UpdatePost", mock.AnythingOfType("*model.Post")).Maybe().Return(nil)
			mmClient.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("PublishWebSocketEvent", "conversation_updated", mock.Anything, mock.Anything).Maybe().Return()

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				mmClient:         mmClient,
				contextBuilder:   askAnotherUserBuilder(t),
				bots:             newAskAnotherUserBotsService(t, bot),
				convService:      conversation.NewService(convStore, nil, nil, nil),
				streamingService: streamingService,
				configProvider:   &channelFollowUpTestConfig{enableAskAnotherUser: true},
			}

			// Stage 1: the initiator accepts both. The normal tool executes
			// with an undecided channel share result, the ask parks waiting,
			// and nothing streams.
			require.NoError(t, c.HandleToolCall(context.Background(), "user-id", anchorPost, channel, []string{"tool-use-1", "ask-1"}, nil))
			require.Empty(t, lm.requests, "a mixed batch with a waiting question must not stream from HandleToolCall")

			share := func() error {
				return c.HandleToolResult(context.Background(), "user-id", anchorPost, channel, []string{"tool-use-1"})
			}
			answer := func() error {
				cardChannel := &model.Channel{Id: "card-dm", Type: model.ChannelTypeDirect, Name: "bob-id__bot-id"}
				_, responseErr := c.HandleAskUserResponse(context.Background(), "bob-id", cardPost, cardChannel, AskUserResponse{
					Action:   AskUserActionAnswer,
					Selected: []string{"Prod"},
				})
				return responseErr
			}

			first, second := answer, share
			if tc.shareFirst {
				first, second = share, answer
			}

			require.NoError(t, first())
			require.Empty(t, lm.requests, tc.firstMsg)

			require.NoError(t, second())
			streamingService.waitForStreaming()
			require.Len(t, lm.requests, 1, "the resume must stream exactly once, after both the share decision and the answer")
		})
	}
}

func TestAskUserAnswerPreview(t *testing.T) {
	long := make([]rune, 0, 300)
	for i := 0; i < 300; i++ {
		long = append(long, 'é')
	}

	cases := []struct {
		name     string
		selected []string
		freeForm string
		want     string
	}{
		{name: "labels only", selected: []string{"A", "B"}, want: "A, B"},
		{name: "free-form only", freeForm: "hello", want: "hello"},
		{name: "labels and free-form", selected: []string{"A"}, freeForm: "extra", want: "A — extra"},
		{name: "whitespace free-form dropped", selected: []string{"A"}, freeForm: "   ", want: "A"},
		{name: "empty", want: ""},
		{name: "long preview truncates to 200 runes", freeForm: string(long), want: string(long[:200])},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, askUserAnswerPreview(tc.selected, tc.freeForm))
		})
	}
}
