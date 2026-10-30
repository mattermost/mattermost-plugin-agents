// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// wakeScheduledAt is when test wakes were scheduled, in unix millis.
const wakeScheduledAt = 1000

// wakeUserID is a well-formed ID: the wake's usage-restriction gate runs the
// ABAC checker, which denies malformed user IDs outright.
var wakeUserID = model.NewId()

func newWakeJob(postID, convID, userID string, scheduledAt int64) WakeJob {
	return WakeJob{PostID: postID, UserID: userID, ConversationID: convID, ScheduledAt: scheduledAt}
}

func wakeConversationStore() (*loadedStateFlowStore, *store.Conversation) {
	convStore := newLoadedStateFlowStore()
	conv := &store.Conversation{
		ID:           "conv-id",
		UserID:       wakeUserID,
		BotID:        "bot-id",
		SystemPrompt: "system",
		Operation:    "conversation",
	}
	_ = convStore.CreateConversation(conv)
	return convStore, conv
}

func TestWakeJobFromProps(t *testing.T) {
	full := newWakeJob("post-id", "conv-id", "user-id", 1234)
	fullMap := func(omit string) map[string]any {
		m := map[string]any{"post_id": "post-id", "conversation_id": "conv-id", "user_id": "user-id", "scheduled_at": 1234}
		delete(m, omit)
		return m
	}

	tests := []struct {
		name    string
		props   any
		want    WakeJob
		wantErr bool
	}{
		{name: "typed struct from same-process schedule", props: full, want: full},
		{name: "generic JSON map after restart", props: fullMap(""), want: full},
		{name: "missing post id", props: fullMap("post_id"), wantErr: true},
		{name: "missing conversation id", props: fullMap("conversation_id"), wantErr: true},
		{name: "missing scheduling user", props: fullMap("user_id"), wantErr: true},
		{name: "missing scheduled time", props: fullMap("scheduled_at"), wantErr: true},
		{name: "non-object props", props: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := wakeJobFromProps(tt.props)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, job)
		})
	}
}

// kvFake is an in-memory plugin KV behind a MockClient, with the JSON encoding
// and create-if-absent / compare-and-delete semantics of the real client.
type kvFake struct {
	data    map[string][]byte
	setFail error
}

func newKVFake(mmClient *mocks.MockClient) *kvFake {
	kv := &kvFake{data: map[string][]byte{}}
	mmClient.EXPECT().KVGet(mock.Anything, mock.Anything).RunAndReturn(func(key string, value any) error {
		raw, ok := kv.data[key]
		if !ok {
			return mmapi.ErrKVNotFound
		}
		return json.Unmarshal(raw, value)
	}).Maybe()
	mmClient.EXPECT().KVCompareAndSetWithExpiry(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(key string, oldValue, newValue any, _ time.Duration) (bool, error) {
		if kv.setFail != nil {
			return false, kv.setFail
		}
		if oldValue != nil {
			return false, errors.New("kvFake only supports create-if-absent with expiry")
		}
		if _, exists := kv.data[key]; exists {
			return false, nil
		}
		raw, err := json.Marshal(newValue)
		kv.data[key] = raw
		return err == nil, err
	}).Maybe()
	mmClient.EXPECT().KVCompareAndSet(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(key string, oldValue, newValue any) (bool, error) {
		oldRaw, err := json.Marshal(oldValue)
		if err != nil || !bytes.Equal(kv.data[key], oldRaw) {
			return false, err
		}
		if newValue == nil {
			delete(kv.data, key)
		}
		return true, nil
	}).Maybe()
	return kv
}

// setMarker records job as the pending wake of post's thread.
func (e *wakeEnv) setMarker(t *testing.T, post *model.Post, job WakeJob) {
	t.Helper()
	raw, err := json.Marshal(job.wakeMarker)
	require.NoError(t, err)
	e.kv.data[responsePostMarkerKey(post)] = raw
}

// wakeEnv wires a Conversations for ResumeConversation tests. Tests register
// the Mattermost lookups they expect with expectLiveWake or directly on mmClient.
type wakeEnv struct {
	convStore *loadedStateFlowStore
	conv      *store.Conversation
	lm        *loadedStateLLM
	streaming *loadedStateStreamingService
	mmClient  *mocks.MockClient
	kv        *kvFake
	c         *Conversations
	wakePost  *model.Post
}

