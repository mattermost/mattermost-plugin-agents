// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package autoreply

import (
	"errors"
	"net/http"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// recordingNotifier records broadcast channel IDs. It is mutex-guarded so the
// concurrency test can exercise Set/Delete from multiple goroutines.
type recordingNotifier struct {
	mu        sync.Mutex
	published []string
	err       error
}

func (n *recordingNotifier) PublishChannelAutoReplyInvalidate(channelID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.published = append(n.published, channelID)
	return n.err
}

func (n *recordingNotifier) publishedIDs() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.published)
}

// newTestService builds a Service backed by a real store and a real MMBots
// instance holding botList.
func newTestService(t *testing.T, dbClient *mmapi.DBClient, notifier ClusterNotifier, mmClient mmapi.Client, botList []*bots.Bot) *Service {
	t.Helper()

	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	botsService := bots.New(mockAPI, client, enterprise.NewLicenseChecker(client), nil, nil, &http.Client{}, nil)
	botsService.SetBotsForTesting(botList)

	return NewService(NewStore(dbClient), botsService, mmClient, notifier)
}

// newTestBot builds a real bots.Bot with the given Mattermost bot user ID.
func newTestBot(botID string, cfg llm.BotConfig) *bots.Bot {
	return bots.NewBot(cfg, llm.ServiceConfig{}, &model.Bot{UserId: botID}, nil)
}

