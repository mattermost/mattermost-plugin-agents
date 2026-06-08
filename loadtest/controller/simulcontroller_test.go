// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
	"errors"
	"os"
	"testing"

	"github.com/blang/semver"
	"github.com/mattermost/mattermost-load-test-ng/loadtest/plugins"
	"github.com/mattermost/mattermost-load-test-ng/loadtest/store"
	"github.com/mattermost/mattermost-load-test-ng/loadtest/store/memstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimulController_Interface(t *testing.T) {
	var _ plugins.SimulController = (*SimulController)(nil)
}

func TestSimulController_PluginIdAndMinVersion(t *testing.T) {
	c := &SimulController{store: NewPluginStore()}
	assert.Equal(t, "mattermost-ai", c.PluginId())
	assert.Equal(t, semver.MustParse("7.8.0"), c.MinServerVersion())
}

func TestSimulController_Actions_TriggerModeAndFrequencies(t *testing.T) {
	type expectedAction struct {
		name      string
		frequency float64
	}

	tests := []struct {
		name string
		cfg  Config
		want []expectedAction
	}{
		{
			name: "both with distinct frequencies",
			cfg: Config{
				TriggerFrequencyChannelMention: 0.001,
				TriggerFrequencyDM:             0.01,
				AgentUsername:                  "x",
				TriggerMode:                    TriggerModeBoth,
			},
			want: []expectedAction{
				{name: "AskAgentChannelMention", frequency: 0.001},
				{name: "AskAgentDM", frequency: 0.01},
			},
		},
		{
			name: "omit zero frequency",
			cfg: Config{
				TriggerFrequencyChannelMention: 0.005,
				TriggerFrequencyDM:             0,
				AgentUsername:                  "x",
				TriggerMode:                    TriggerModeBoth,
			},
			want: []expectedAction{
				{name: "AskAgentChannelMention", frequency: 0.005},
			},
		},
		{
			name: "channel_mention only",
			cfg: Config{
				TriggerFrequencyChannelMention: 0.002,
				TriggerFrequencyDM:             0.001,
				AgentUsername:                  "x",
				TriggerMode:                    TriggerModeChannelMention,
			},
			want: []expectedAction{
				{name: "AskAgentChannelMention", frequency: 0.002},
			},
		},
		{
			name: "dm only",
			cfg: Config{
				TriggerFrequencyChannelMention: 0.001,
				TriggerFrequencyDM:             0.003,
				AgentUsername:                  "x",
				TriggerMode:                    TriggerModeDM,
			},
			want: []expectedAction{
				{name: "AskAgentDM", frequency: 0.003},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := &SimulController{
				store:  NewPluginStore(),
				config: tt.cfg,
			}
			acts := ctrl.Actions()
			require.Len(t, acts, len(tt.want))
			for i, want := range tt.want {
				assert.Equal(t, want.name, acts[i].Name)
				assert.Equal(t, want.frequency, acts[i].Frequency)
			}
		})
	}
}

func TestSimulController_Actions_ConfigErr(t *testing.T) {
	ctrl := &SimulController{
		store: NewPluginStore(),
		config: Config{
			AgentUsername:                  "b",
			TriggerMode:                    TriggerMode("invalid"),
			TriggerFrequencyChannelMention: 0,
			TriggerFrequencyDM:             0,
		},
		configErr: errors.New("bad config"),
	}
	acts := ctrl.Actions()
	require.Len(t, acts, 2)
	assert.Equal(t, "AskAgentChannelMention", acts[0].Name)
	assert.Equal(t, "AskAgentDM", acts[1].Name)
	resp := acts[0].Run(nil)
	require.Error(t, resp.Err)
	assert.Contains(t, resp.Err.Error(), "bad config")
}

func TestSimulController_RunHook_LoginSwitchTypes(t *testing.T) {
	st := newMemStoreWithUser(t)
	botID := model.NewId()
	require.NoError(t, st.SetUsers([]*model.User{{Id: botID, Username: "aibot"}}))

	u := &ltUserTest{
		simulTestUser: &simulTestUser{
			st: st,
			namesFn: func([]string) ([]string, error) {
				return []string{botID}, nil
			},
		},
	}

	ctrl := &SimulController{
		store: NewPluginStore(),
		config: Config{
			AgentUsername: "aibot",
			TriggerMode:   TriggerModeBoth,
		},
	}

	require.NoError(t, ctrl.RunHook(plugins.HookLogin, u, nil))
	targ := ctrl.store.Get(st.Id())
	assert.Equal(t, botID, targ.AgentUserID)
	assert.Equal(t, "aibot", targ.AgentUsername)

	err := ctrl.RunHook(plugins.HookSwitchTeam, u, plugins.HookPayloadSwitchTeam{TeamId: "team1"})
	require.NoError(t, err)
	assert.Equal(t, "team1", ctrl.store.Get(st.Id()).CurrentTeamID)

	err = ctrl.RunHook(plugins.HookSwitchChannel, u, plugins.HookPayloadSwitchChannel{ChannelId: "chan1"})
	require.NoError(t, err)
	assert.Equal(t, "chan1", ctrl.store.Get(st.Id()).CurrentChannelID)

	err = ctrl.RunHook(plugins.HookSwitchTeam, u, &plugins.HookPayloadSwitchTeam{TeamId: "team2"})
	require.NoError(t, err)
	assert.Equal(t, "team2", ctrl.store.Get(st.Id()).CurrentTeamID)

	err = ctrl.RunHook(plugins.HookSwitchChannel, u, &plugins.HookPayloadSwitchChannel{ChannelId: "chan2"})
	require.NoError(t, err)
	assert.Equal(t, "chan2", ctrl.store.Get(st.Id()).CurrentChannelID)

	err = ctrl.RunHook(plugins.HookSwitchTeam, u, "wrong")
	require.Error(t, err)
	err = ctrl.RunHook(plugins.HookSwitchChannel, u, 42)
	require.Error(t, err)

	require.NoError(t, ctrl.RunHook(plugins.HookType("unknown"), u, nil))
}