// newWakeEnv builds the env around bot; a nil bot uses the default test bot.
func newWakeEnv(t *testing.T, bot *bots.Bot) *wakeEnv {
	t.Helper()
	env := &wakeEnv{lm: &loadedStateLLM{}, streaming: &loadedStateStreamingService{}}
	env.convStore, env.conv = wakeConversationStore()
	if bot == nil {
		bot = loadedStateBot(env.lm)
	}

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{bot})

	env.mmClient = mocks.NewMockClient(t)
	env.mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
	env.mmClient.On("LogError", mock.Anything, mock.Anything).Maybe().Return()
	env.mmClient.On("LogWarn", mock.Anything, mock.Anything).Maybe().Return()
	env.mmClient.On("GetConfig").Return(&model.Config{}).Maybe()
	env.kv = newKVFake(env.mmClient)

	env.c = &Conversations{
		mmClient:         env.mmClient,
		contextBuilder:   loadedStateBuilder(t),
		bots:             botsService,
		convService:      conversation.NewService(env.convStore, nil, nil, nil),
		streamingService: env.streaming,
	}
	return env
}

// seedTurn adds a user or assistant turn to the conversation.
func (e *wakeEnv) seedTurn(t *testing.T, id, role, text string, postID *string, sequence int, createdAt int64) {
	t.Helper()
	content, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: text}})
	require.NoError(t, err)
	require.NoError(t, e.convStore.CreateTurn(&store.Turn{
		ID: id, ConversationID: e.conv.ID, PostID: postID, Role: role, Content: content, Sequence: sequence, CreatedAt: createdAt,
	}))
}

// expectLiveWake registers the lookups of a wake that reaches the LLM and
// captures the wake reply post.
func (e *wakeEnv) expectLiveWake(post *model.Post, channel *model.Channel, userID string) {
	e.mmClient.On("GetPost", post.Id).Return(post, nil).Once()
	if channel.Type == model.ChannelTypeDirect && post.RootId != "" {
		// The DM analysis-source check looks the thread root up.
		e.mmClient.On("GetPost", post.RootId).Return(&model.Post{Id: post.RootId, ChannelId: channel.Id}, nil).Maybe()
	}
	e.mmClient.On("GetUser", userID).Return(&model.User{Id: userID, Username: "user"}, nil)
	e.mmClient.On("GetChannel", channel.Id).Return(channel, nil).Once()
	e.mmClient.On("HasPermissionToChannel", userID, channel.Id, model.PermissionReadChannel).Return(true).Once()
	e.mmClient.On("CreatePost", mock.Anything).Run(func(args mock.Arguments) {
		e.wakePost = args.Get(0).(*model.Post)
		e.wakePost.Id = "wake-post-id"
	}).Return(nil).Once()
}

// resume fires the wake of post that userID scheduled, with its marker intact,
// and checks the wake consumed the marker.
func (e *wakeEnv) resume(t *testing.T, post *model.Post, userID string) {
	t.Helper()
	job := newWakeJob(post.Id, e.conv.ID, userID, wakeScheduledAt)
	e.setMarker(t, post, job)
	require.NoError(t, e.c.ResumeConversation(context.Background(), job))
	e.streaming.waitForStreaming()
	require.Empty(t, e.kv.data, "a resuming wake must consume its marker")
}