func TestServiceSetValidation(t *testing.T) {
	dbClient := testDB(t)

	allowAllCfg := llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAll}

	tests := []struct {
		name string
		// mode used for the Set call.
		mode Mode
		// blank out the channel/bot ID passed to Set.
		emptyChannelID bool
		emptyBotID     bool
		// register the bot with the passed bot ID; false simulates an
		// unknown bot ID.
		registerBot bool
		// bot config; may reference the channel via the callback.
		botCfg func(channelID string) llm.BotConfig
		// channel returned by GetChannel (unless getChannelErr is set).
		channelType   model.ChannelType
		getChannelErr error
		// true: errors.Is(err, ErrValidation). false: an error that is
		// NOT ErrValidation (infrastructure failure).
		wantValidation bool
	}{
		{
			name:           "unknown mode string",
			mode:           Mode("banana"),
			registerBot:    true,
			botCfg:         func(string) llm.BotConfig { return allowAllCfg },
			channelType:    model.ChannelTypeOpen,
			wantValidation: true,
		},
		{
			name:           "mode off is not storable",
			mode:           ModeOff,
			registerBot:    true,
			botCfg:         func(string) llm.BotConfig { return allowAllCfg },
			channelType:    model.ChannelTypeOpen,
			wantValidation: true,
		},
		{
			name:           "empty channel ID",
			mode:           ModeRootPosts,
			emptyChannelID: true,
			registerBot:    true,
			botCfg:         func(string) llm.BotConfig { return allowAllCfg },
			channelType:    model.ChannelTypeOpen,
			wantValidation: true,
		},
		{
			name:           "empty bot ID",
			mode:           ModeRootPosts,
			emptyBotID:     true,
			registerBot:    true,
			botCfg:         func(string) llm.BotConfig { return allowAllCfg },
			channelType:    model.ChannelTypeOpen,
			wantValidation: true,
		},
		{
			name:           "bot ID not in bots list",
			mode:           ModeRootPosts,
			registerBot:    false,
			botCfg:         func(string) llm.BotConfig { return allowAllCfg },
			channelType:    model.ChannelTypeOpen,
			wantValidation: true,
		},
		{
			name:           "direct channel rejected",
			mode:           ModeRootPosts,
			registerBot:    true,
			botCfg:         func(string) llm.BotConfig { return allowAllCfg },
			channelType:    model.ChannelTypeDirect,
			wantValidation: true,
		},
		{
			name:           "group channel rejected",
			mode:           ModeRootPosts,
			registerBot:    true,
			botCfg:         func(string) llm.BotConfig { return allowAllCfg },
			channelType:    model.ChannelTypeGroup,
			wantValidation: true,
		},
		{
			name:        "bot allow list does not contain channel",
			mode:        ModeRootPosts,
			registerBot: true,
			botCfg: func(string) llm.BotConfig {
				return llm.BotConfig{
					ChannelAccessLevel: llm.ChannelAccessLevelAllow,
					ChannelIDs:         []string{model.NewId()},
				}
			},
			channelType:    model.ChannelTypeOpen,
			wantValidation: true,
		},
		{
			name:        "bot block list contains channel",
			mode:        ModeRootPosts,
			registerBot: true,
			botCfg: func(channelID string) llm.BotConfig {
				return llm.BotConfig{
					ChannelAccessLevel: llm.ChannelAccessLevelBlock,
					ChannelIDs:         []string{channelID},
				}
			},
			channelType:    model.ChannelTypeOpen,
			wantValidation: true,
		},
		{
			name:        "bot channel access level none",
			mode:        ModeRootPosts,
			registerBot: true,
			botCfg: func(string) llm.BotConfig {
				return llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelNone}
			},
			channelType:    model.ChannelTypeOpen,
			wantValidation: true,
		},
		{
			name:           "channel lookup failure is not a validation error",
			mode:           ModeRootPosts,
			registerBot:    true,
			botCfg:         func(string) llm.BotConfig { return allowAllCfg },
			getChannelErr:  errors.New("boom"),
			wantValidation: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			channelID := model.NewId()
			if tc.emptyChannelID {
				channelID = ""
			}
			botID := model.NewId()
			if tc.emptyBotID {
				botID = ""
			}

			mmClient := mocks.NewMockClient(t)
			if tc.getChannelErr != nil {
				mmClient.EXPECT().GetChannel(mock.Anything).Return(nil, tc.getChannelErr).Maybe()
			} else {
				mmClient.EXPECT().GetChannel(mock.Anything).RunAndReturn(func(id string) (*model.Channel, error) {
					return &model.Channel{Id: id, Type: tc.channelType}, nil
				}).Maybe()
			}

			var botList []*bots.Bot
			if tc.registerBot && botID != "" {
				botList = append(botList, newTestBot(botID, tc.botCfg(channelID)))
			}

			notifier := &recordingNotifier{}
			svc := newTestService(t, dbClient, notifier, mmClient, botList)

			setting, err := svc.Set(channelID, botID, tc.mode, model.NewId())
			require.Error(t, err)
			require.Nil(t, setting)
			if tc.wantValidation {
				require.ErrorIs(t, err, ErrValidation)
			} else {
				require.NotErrorIs(t, err, ErrValidation)
			}

			// Nothing was persisted or cached, and peers were not notified.
			stored, getErr := NewStore(dbClient).Get(channelID)
			require.NoError(t, getErr)
			require.Nil(t, stored)
			_, ok := svc.GetCached(channelID)
			require.False(t, ok)
			require.Empty(t, notifier.publishedIDs())
		})
	}
}

func TestServiceSetSuccess(t *testing.T) {
	dbClient := testDB(t)

	tests := []struct {
		name        string
		mode        Mode
		channelType model.ChannelType
		botCfg      func(channelID string) llm.BotConfig
	}{
		{
			name:        "open channel with access-all bot and root posts mode",
			mode:        ModeRootPosts,
			channelType: model.ChannelTypeOpen,
			botCfg: func(string) llm.BotConfig {
				return llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAll}
			},
		},
		{
			name:        "private channel with allow-listed bot and threads mode",
			mode:        ModeThreads,
			channelType: model.ChannelTypePrivate,
			botCfg: func(channelID string) llm.BotConfig {
				return llm.BotConfig{
					ChannelAccessLevel: llm.ChannelAccessLevelAllow,
					ChannelIDs:         []string{channelID},
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			channelID := model.NewId()
			botID := model.NewId()
			updatedBy := model.NewId()

			mmClient := mocks.NewMockClient(t)
			mmClient.EXPECT().GetChannel(channelID).Return(&model.Channel{Id: channelID, Type: tc.channelType}, nil).Once()

			notifier := &recordingNotifier{}
			svc := newTestService(t, dbClient, notifier, mmClient, []*bots.Bot{newTestBot(botID, tc.botCfg(channelID))})

			setting, err := svc.Set(channelID, botID, tc.mode, updatedBy)
			require.NoError(t, err)
			require.NotNil(t, setting)
			require.Equal(t, channelID, setting.ChannelID)
			require.Equal(t, botID, setting.BotID)
			require.Equal(t, tc.mode, setting.Mode)
			require.Equal(t, updatedBy, setting.UpdatedBy)
			require.Greater(t, setting.UpdateAt, int64(0))

			stored, err := NewStore(dbClient).Get(channelID)
			require.NoError(t, err)
			require.NotNil(t, stored)
			require.Equal(t, *setting, *stored)

			cached, ok := svc.GetCached(channelID)
			require.True(t, ok)
			require.Equal(t, *setting, cached)

			require.Equal(t, []string{channelID}, notifier.publishedIDs())
		})
	}
}