func TestSimulController_HookLogin_BestEffort(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) (*memstore.MemStore, *ltUserTest, *SimulController, func(t *testing.T, st *memstore.MemStore, u *ltUserTest, ctrl *SimulController))
	}{
		{
			name: "config error skips resolution",
			setup: func(t *testing.T) (*memstore.MemStore, *ltUserTest, *SimulController, func(t *testing.T, st *memstore.MemStore, u *ltUserTest, ctrl *SimulController)) {
				t.Helper()
				st := newMemStoreWithUser(t)
				called := false
				u := &ltUserTest{
					simulTestUser: &simulTestUser{
						st: st,
						namesFn: func([]string) ([]string, error) {
							called = true
							return nil, errors.New("should not resolve")
						},
					},
				}
				ctrl := &SimulController{
					store:     NewPluginStore(),
					config:    Config{AgentUsername: "ai", TriggerMode: TriggerModeBoth},
					configErr: errors.New("bad config"),
				}

				return st, u, ctrl, func(t *testing.T, st *memstore.MemStore, _ *ltUserTest, ctrl *SimulController) {
					t.Helper()
					assert.False(t, called)
					assert.Equal(t, UserState{}, ctrl.store.Get(st.Id()))
				}
			},
		},
		{
			name: "resolution errors are deferred to actions",
			setup: func(t *testing.T) (*memstore.MemStore, *ltUserTest, *SimulController, func(t *testing.T, st *memstore.MemStore, u *ltUserTest, ctrl *SimulController)) {
				t.Helper()
				st := newMemStoreWithUser(t)
				seedOpenChannel(t, st, "teamA", "town-square")
				u := &ltUserTest{
					simulTestUser: &simulTestUser{
						st: st,
						namesFn: func([]string) ([]string, error) {
							return nil, errors.New("missing ai user")
						},
					},
				}
				ctrl := &SimulController{
					store: NewPluginStore(),
					config: Config{
						AgentUsername:                  "ai",
						TriggerMode:                    TriggerModeBoth,
						TriggerFrequencyChannelMention: 0.001,
						TriggerFrequencyDM:             0.001,
						PromptProfile:                  "short",
					},
				}

				return st, u, ctrl, func(t *testing.T, _ *memstore.MemStore, u *ltUserTest, ctrl *SimulController) {
					t.Helper()
					resp := ctrl.askAgentChannelMention(u)
					require.Error(t, resp.Err)
					assert.Contains(t, resp.Err.Error(), "missing ai user")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, u, ctrl, assertion := tt.setup(t)

			require.NoError(t, ctrl.RunHook(plugins.HookLogin, u, nil))
			assertion(t, st, u, ctrl)
		})
	}
}

func TestSimulController_ClearUserData(t *testing.T) {
	ctrl := &SimulController{store: NewPluginStore()}
	ctrl.store.SetCurrentChannel("u1", "c1")
	ctrl.ClearUserData()
	assert.Equal(t, UserState{}, ctrl.store.Get("u1"))
}

func TestAskAgentChannelMention_BuildsMentionPost(t *testing.T) {
	st := newMemStoreWithUser(t)
	seedOpenChannel(t, st, "teamA", "town-square")

	botID := model.NewId()
	require.NoError(t, st.SetUsers([]*model.User{{Id: botID, Username: "aibot"}}))

	u := &ltUserTest{simulTestUser: &simulTestUser{st: st}}

	ctrl := &SimulController{
		store: NewPluginStore(),
		config: Config{
			TriggerMode:                    TriggerModeBoth,
			TriggerFrequencyChannelMention: 0.001,
			TriggerFrequencyDM:             0.001,
			AgentUsername:                  "ignored",
			PromptProfile:                  "short",
		},
	}
	ctrl.store.SetAgentTarget(st.Id(), AgentTarget{UserID: botID, Username: "aibot"})

	resp := ctrl.askAgentChannelMention(u)
	require.NoError(t, resp.Err)
	post := u.captured
	require.NotNil(t, post)
	ch, err := st.CurrentChannel()
	require.NoError(t, err)
	assert.Equal(t, ch.Id, post.ChannelId)
	assert.Contains(t, post.Message, "@aibot")
	assert.Contains(t, post.Message, " ")
}