func TestResumeConversationWritesWakeTurnAndFollowsUp(t *testing.T) {
	env := newWakeEnv(t, nil)
	env.seedTurn(t, "user-1", "user", "please watch the job", nil, 1, 0)

	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
	post := &model.Post{Id: "bot-post-id", UserId: "bot-id", ChannelId: channel.Id, RootId: "root-post-id"}
	env.expectLiveWake(post, channel, wakeUserID)

	env.resume(t, post, wakeUserID)

	// The wake streams onto a new bot post in the same thread; the original
	// post's finished response is left untouched.
	require.NotNil(t, env.wakePost)
	require.Equal(t, "root-post-id", env.wakePost.RootId)
	require.Equal(t, "dm-channel", env.wakePost.ChannelId)
	require.Equal(t, "bot-id", env.wakePost.UserId)
	require.Equal(t, env.conv.ID, env.wakePost.GetProp(streaming.ConversationIDProp))

	turns, err := env.convStore.GetTurnsForConversation(env.conv.ID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	require.Equal(t, "user", turns[1].Role)
	require.Nil(t, turns[1].PostID)

	blocks, err := conversation.UnmarshalBlocks(turns[1].Content)
	require.NoError(t, err)
	require.Equal(t, conversation.BlockTypeText, blocks[0].Type)
	require.Equal(t, mmtools.WakeUserMessage, blocks[0].Text)
	require.Equal(t, conversation.SyntheticKindWake, blocks[0].SyntheticKind)

	require.Len(t, env.lm.requests, 1)
	// Nobody is present to answer user-interaction tools on a wake.
	require.False(t, env.lm.requests[0].Context.ToolCatalog.InteractiveUserPresent)
}

func TestResumeConversationInChannelOnlySeesPostsVettedBeforeSleeping(t *testing.T) {
	tests := []struct {
		name     string
		createAt int64
		updateAt int64
		wantPeer bool
	}{
		{name: "post made while waiting is withheld", createAt: 3000, updateAt: 3000},
		{name: "post made before the requester's message is kept", createAt: 900, updateAt: 900, wantPeer: true},
		{name: "earlier post edited while waiting is withheld", createAt: 900, updateAt: 3500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newWakeEnv(t, nil)
			env.c.licenseChecker = enterprisetest.CheckerAt(enterprise.LevelProfessional)
			rootPostID := "mention-post-id"
			env.conv.RootPostID = &rootPostID
			require.NoError(t, env.convStore.CreateConversation(env.conv))
			replyPostID := "bot-post-id"
			env.seedTurn(t, "user-1", "user", "@matty watch the job", &rootPostID, 1, 0)
			env.seedTurn(t, "assistant-1", "assistant", "waiting on the job", &replyPostID, 2, 0)

			peerID := model.NewId()
			channel := &model.Channel{Id: "open-channel", Type: model.ChannelTypeOpen, TeamId: "team-id"}
			mention := &model.Post{Id: rootPostID, UserId: wakeUserID, ChannelId: channel.Id, CreateAt: 1000, UpdateAt: 1000, Message: "@matty watch the job"}
			post := &model.Post{Id: replyPostID, UserId: "bot-id", ChannelId: channel.Id, RootId: rootPostID, CreateAt: 2000, UpdateAt: 2000}
			peer := &model.Post{
				Id: "peer-post-id", UserId: peerID, ChannelId: channel.Id, RootId: rootPostID,
				CreateAt: tt.createAt, UpdateAt: tt.updateAt, Message: "PEER_MESSAGE",
			}
			// The wake's own empty reply post exists in the thread when the request is built.
			wakeReply := &model.Post{Id: "wake-post-id", UserId: "bot-id", ChannelId: channel.Id, RootId: rootPostID, CreateAt: 4000, UpdateAt: 4000}

			ordered := []*model.Post{mention, post, peer, wakeReply}
			slices.SortStableFunc(ordered, func(a, b *model.Post) int { return int(a.CreateAt - b.CreateAt) })
			thread := &model.PostList{Posts: map[string]*model.Post{}}
			for _, p := range ordered {
				thread.Order = append(thread.Order, p.Id)
				thread.Posts[p.Id] = p
			}

			env.expectLiveWake(post, channel, wakeUserID)
			env.mmClient.On("GetPost", rootPostID).Return(mention, nil).Maybe()
			env.mmClient.On("GetUser", peerID).Return(&model.User{Id: peerID, Username: "mallory"}, nil).Maybe()
			env.mmClient.On("GetUser", "bot-id").Return(&model.User{Id: "bot-id", Username: "matty", IsBot: true}, nil).Maybe()
			env.mmClient.On("GetPostThread", rootPostID).Return(thread, nil).Once()

			env.resume(t, post, wakeUserID)

			require.Len(t, env.lm.requests, 1)
			var messages []string
			for _, p := range env.lm.requests[0].Posts[1:] {
				require.NotEmpty(t, p.Message, "the empty wake placeholder must not reach the model")
				messages = append(messages, p.Message)
			}
			require.Equal(t, mmtools.WakeUserMessage, messages[len(messages)-1], "the wake turn is the latest input")
			if tt.wantPeer {
				require.Contains(t, strings.Join(messages, "\n"), "PEER_MESSAGE")
				return
			}
			require.NotContains(t, strings.Join(messages, "\n"), "PEER_MESSAGE", "unattended wakes must not see unvetted thread posts")
		})
	}
}