func TestServiceSetOverwrite(t *testing.T) {
	dbClient := testDB(t)

	channelID := model.NewId()
	botA := model.NewId()
	botB := model.NewId()

	mmClient := mocks.NewMockClient(t)
	mmClient.EXPECT().GetChannel(channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil).Twice()

	notifier := &recordingNotifier{}
	allowAll := llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAll}
	svc := newTestService(t, dbClient, notifier, mmClient, []*bots.Bot{
		newTestBot(botA, allowAll),
		newTestBot(botB, allowAll),
	})

	_, err := svc.Set(channelID, botA, ModeRootPosts, model.NewId())
	require.NoError(t, err)

	second, err := svc.Set(channelID, botB, ModeThreads, model.NewId())
	require.NoError(t, err)

	stored, err := svc.Get(channelID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, botB, stored.BotID)
	require.Equal(t, ModeThreads, stored.Mode)
	require.Equal(t, *second, *stored)

	cached, ok := svc.GetCached(channelID)
	require.True(t, ok)
	require.Equal(t, *second, cached)

	require.Equal(t, []string{channelID, channelID}, notifier.publishedIDs())
}

func TestServiceDelete(t *testing.T) {
	dbClient := testDB(t)

	tests := []struct {
		name           string
		emptyChannelID bool
		setFirst       bool
		wantValidation bool
	}{
		{
			name:     "delete after set removes row and cache entry",
			setFirst: true,
		},
		{
			name: "delete of a never-set channel succeeds and still notifies",
		},
		{
			name:           "empty channel ID is a validation error",
			emptyChannelID: true,
			wantValidation: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			channelID := model.NewId()
			if tc.emptyChannelID {
				channelID = ""
			}
			botID := model.NewId()

			mmClient := mocks.NewMockClient(t)
			mmClient.EXPECT().GetChannel(mock.Anything).RunAndReturn(func(id string) (*model.Channel, error) {
				return &model.Channel{Id: id, Type: model.ChannelTypeOpen}, nil
			}).Maybe()

			notifier := &recordingNotifier{}
			bot := newTestBot(botID, llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAll})
			svc := newTestService(t, dbClient, notifier, mmClient, []*bots.Bot{bot})

			expectedNotifications := []string(nil)
			if tc.setFirst {
				_, err := svc.Set(channelID, botID, ModeRootPosts, model.NewId())
				require.NoError(t, err)
				expectedNotifications = append(expectedNotifications, channelID)
			}

			err := svc.Delete(channelID)
			if tc.wantValidation {
				require.ErrorIs(t, err, ErrValidation)
				require.Empty(t, notifier.publishedIDs(), "a rejected delete must not notify peers")
				return
			}
			require.NoError(t, err)
			expectedNotifications = append(expectedNotifications, channelID)

			stored, err := svc.Get(channelID)
			require.NoError(t, err)
			require.Nil(t, stored)

			_, ok := svc.GetCached(channelID)
			require.False(t, ok)

			require.Equal(t, expectedNotifications, notifier.publishedIDs(),
				"delete must notify peers even when no row existed (idempotent convergence)")
		})
	}
}