func TestAskAgentDM_DirectChannelAndPost(t *testing.T) {
	st := newMemStoreWithUser(t)
	botID := model.NewId()
	require.NoError(t, st.SetUsers([]*model.User{{Id: botID, Username: "aibot"}}))

	u := &ltUserTest{
		simulTestUser: &simulTestUser{
			st:        st,
			dmChannel: "dm-channel-1",
		},
	}

	ctrl := &SimulController{
		store: NewPluginStore(),
		config: Config{
			TriggerMode:                    TriggerModeDM,
			TriggerFrequencyChannelMention: 0.001,
			TriggerFrequencyDM:             0.001,
			AgentUsername:                  "aibot",
			PromptProfile:                  "short",
		},
	}
	ctrl.store.SetAgentTarget(st.Id(), AgentTarget{UserID: botID, Username: "aibot"})

	resp := ctrl.askAgentDM(u)
	require.NoError(t, resp.Err)
	require.NotNil(t, u.captured)
	assert.Equal(t, botID, u.dmPeer, "CreateDirectChannel should be called with agent user id")
	assert.Equal(t, "dm-channel-1", u.captured.ChannelId)
	assert.NotContains(t, u.captured.Message, "@")
}

func TestAskAgentDM_SkipsWhenAgentIsSelf(t *testing.T) {
	st := newMemStoreWithUser(t)
	selfID := st.Id()

	u := &ltUserTest{simulTestUser: &simulTestUser{st: st}}

	ctrl := &SimulController{
		store: NewPluginStore(),
		config: Config{
			TriggerMode:        TriggerModeDM,
			TriggerFrequencyDM: 0.001,
			AgentUsername:      "me",
		},
	}
	ctrl.store.SetAgentTarget(selfID, AgentTarget{UserID: selfID, Username: "me"})

	resp := ctrl.askAgentDM(u)
	require.NoError(t, resp.Err)
	assert.Contains(t, resp.Info, "skip DM")
	assert.Nil(t, u.captured)
}

func TestReadConfigFromEnv_DefaultsWhenNoFile(t *testing.T) {
	t.Setenv(configEnvVar, "")
	_ = os.Remove(defaultConfigPath)
	c, err := ReadConfigFromEnv()
	require.NoError(t, err)
	assert.Equal(t, 0.001, c.TriggerFrequencyChannelMention)
}

func TestResolveChannelForMention_FromStore(t *testing.T) {
	st := newMemStoreWithUser(t)
	seedOpenChannel(t, st, "t1", "town-square")
	ch, ok, err := resolveChannelForMention(st, UserState{})
	require.NoError(t, err)
	assert.True(t, ok)
	cur, err := st.CurrentChannel()
	require.NoError(t, err)
	assert.Equal(t, cur.Id, ch.Id)
}

// --- helpers ---

type simulTestUser struct {
	st *memstore.MemStore

	captured  *model.Post
	dmPeer    string
	dmChannel string
	namesFn   func([]string) ([]string, error)
	idsFn     func([]string, int64) ([]string, error)
}

func (s *simulTestUser) Store() store.UserStore {
	return s.st
}

func (s *simulTestUser) CreatePost(p *model.Post) (string, error) {
	s.captured = p
	return "posted-id", nil
}

func (s *simulTestUser) CreateDirectChannel(otherUserID string) (string, error) {
	s.dmPeer = otherUserID
	if s.dmChannel == "" {
		return "dm-fallback", nil
	}
	return s.dmChannel, nil
}

func (s *simulTestUser) GetUsersByUsernames(names []string) ([]string, error) {
	if s.namesFn != nil {
		return s.namesFn(names)
	}
	return nil, errors.New("GetUsersByUsernames not stubbed")
}

//revive:disable-next-line:var-naming - GetUsersByIds matches the load-test-ng UserStore interface.
func (s *simulTestUser) GetUsersByIds(ids []string, since int64) ([]string, error) {
	if s.idsFn != nil {
		return s.idsFn(ids, since)
	}
	out := append([]string{}, ids...)
	return out, nil
}

func newMemStoreWithUser(t *testing.T) *memstore.MemStore {
	t.Helper()
	st, err := memstore.New(nil)
	require.NoError(t, err)
	uid := model.NewId()
	require.NoError(t, st.SetUser(&model.User{Id: uid, Username: "simuser"}))
	return st
}

func seedOpenChannel(t *testing.T, st *memstore.MemStore, teamID, channelName string) {
	t.Helper()
	ch := &model.Channel{TeamId: teamID, Name: channelName, Id: model.NewId(), Type: model.ChannelTypeOpen}
	require.NoError(t, st.SetTeam(&model.Team{Id: teamID, Name: "t"}))
	require.NoError(t, st.SetChannel(ch))
	require.NoError(t, st.SetTeamMember(teamID, &model.TeamMember{TeamId: teamID, UserId: st.Id()}))
	require.NoError(t, st.SetChannelMember(ch.Id, &model.ChannelMember{ChannelId: ch.Id, UserId: st.Id()}))
	require.NoError(t, st.SetCurrentChannel(ch))
}