func TestResumeConversationDrops(t *testing.T) {
	dmChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
	openChannel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeOpen, TeamId: "team-id"}

	tests := []struct {
		name string
		// postMissing makes GetPost fail; jobConvID overrides the job's conversation.
		postMissing bool
		jobConvID   string
		// markerGone means steering consumed the wake's marker before it fired.
		markerGone bool
		// user, when set, means lookups reach the authorization check.
		user       *model.User
		canRead    bool
		channel    *model.Channel
		botCfg     func(*llm.BotConfig)
		priorWakes int
		// archivedAt archives the conversation at that time before the wake
		// fires; the dropped wake must leave it archived.
		archivedAt int64
	}{
		{name: "post missing", postMissing: true},
		{name: "conversation missing", jobConvID: "missing-conv"},
		{name: "wake steered after it fired", markerGone: true},
		{
			name:    "requester deactivated",
			user:    &model.User{Id: wakeUserID, DeleteAt: 1},
			canRead: true,
		},
		{
			name:    "requester lost channel access",
			user:    &model.User{Id: wakeUserID},
			canRead: false,
		},
		{
			name:    "requester no longer allowed to use the agent in DM",
			user:    &model.User{Id: wakeUserID},
			canRead: true,
			botCfg:  func(cfg *llm.BotConfig) { cfg.UserAccessLevel = llm.UserAccessLevelNone },
		},
		{
			name:    "agent no longer allowed in the channel",
			user:    &model.User{Id: wakeUserID},
			canRead: true,
			channel: openChannel,
			botCfg:  func(cfg *llm.BotConfig) { cfg.ChannelAccessLevel = llm.ChannelAccessLevelNone },
		},
		{
			name:       "wake chain limit reached",
			user:       &model.User{Id: wakeUserID},
			canRead:    true,
			priorWakes: maxConsecutiveWakes,
		},
		// An archived conversation drops the wake before any other lookup,
		// so these cases register no user or channel expectations.
		{name: "conversation archived after the wait was scheduled", archivedAt: wakeScheduledAt + 1},
		{name: "conversation archived before the wait was scheduled", archivedAt: wakeScheduledAt - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bot := loadedStateBot(&loadedStateLLM{})
			if tt.botCfg != nil {
				cfg := bot.GetConfig()
				tt.botCfg(&cfg)
				bot = bots.NewBot(cfg, bot.GetService(), bot.GetMMBot(), bot.LLM())
			}
			env := newWakeEnv(t, bot)
			for range tt.priorWakes {
				require.NoError(t, env.c.convService.AppendSyntheticUserTurn(env.conv.ID, conversation.SyntheticKindWake, mmtools.WakeUserMessage))
			}
			channel := dmChannel
			if tt.channel != nil {
				channel = tt.channel
			}
			if tt.archivedAt != 0 {
				env.convStore.conversations[env.conv.ID].DeleteAt = tt.archivedAt
			}

			convID := env.conv.ID
			if tt.jobConvID != "" {
				convID = tt.jobConvID
			}
			job := newWakeJob("bot-post-id", convID, wakeUserID, wakeScheduledAt)

			if tt.postMissing {
				env.mmClient.On("GetPost", "bot-post-id").Return(nil, model.NewAppError("GetPost", "not found", nil, "", 404)).Once()
			} else {
				post := &model.Post{Id: "bot-post-id", UserId: "bot-id", ChannelId: "dm-channel"}
				env.mmClient.On("GetPost", "bot-post-id").Return(post, nil).Once()
				if !tt.markerGone {
					env.setMarker(t, post, job)
				}
			}
			if tt.user != nil {
				env.mmClient.On("GetUser", wakeUserID).Return(tt.user, nil).Once()
				env.mmClient.On("GetChannel", "dm-channel").Return(channel, nil).Once()
				if tt.user.DeleteAt == 0 {
					env.mmClient.On("HasPermissionToChannel", wakeUserID, "dm-channel", model.PermissionReadChannel).Return(tt.canRead).Once()
				}
			}

			require.NoError(t, env.c.ResumeConversation(context.Background(), job))

			turns, err := env.convStore.GetTurnsForConversation(env.conv.ID)
			require.NoError(t, err)
			require.Len(t, turns, tt.priorWakes)
			if tt.archivedAt != 0 {
				require.Equal(t, tt.archivedAt, env.convStore.conversations[env.conv.ID].DeleteAt,
					"a wake must not un-archive the conversation")
				require.Empty(t, env.lm.requests, "a wake on an archived conversation must not run")
			}
		})
	}
}