func TestServiceGet(t *testing.T) {
	dbClient := testDB(t)

	channelID := model.NewId()
	botID := model.NewId()

	mmClient := mocks.NewMockClient(t)
	mmClient.EXPECT().GetChannel(channelID).Return(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil).Once()

	bot := newTestBot(botID, llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAll})
	svc := newTestService(t, dbClient, &recordingNotifier{}, mmClient, []*bots.Bot{bot})

	setting, err := svc.Set(channelID, botID, ModeThreads, model.NewId())
	require.NoError(t, err)

	got, err := svc.Get(channelID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, *setting, *got)

	absent, err := svc.Get(model.NewId())
	require.NoError(t, err)
	require.Nil(t, absent)
}

func TestServiceLoadCache(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	first := Setting{ChannelID: model.NewId(), BotID: model.NewId(), Mode: ModeRootPosts, UpdatedBy: model.NewId(), UpdateAt: 100}
	second := Setting{ChannelID: model.NewId(), BotID: model.NewId(), Mode: ModeThreads, UpdatedBy: model.NewId(), UpdateAt: 200}
	require.NoError(t, store.Set(first))
	require.NoError(t, store.Set(second))

	svc := newTestService(t, dbClient, &recordingNotifier{}, mocks.NewMockClient(t), nil)
	require.NoError(t, svc.LoadCache())

	cachedFirst, ok := svc.GetCached(first.ChannelID)
	require.True(t, ok)
	require.Equal(t, first, cachedFirst)

	cachedSecond, ok := svc.GetCached(second.ChannelID)
	require.True(t, ok)
	require.Equal(t, second, cachedSecond)

	_, ok = svc.GetCached(model.NewId())
	require.False(t, ok)
}

// TestServiceGetCachedDoesNotHitDB pins the hot-path contract as behavior:
// after LoadCache, cached reads must keep working even when the database is
// gone.
func TestServiceGetCachedDoesNotHitDB(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	setting := Setting{ChannelID: model.NewId(), BotID: model.NewId(), Mode: ModeThreads, UpdatedBy: model.NewId(), UpdateAt: 100}
	require.NoError(t, store.Set(setting))

	svc := newTestService(t, dbClient, &recordingNotifier{}, mocks.NewMockClient(t), nil)
	require.NoError(t, svc.LoadCache())

	require.NoError(t, dbClient.Close())

	cached, ok := svc.GetCached(setting.ChannelID)
	require.True(t, ok)
	require.Equal(t, setting, cached)

	_, ok = svc.GetCached(model.NewId())
	require.False(t, ok)
}

func TestServiceRefreshChannel(t *testing.T) {
	dbClient := testDB(t)

	tests := []struct {
		name string
		// mutate the DB behind the service's back, simulating a write on a
		// peer node.
		mutate     func(t *testing.T, store *Store, channelID string) *Setting
		expectMiss bool
	}{
		{
			name: "row changed in the database is picked up",
			mutate: func(t *testing.T, store *Store, channelID string) *Setting {
				updated := Setting{ChannelID: channelID, BotID: model.NewId(), Mode: ModeThreads, UpdatedBy: model.NewId(), UpdateAt: 999}
				require.NoError(t, store.Set(updated))
				return &updated
			},
		},
		{
			name: "row deleted in the database evicts the cache entry",
			mutate: func(t *testing.T, store *Store, channelID string) *Setting {
				require.NoError(t, store.Delete(channelID))
				return nil
			},
			expectMiss: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(dbClient)
			channelID := model.NewId()
			initial := Setting{ChannelID: channelID, BotID: model.NewId(), Mode: ModeRootPosts, UpdatedBy: model.NewId(), UpdateAt: 100}
			require.NoError(t, store.Set(initial))

			svc := newTestService(t, dbClient, &recordingNotifier{}, mocks.NewMockClient(t), nil)
			require.NoError(t, svc.LoadCache())

			expected := tc.mutate(t, store, channelID)

			require.NoError(t, svc.RefreshChannel(channelID))

			cached, ok := svc.GetCached(channelID)
			if tc.expectMiss {
				require.False(t, ok)
			} else {
				require.True(t, ok)
				require.Equal(t, *expected, cached)
			}
		})
	}
}