func TestCountConsecutiveWakes(t *testing.T) {
	turnOfKind := func(role, kind, text, postID string) store.Turn {
		content, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: text, SyntheticKind: kind}})
		require.NoError(t, err)
		turn := store.Turn{ID: model.NewId(), Role: role, Content: content}
		if postID != "" {
			turn.PostID = &postID
		}
		return turn
	}
	turn := func(role, text, postID string) store.Turn { return turnOfKind(role, "", text, postID) }
	wake := func() store.Turn { return turnOfKind("user", conversation.SyntheticKindWake, "any wording", "") }

	tests := []struct {
		name      string
		turns     []store.Turn
		wantWakes int
	}{
		{name: "no turns"},
		{
			name:      "wakes after the requester's post",
			turns:     []store.Turn{turn("user", "watch it", "p1"), wake(), wake()},
			wantWakes: 2,
		},
		{
			name:      "requester posting again resets the chain",
			turns:     []store.Turn{wake(), wake(), turn("user", "still there?", "p2"), wake()},
			wantWakes: 1,
		},
		{
			name:      "other synthetic user turns are not wakes",
			turns:     []store.Turn{turn("user", "watch it", "p1"), turn("user", "something else", "")},
			wantWakes: 0,
		},
		{
			name:      "steering note neither counts as a wake nor breaks the chain",
			turns:     []store.Turn{turn("user", "watch it", "p1"), wake(), turnOfKind("user", conversation.SyntheticKindWakeCanceled, mmtools.WakeCanceledUserMessage, ""), wake()},
			wantWakes: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantWakes, countConsecutiveWakes(tt.turns))
		})
	}
}

type fakeWakeScheduler struct {
	scheduled   map[string]WakeJob
	scheduleErr error
	canceled    []string
}

func (f *fakeWakeScheduler) Cancel(key string) {
	f.canceled = append(f.canceled, key)
	delete(f.scheduled, key)
}

func (f *fakeWakeScheduler) ScheduleOnce(key string, _ time.Time, props any) (*cluster.JobOnce, error) {
	if f.scheduleErr != nil {
		return nil, f.scheduleErr
	}
	if _, exists := f.scheduled[key]; exists {
		return nil, errors.New("could not save job metadata: failed to set data")
	}
	f.scheduled[key] = props.(WakeJob)
	return nil, nil
}