func TestServiceNotifierFailureDoesNotFailWrite(t *testing.T) {
	dbClient := testDB(t)

	tests := []struct {
		name string
		op   func(t *testing.T, svc *Service, channelID, botID string)
		// expected DB/cache state after the operation.
		expectStored bool
	}{
		{
			name: "set succeeds when the notifier fails",
			op: func(t *testing.T, svc *Service, channelID, botID string) {
				setting, err := svc.Set(channelID, botID, ModeRootPosts, model.NewId())
				require.NoError(t, err, "a failed broadcast must not fail the write")
				require.NotNil(t, setting)
			},
			expectStored: true,
		},
		{
			name: "delete succeeds when the notifier fails",
			op: func(t *testing.T, svc *Service, channelID, botID string) {
				require.NoError(t, svc.store.Set(Setting{
					ChannelID: channelID, BotID: botID, Mode: ModeThreads, UpdatedBy: model.NewId(), UpdateAt: 100,
				}))
				require.NoError(t, svc.LoadCache())
				require.NoError(t, svc.Delete(channelID), "a failed broadcast must not fail the delete")
			},
			expectStored: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			channelID := model.NewId()
			botID := model.NewId()

			mmClient := mocks.NewMockClient(t)
			mmClient.EXPECT().GetChannel(mock.Anything).RunAndReturn(func(id string) (*model.Channel, error) {
				return &model.Channel{Id: id, Type: model.ChannelTypeOpen}, nil
			}).Maybe()
			// The failed broadcast is surfaced as a warning log.
			mmClient.EXPECT().LogWarn(mock.Anything, mock.Anything).Once()

			notifier := &recordingNotifier{err: errors.New("cluster down")}
			bot := newTestBot(botID, llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAll})
			svc := newTestService(t, dbClient, notifier, mmClient, []*bots.Bot{bot})

			tc.op(t, svc, channelID, botID)

			stored, err := svc.Get(channelID)
			require.NoError(t, err)
			cached, ok := svc.GetCached(channelID)
			if tc.expectStored {
				require.NotNil(t, stored, "the DB row must be durable despite the failed broadcast")
				require.True(t, ok)
				require.Equal(t, *stored, cached)
			} else {
				require.Nil(t, stored)
				require.False(t, ok)
			}
		})
	}
}