func TestScheduleWakeAllowsOnePendingWaitPerThread(t *testing.T) {
	// postIn is the bot's response post of convID in the thread at rootID.
	postIn := func(id, convID, rootID string) *model.Post {
		post := &model.Post{Id: id, UserId: "bot-id", RootId: rootID}
		post.AddProp(streaming.ConversationIDProp, convID)
		return post
	}

	tests := []struct {
		name  string
		posts map[string]*model.Post
		calls []string
		sched *fakeWakeScheduler
		kvErr error
		// wantErrs[i] is nil, mmtools.ErrWaitAlreadyScheduled, or any other
		// error (matched as "an error that is not already-scheduled").
		wantErrs []error
		// wantJobs are the conversations with a pending job; each must hold the
		// marker of its thread.
		wantJobs    []string
		wantMarkers int
	}{
		{
			name:        "first wait claims the thread marker and schedules under the conversation key",
			posts:       map[string]*model.Post{"p1": postIn("p1", "conv-a", "root-1")},
			calls:       []string{"p1"},
			wantErrs:    []error{nil},
			wantJobs:    []string{"conv-a"},
			wantMarkers: 1,
		},
		{
			name: "second wait in the same conversation is rejected",
			posts: map[string]*model.Post{
				"p1": postIn("p1", "conv-a", "root-1"),
				"p2": postIn("p2", "conv-a", "root-1"),
			},
			calls:       []string{"p1", "p2"},
			wantErrs:    []error{nil, mmtools.ErrWaitAlreadyScheduled},
			wantJobs:    []string{"conv-a"},
			wantMarkers: 1,
		},
		{
			name: "concurrent wait from another conversation in the thread is rejected",
			posts: map[string]*model.Post{
				"p1": postIn("p1", "conv-a", "root-1"),
				"p2": postIn("p2", "conv-b", "root-1"),
			},
			calls:       []string{"p1", "p2"},
			wantErrs:    []error{nil, mmtools.ErrWaitAlreadyScheduled},
			wantJobs:    []string{"conv-a"},
			wantMarkers: 1,
		},
		{
			name: "other threads are independent",
			posts: map[string]*model.Post{
				"p1": postIn("p1", "conv-a", "root-1"),
				"p2": postIn("p2", "conv-b", "root-2"),
			},
			calls:       []string{"p1", "p2"},
			wantErrs:    []error{nil, nil},
			wantJobs:    []string{"conv-a", "conv-b"},
			wantMarkers: 2,
		},
		{
			name:     "marker claim failure leaves no job and no marker",
			posts:    map[string]*model.Post{"p1": postIn("p1", "conv-a", "root-1")},
			calls:    []string{"p1"},
			kvErr:    errors.New("kv down"),
			wantErrs: []error{errors.New("kv down")},
		},
		{
			name:     "scheduler failure releases the marker and is not reported as already pending",
			posts:    map[string]*model.Post{"p1": postIn("p1", "conv-a", "root-1")},
			calls:    []string{"p1"},
			sched:    &fakeWakeScheduler{scheduleErr: errors.New("scheduler down")},
			wantErrs: []error{errors.New("scheduler down")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogWarn", mock.Anything, mock.Anything).Maybe().Return()
			kv := newKVFake(mmClient)
			kv.setFail = tt.kvErr
			for id, post := range tt.posts {
				mmClient.On("GetPost", id).Return(post, nil)
			}
			sched := tt.sched
			if sched == nil {
				sched = &fakeWakeScheduler{}
			}
			if sched.scheduled == nil {
				sched.scheduled = map[string]WakeJob{}
			}
			c := &Conversations{mmClient: mmClient, wakeScheduler: sched}

			for i, postID := range tt.calls {
				err := c.ScheduleWake(postID, "user-id", time.Minute)
				switch want := tt.wantErrs[i]; {
				case want == nil:
					require.NoError(t, err)
				case errors.Is(want, mmtools.ErrWaitAlreadyScheduled):
					require.ErrorIs(t, err, mmtools.ErrWaitAlreadyScheduled)
				default:
					require.Error(t, err)
					require.NotErrorIs(t, err, mmtools.ErrWaitAlreadyScheduled)
				}
			}

			var jobConvs []string
			for key, job := range sched.scheduled {
				require.Equal(t, WakeJobKeyPrefix+job.ConversationID, key)
				require.Equal(t, "user-id", job.UserID)
				require.NotZero(t, job.ScheduledAt)
				var marker wakeMarker
				require.NoError(t, json.Unmarshal(kv.data[responsePostMarkerKey(tt.posts[job.PostID])], &marker))
				require.Equal(t, job.wakeMarker, marker, "a pending job must own its thread's marker or its wake would be dropped")
				jobConvs = append(jobConvs, job.ConversationID)
			}
			require.ElementsMatch(t, tt.wantJobs, jobConvs)
			require.Len(t, kv.data, tt.wantMarkers)
		})
	}
}