// TestServiceStalledDBWriteDoesNotBlockGetCached pins the split-lock model:
// a writer stuck inside its DB round-trip must not block the GetCached hot
// path. The stall is real: an uncommitted conflicting row makes Set's upsert
// wait on the Postgres row lock until the blocking transaction ends. Under a
// locking model that holds the cache lock across the DB call, the GetCached
// goroutine below blocks until the rollback and the test fails.
func TestServiceStalledDBWriteDoesNotBlockGetCached(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	// Seed one cached channel for the reader to hit while the writer stalls.
	cachedSetting := Setting{ChannelID: model.NewId(), BotID: model.NewId(), Mode: ModeThreads, UpdatedBy: model.NewId(), UpdateAt: 100}
	require.NoError(t, store.Set(cachedSetting))

	stalledChannelID := model.NewId()
	botID := model.NewId()

	mmClient := mocks.NewMockClient(t)
	mmClient.EXPECT().GetChannel(stalledChannelID).Return(&model.Channel{Id: stalledChannelID, Type: model.ChannelTypeOpen}, nil).Once()

	bot := newTestBot(botID, llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAll})
	svc := newTestService(t, dbClient, &recordingNotifier{}, mmClient, []*bots.Bot{bot})
	require.NoError(t, svc.LoadCache())

	// Uncommitted insert for the same channel ID: Set's upsert now blocks on
	// the row lock until this transaction ends.
	tx, err := dbClient.Beginx()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Exec(
		`INSERT INTO Agents_ChannelAutoReply (ChannelID, BotID, Mode, UpdatedBy, UpdateAt) VALUES ($1, $2, $3, $4, $5)`,
		stalledChannelID, botID, string(ModeRootPosts), "blocker", int64(1))
	require.NoError(t, err)

	setDone := make(chan error, 1)
	go func() {
		_, setErr := svc.Set(stalledChannelID, botID, ModeThreads, model.NewId())
		setDone <- setErr
	}()

	// Wait until Postgres reports the upsert waiting on the row lock so the
	// read below provably runs while the write is stalled mid-DB-call.
	require.Eventually(t, func() bool {
		var waiting int
		qErr := dbClient.Get(&waiting,
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND state = 'active' AND query ILIKE 'INSERT INTO Agents_ChannelAutoReply%'`)
		return qErr == nil && waiting > 0
	}, 10*time.Second, 20*time.Millisecond, "Set never blocked on the row lock")

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		cached, ok := svc.GetCached(cachedSetting.ChannelID)
		assert.True(t, ok)
		assert.Equal(t, cachedSetting, cached)
	}()

	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("GetCached blocked behind a stalled DB write; readers must never wait on the database")
	}

	// Unblock the writer and confirm the write completes and converges.
	require.NoError(t, tx.Rollback())
	require.NoError(t, <-setDone)

	stored, err := svc.Get(stalledChannelID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, ModeThreads, stored.Mode)
	cached, ok := svc.GetCached(stalledChannelID)
	require.True(t, ok)
	require.Equal(t, *stored, cached)
}

// TestServiceConcurrentAccess is a smoke test for the locking model: cached
// reads run concurrently with writes and refreshes. The race detector (go
// test -race) is the point; the final assertion pins the invariant that the
// local cache converges to the database state once writes quiesce.
func TestServiceConcurrentAccess(t *testing.T) {
	dbClient := testDB(t)

	mmClient := mocks.NewMockClient(t)
	mmClient.EXPECT().GetChannel(mock.Anything).RunAndReturn(func(id string) (*model.Channel, error) {
		return &model.Channel{Id: id, Type: model.ChannelTypeOpen}, nil
	}).Maybe()

	botID := model.NewId()
	bot := newTestBot(botID, llm.BotConfig{ChannelAccessLevel: llm.ChannelAccessLevelAll})
	notifier := &recordingNotifier{}
	svc := newTestService(t, dbClient, notifier, mmClient, []*bots.Bot{bot})

	channelIDs := []string{model.NewId(), model.NewId(), model.NewId(), model.NewId()}
	updatedBy := model.NewId()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 50; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, channelID := range channelIDs {
					svc.GetCached(channelID)
				}
				// Yield so writer goroutines make progress; under the race
				// detector 50 spinning readers otherwise starve the writers.
				runtime.Gosched()
			}
		}()
	}

	var writers sync.WaitGroup
	for w := 0; w < 8; w++ {
		writers.Add(1)
		go func(seed int) {
			defer writers.Done()
			for i := 0; i < 25; i++ {
				channelID := channelIDs[(seed+i)%len(channelIDs)]
				switch i % 4 {
				case 0:
					_, err := svc.Set(channelID, botID, ModeRootPosts, updatedBy)
					assert.NoError(t, err)
				case 1:
					_, err := svc.Set(channelID, botID, ModeThreads, updatedBy)
					assert.NoError(t, err)
				case 2:
					assert.NoError(t, svc.Delete(channelID))
				case 3:
					assert.NoError(t, svc.RefreshChannel(channelID))
				}
			}
		}(w)
	}

	writers.Wait()
	close(stop)
	readers.Wait()

	// Once writes quiesce the cache must match the database for every
	// channel — writers are fully serialized by writeMu across the DB write
	// and the cache mutation, so they cannot diverge.
	for _, channelID := range channelIDs {
		stored, err := svc.Get(channelID)
		require.NoError(t, err)
		cached, ok := svc.GetCached(channelID)
		if stored == nil {
			require.False(t, ok, "channel %s: cache has an entry but the DB row is gone", channelID)
		} else {
			require.True(t, ok, "channel %s: DB has a row but the cache is empty", channelID)
			require.Equal(t, *stored, cached, "channel %s: cache diverged from the DB", channelID)
		}
	}
}