func TestRejectedConcurrentWaitDoesNotDropTheFirstWake(t *testing.T) {
	env := newWakeEnv(t, nil)
	env.seedTurn(t, "user-1", "user", "please watch the job", nil, 1, 0)
	sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
	env.c.wakeScheduler = sched

	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
	first := &model.Post{Id: "bot-post-id", UserId: "bot-id", ChannelId: channel.Id, RootId: "root-post-id"}
	first.AddProp(streaming.ConversationIDProp, env.conv.ID)
	second := &model.Post{Id: "other-bot-post-id", UserId: "bot-id", ChannelId: channel.Id, RootId: "root-post-id"}
	second.AddProp(streaming.ConversationIDProp, "other-conv-id")

	env.mmClient.On("GetPost", first.Id).Return(first, nil).Once()
	env.mmClient.On("GetPost", second.Id).Return(second, nil).Once()
	env.expectLiveWake(first, channel, wakeUserID)

	require.NoError(t, env.c.ScheduleWake(first.Id, wakeUserID, time.Minute))
	require.ErrorIs(t, env.c.ScheduleWake(second.Id, model.NewId(), time.Minute), mmtools.ErrWaitAlreadyScheduled)

	job := sched.scheduled[WakeJobKeyPrefix+env.conv.ID]
	require.NoError(t, env.c.ResumeConversation(context.Background(), job))
	env.streaming.waitForStreaming()

	require.NotNil(t, env.wakePost, "the first wake must still resume the conversation")
	require.Empty(t, env.kv.data)
}

func TestResumeConversationRunsAsSchedulingUser(t *testing.T) {
	env := newWakeEnv(t, nil)
	env.seedTurn(t, "user-1", "user", "please watch the job", nil, 1, 0)

	// The conversation belongs to wakeUserID, but another participant's message
	// steered it and scheduled this wait.
	schedulerID := model.NewId()
	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}
	post := &model.Post{Id: "bot-post-id", UserId: "bot-id", ChannelId: channel.Id, RootId: "root-post-id"}
	env.expectLiveWake(post, channel, schedulerID)

	env.resume(t, post, schedulerID)

	require.NotNil(t, env.wakePost)
	require.Equal(t, schedulerID, env.wakePost.GetProp(streaming.LLMRequesterUserIDProp))
	require.Len(t, env.lm.requests, 1)
	require.Equal(t, schedulerID, env.lm.requests[0].Context.RequestingUser.Id)
}

func TestNewMessageSteersThreadWake(t *testing.T) {
	const rootPostID = "root-post-id"
	otherUserID := model.NewId()

	tests := []struct {
		name string
		// invokerID is the user whose message arrives; pending says whether the
		// requester (wakeUserID) has a wait scheduled in the thread.
		invokerID string
		pending   bool
	}{
		{name: "same user's follow-up steers their own wait", invokerID: wakeUserID, pending: true},
		{name: "another user's first mention steers the requester's wait", invokerID: otherUserID, pending: true},
		{name: "no pending wait leaves the thread alone", invokerID: otherUserID, pending: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newWakeEnv(t, nil)
			env.conv.RootPostID = new(rootPostID)
			require.NoError(t, env.convStore.CreateConversation(env.conv))

			sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
			env.c.wakeScheduler = sched
			requesterPost := &model.Post{Id: "wait-post-id", UserId: "bot-id", RootId: rootPostID}
			if tt.pending {
				job := newWakeJob(requesterPost.Id, env.conv.ID, wakeUserID, wakeScheduledAt)
				sched.scheduled[WakeJobKeyPrefix+env.conv.ID] = job
				env.setMarker(t, requesterPost, job)
			}

			result, err := env.c.getOrCreateConversation(conversation.GetOrCreateParams{
				UserID:      tt.invokerID,
				BotID:       "bot-id",
				RootPostID:  rootPostID,
				Operation:   "conversation",
				UserMessage: "actually, do something else",
				UserPostID:  new("new-post-id"),
			})
			require.NoError(t, err)

			// The invoker's conversation gets the note after the new message;
			// the requester's conversation is never touched on their behalf.
			invokerTurns, err := env.convStore.GetTurnsForConversation(result.Conversation.ID)
			require.NoError(t, err)
			require.NotNil(t, invokerTurns[0].PostID)
			if !tt.pending {
				require.Len(t, invokerTurns, 1)
				require.Empty(t, sched.canceled)
				return
			}
			require.Len(t, invokerTurns, 2)
			require.Nil(t, invokerTurns[1].PostID)
			blocks, err := conversation.UnmarshalBlocks(invokerTurns[1].Content)
			require.NoError(t, err)
			require.Equal(t, mmtools.WakeCanceledUserMessage, blocks[0].Text)
			require.Equal(t, conversation.SyntheticKindWakeCanceled, blocks[0].SyntheticKind)
			if result.Conversation.ID != env.conv.ID {
				requesterTurns, turnsErr := env.convStore.GetTurnsForConversation(env.conv.ID)
				require.NoError(t, turnsErr)
				require.Empty(t, requesterTurns)
			}
			require.Equal(t, []string{WakeJobKeyPrefix + env.conv.ID}, sched.canceled)
			require.Empty(t, env.kv.data)

			// The steered turn can wait again; the new wake belongs to the
			// invoker's conversation and runs as the invoker.
			invokerPost := &model.Post{Id: "invoker-post-id", UserId: "bot-id", RootId: rootPostID}
			invokerPost.AddProp(streaming.ConversationIDProp, result.Conversation.ID)
			env.mmClient.On("GetPost", invokerPost.Id).Return(invokerPost, nil)
			require.NoError(t, env.c.ScheduleWake(invokerPost.Id, tt.invokerID, time.Minute))
			job := sched.scheduled[WakeJobKeyPrefix+result.Conversation.ID]
			require.Equal(t, tt.invokerID, job.UserID)
			var marker wakeMarker
			require.NoError(t, json.Unmarshal(env.kv.data[responsePostMarkerKey(invokerPost)], &marker))
			require.Equal(t, job.wakeMarker, marker)
		})
	}
}

func TestRegenerationCancelsThreadWakeWithoutNote(t *testing.T) {
	convStore, conv := loadedStateConversationStore()
	for i, turn := range []struct{ role, text, postID string }{
		{"user", "watch the job", ""},
		{"assistant", "waiting", "response-post-id"},
	} {
		content, err := json.Marshal([]conversation.ContentBlock{{Type: conversation.BlockTypeText, Text: turn.text}})
		require.NoError(t, err)
		st := &store.Turn{ID: model.NewId(), ConversationID: conv.ID, Role: turn.role, Content: content, Sequence: i + 1}
		if turn.postID != "" {
			st.PostID = new(turn.postID)
		}
		require.NoError(t, convStore.CreateTurn(st))
	}

	mmClient := mocks.NewMockClient(t)
	kv := newKVFake(mmClient)
	mmClient.On("GetPost", "root-post-id").Return(&model.Post{Id: "root-post-id"}, nil).Maybe()
	sched := &fakeWakeScheduler{scheduled: map[string]WakeJob{}}
	post := &model.Post{Id: "response-post-id", UserId: "bot-id", RootId: "root-post-id"}
	post.AddProp(streaming.ConversationIDProp, conv.ID)
	job := newWakeJob(post.Id, "other-conv", "user-id", wakeScheduledAt)
	sched.scheduled[WakeJobKeyPrefix+job.ConversationID] = job
	raw, err := json.Marshal(job.wakeMarker)
	require.NoError(t, err)
	kv.data[responsePostMarkerKey(post)] = raw

	bot := loadedStateBot(&loadedStateLLM{})
	c := &Conversations{
		mmClient:       mmClient,
		contextBuilder: loadedStateBuilder(t),
		bots:           passthroughBotsService(bot),
		convService:    conversation.NewService(convStore, nil, nil, nil),
		configProvider: &channelFollowUpTestConfig{},
		wakeScheduler:  sched,
	}
	policy, err := c.resolveTurnPolicy(context.Background(), turnRequest{
		bot:     bot,
		user:    &model.User{Id: wakeUserID, Username: "user"},
		channel: &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__" + wakeUserID},
		conv:    conv,
		post:    post,
	})
	require.NoError(t, err)
	streamResult, err := c.regenerateViaConversation(context.Background(), policy, post)
	require.NoError(t, err)
	_, err = streamResult.ReadAll()
	require.NoError(t, err)

	require.Equal(t, []string{WakeJobKeyPrefix + "other-conv"}, sched.canceled)
	require.Empty(t, kv.data)
	turns, err := convStore.GetTurnsForConversation(conv.ID)
	require.NoError(t, err)
	for _, turn := range turns {
		blocks, blocksErr := conversation.UnmarshalBlocks(turn.Content)
		require.NoError(t, blocksErr)
		for _, block := range blocks {
			require.NotEqual(t, mmtools.WakeCanceledUserMessage, block.Text)
		}
	}
}
