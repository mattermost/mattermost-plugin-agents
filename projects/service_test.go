// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// memStore is an in-memory Store with the same compare-and-set semantics as
// the SQL store.
type memStore struct {
	mu       sync.Mutex
	projects map[string]*store.Project
	grants   map[string]store.ProjectGrant
	deleted  []string
}

func newMemStore(projects ...*store.Project) *memStore {
	m := &memStore{projects: map[string]*store.Project{}, grants: map[string]store.ProjectGrant{}}
	for _, p := range projects {
		m.projects[p.ChannelID] = p
	}
	return m
}

func (m *memStore) update(channelID string, ok func(*store.Project) bool, apply func(*store.Project)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, found := m.projects[channelID]
	if !found || !ok(p) {
		return store.ErrProjectStateChanged
	}
	apply(p)
	return nil
}

func notEnded(p *store.Project) bool { return p.State != store.ProjectStateEnded }

func (m *memStore) ClaimProject(p *store.Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[p.ChannelID]; ok {
		return store.ErrProjectExists
	}
	cp := *p
	cp.State = store.ProjectStateProvisioning
	m.projects[p.ChannelID] = &cp
	return nil
}
func (m *memStore) DeleteProvisioningProject(channelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.projects, channelID)
	return nil
}
func (m *memStore) GetProject(channelID string) (*store.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[channelID]
	if !ok {
		return nil, store.ErrProjectNotFound
	}
	cp := *p
	return &cp, nil
}
func (m *memStore) ListLiveProjects() ([]*store.Project, error) { return nil, nil }
func (m *memStore) ListTeamProjects(teamID string) ([]*store.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*store.Project{}
	for _, p := range m.projects {
		if p.TeamID == teamID {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (m *memStore) SetProjectAgent(channelID, agentID, botUserID, root string) error {
	return m.update(channelID, func(p *store.Project) bool { return p.State == store.ProjectStateProvisioning }, func(p *store.Project) {
		p.AgentID, p.AgentBotUserID, p.SetupRootPostID, p.State = agentID, botUserID, root, store.ProjectStateSetup
	})
}
func (m *memStore) SetProjectSetupRoot(channelID, root string) error {
	return m.update(channelID, notEnded, func(p *store.Project) { p.SetupRootPostID = root })
}
func (m *memStore) TransitionProject(channelID, from, to string) error {
	return m.update(channelID, func(p *store.Project) bool { return p.State == from }, func(p *store.Project) { p.State = to })
}
func (m *memStore) EndProject(channelID, endedBy string, withoutWrapUp bool) error {
	return m.update(channelID, notEnded, func(p *store.Project) {
		p.State, p.EndedBy, p.EndedWithoutWrapUp, p.NextCheckinAt = store.ProjectStateEnded, endedBy, withoutWrapUp, 0
	})
}
func (m *memStore) SetProjectBoard(channelID, id string) error {
	return m.update(channelID, func(p *store.Project) bool { return notEnded(p) && p.BoardChannelID == "" }, func(p *store.Project) { p.BoardChannelID = id })
}
func (m *memStore) SetProjectRun(channelID, id string) error {
	return m.update(channelID, func(p *store.Project) bool { return notEnded(p) && p.PlaybookRunID == "" }, func(p *store.Project) { p.PlaybookRunID = id })
}
func (m *memStore) SetProjectArtifact(channelID string, wrapUp bool, id string) error {
	return m.update(channelID, notEnded, func(p *store.Project) {
		if wrapUp {
			p.WrapUpArtifactID = id
		} else {
			p.CharterArtifactID = id
		}
	})
}
func (m *memStore) SetProjectStatusNote(channelID, note string) error {
	return m.update(channelID, notEnded, func(p *store.Project) { p.StatusNote = note })
}
func (m *memStore) SetProjectLead(channelID, lead string) error {
	return m.update(channelID, notEnded, func(p *store.Project) { p.LeadUserID = lead })
}
func (m *memStore) SetProjectSchedule(channelID, cron, tz string, nextAt int64) error {
	return m.update(channelID, notEnded, func(p *store.Project) { p.CheckinCron, p.CheckinTimezone, p.NextCheckinAt = cron, tz, nextAt })
}
func (m *memStore) DueCheckins(now int64) ([]*store.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*store.Project
	for _, p := range m.projects {
		if p.NextCheckinAt > 0 && p.NextCheckinAt <= now && p.State == store.ProjectStateActive {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (m *memStore) ClaimCheckin(channelID string, expected, nextAt, now int64) (bool, error) {
	err := m.update(channelID, func(p *store.Project) bool { return p.NextCheckinAt == expected && p.State == store.ProjectStateActive },
		func(p *store.Project) { p.NextCheckinAt, p.LastCheckinAt = nextAt, now })
	return err == nil, nil
}
func (m *memStore) SetProjectLastCheckinPost(channelID, postID string) error {
	return m.update(channelID, func(*store.Project) bool { return true }, func(p *store.Project) { p.LastCheckinPostID = postID })
}
func (m *memStore) AddProjectThread(*store.ProjectThread) error { return nil }
func (m *memStore) RecordProjectGrant(g store.ProjectGrant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.grants[g.ProjectChannelID+"/"+g.TargetType+"/"+g.TargetID] = g
	return nil
}
func (m *memStore) ListProjectGrants(channelID string) ([]store.ProjectGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.ProjectGrant
	for _, g := range m.grants {
		if g.ProjectChannelID == channelID && g.RevokedAt == 0 {
			out = append(out, g)
		}
	}
	return out, nil
}
func (m *memStore) RevokeProjectGrant(channelID, targetType, targetID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := channelID + "/" + targetType + "/" + targetID
	g := m.grants[key]
	g.RevokedAt = 1
	m.grants[key] = g
	return nil
}
func (m *memStore) CreateAgent(cfg *llm.BotConfig, _ store.AgentVersionMeta) error {
	cfg.ID = model.NewId()
	return nil
}
func (m *memStore) DeleteAgent(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, id)
	return nil
}

// fakePlatform records what the service did to Mattermost.
type fakePlatform struct {
	mu            sync.Mutex
	users         map[string]*model.User
	members       map[string][]string
	botChannels   []string
	channelAdmins []string
	channelsErr   error
	archived      []string
	deactivated   []string
	posts         []*model.Post
	events        int
}

func (f *fakePlatform) GetUser(id string) (*model.User, error) {
	if u, ok := f.users[id]; ok {
		return u, nil
	}
	return nil, errors.New("not found")
}
func (f *fakePlatform) GetChannel(id string) (*model.Channel, error) {
	return &model.Channel{Id: id, Type: model.ChannelTypeOpen}, nil
}
func (f *fakePlatform) CreateChannel(c *model.Channel) (*model.Channel, error) {
	c.Id = model.NewId()
	return c, nil
}
func (f *fakePlatform) ArchiveChannel(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archived = append(f.archived, id)
	return nil
}
func (f *fakePlatform) AddTeamMember(string, string) error       { return nil }
func (f *fakePlatform) RemoveTeamMember(string, string) error    { return nil }
func (f *fakePlatform) AddChannelMember(string, string) error    { return nil }
func (f *fakePlatform) RemoveChannelMember(string, string) error { return nil }
func (f *fakePlatform) GetChannelMember(channelID, userID string) (*model.ChannelMember, error) {
	if slices.Contains(f.members[channelID], userID) {
		return &model.ChannelMember{ChannelId: channelID, UserId: userID}, nil
	}
	return nil, errors.New("not a member")
}
func (f *fakePlatform) SetChannelAdmin(string, string, bool) error { return nil }
func (f *fakePlatform) BotChannelIDs(string) ([]string, error)     { return f.botChannels, f.channelsErr }
func (f *fakePlatform) CreateBot(b *model.Bot) error {
	b.UserId = model.NewId()
	return nil
}
func (f *fakePlatform) DeactivateBot(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deactivated = append(f.deactivated, id)
	return nil
}
func (f *fakePlatform) CreatePost(p *model.Post) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p.Id = model.NewId()
	f.posts = append(f.posts, p)
	return nil
}
func (f *fakePlatform) PublishWebSocketEvent(string, map[string]any, *model.WebsocketBroadcast) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events++
}
func (f *fakePlatform) HasPermissionToTeam(string, string, *model.Permission) bool { return true }
func (f *fakePlatform) HasPermissionToChannel(userID, channelID string, permission *model.Permission) bool {
	if permission.Id == model.PermissionManageChannelRoles.Id {
		return slices.Contains(f.channelAdmins, userID)
	}
	return slices.Contains(f.members[channelID], userID)
}
func (f *fakePlatform) LogError(string, ...any) {}

type fakeBots struct{ bot *bots.Bot }

func (f fakeBots) GetBotByID(id string) *bots.Bot {
	if f.bot != nil && f.bot.BotUserID() == id {
		return f.bot
	}
	return nil
}
func (fakeBots) EnsureBots() error { return nil }

type fakeThreads struct {
	mu    sync.Mutex
	calls []conversations.AgentThreadRequest
}

func (f *fakeThreads) StartAgentThread(_ context.Context, req conversations.AgentThreadRequest) (*conversations.AgentThread, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return &conversations.AgentThread{RootPost: &model.Post{Id: model.NewId()}}, nil
}

type fakeConfig struct{ enabled bool }

func (f fakeConfig) EnableProjectChannels() bool   { return f.enabled }
func (fakeConfig) ProjectDefaultServiceID() string { return "svc" }
func (fakeConfig) ProjectDefaultModel() string     { return "" }

type fakeAutoReply struct{}

func (fakeAutoReply) EnableAmbient(context.Context, string, string, string) error { return nil }

type fakeWrapUps struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeWrapUps) WriteWrapUp(context.Context, *bots.Bot, *store.Project, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return "We shipped it.", nil
}

type fixture struct {
	svc      *Service
	store    *memStore
	platform *fakePlatform
	threads  *fakeThreads
	wrapUps  *fakeWrapUps
	bot      *bots.Bot
}

func newFixture(t *testing.T, enabled bool, project *store.Project) *fixture {
	t.Helper()
	bot := bots.NewBot(llm.BotConfig{ID: "agent", BotUserID: "botuser", ProjectChannelID: "P"}, llm.ServiceConfig{}, &model.Bot{UserId: "botuser"}, nil)
	f := &fixture{
		store: newMemStore(project),
		platform: &fakePlatform{
			users: map[string]*model.User{
				"lead":  {Id: "lead", Roles: model.SystemUserRoleId},
				"other": {Id: "other", Roles: model.SystemUserRoleId},
				"guest": {Id: "guest", Roles: model.SystemGuestRoleId},
			},
			members:     map[string][]string{"P": {"lead", "other", "guest", "botuser"}},
			botChannels: []string{"P"},
		},
		threads: &fakeThreads{},
		wrapUps: &fakeWrapUps{},
		bot:     bot,
	}
	f.svc = New(Deps{
		Store: f.store, Platform: f.platform, Bots: fakeBots{bot: bot}, Threads: f.threads,
		Config: fakeConfig{enabled: enabled}, AutoReply: fakeAutoReply{}, WrapUps: f.wrapUps,
	})
	return f
}

func activeProject() *store.Project {
	return &store.Project{ChannelID: "P", TeamID: "T", AgentID: "agent", AgentBotUserID: "botuser", State: store.ProjectStateActive, LeadUserID: "lead"}
}

func TestProjectTurnFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		enabled     bool
		mutate      func(p *store.Project)
		botChannels []string
		channelsErr error
		user        string
		channel     string
		wantErr     bool
		userOnly    bool
	}{
		{name: "member in project channel", enabled: true, user: "lead", channel: "P"},
		{name: "board channel membership is allowed", enabled: true, mutate: func(p *store.Project) { p.BoardChannelID = "B" }, botChannels: []string{"P", "B"}, user: "lead", channel: "P"},
		{name: "extra channel membership refuses", enabled: true, botChannels: []string{"P", "TOWN"}, user: "lead", channel: "P", wantErr: true},
		{name: "unlinked board membership refuses", enabled: true, botChannels: []string{"P", "B"}, user: "lead", channel: "P", wantErr: true},
		{name: "membership lookup error refuses", enabled: true, channelsErr: errors.New("db down"), user: "lead", channel: "P", wantErr: true},
		{name: "feature off refuses", enabled: false, user: "lead", channel: "P", wantErr: true},
		{name: "ended project refuses", enabled: true, mutate: func(p *store.Project) { p.State = store.ProjectStateEnded }, user: "lead", channel: "P", wantErr: true},
		{name: "another agent's project refuses", enabled: true, mutate: func(p *store.Project) { p.AgentID = "someone-else" }, user: "lead", channel: "P", wantErr: true},
		{name: "non-member refuses", enabled: true, user: "stranger", channel: "P", wantErr: true, userOnly: true},
		{name: "other channel refuses", enabled: true, user: "lead", channel: "X", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := activeProject()
			if tc.mutate != nil {
				tc.mutate(p)
			}
			f := newFixture(t, tc.enabled, p)
			if tc.botChannels != nil {
				f.platform.botChannels = tc.botChannels
			}
			f.platform.channelsErr = tc.channelsErr
			user := &model.User{Id: tc.user}
			channel := &model.Channel{Id: tc.channel}

			turnErr := f.svc.CheckTurn(context.Background(), f.bot, channel, user)
			_, scopeErr := f.svc.Scope(context.Background(), f.bot, channel)
			if tc.userOnly {
				assert.Error(t, turnErr)
				return
			}
			if tc.wantErr {
				assert.Error(t, turnErr)
				assert.Error(t, scopeErr)
				assert.Empty(t, f.svc.BindPluginTools(context.Background(), f.bot, &llm.Context{}, []llm.Tool{{Name: "get_board", ServerOrigin: BoardsOrigin}}))
				return
			}
			assert.NoError(t, turnErr)
			assert.NoError(t, scopeErr)
		})
	}
}

func TestEndProject(t *testing.T) {
	tests := []struct {
		name       string
		actor      string
		state      string
		skipWrapUp bool
		wantErr    error
		wantWrapUp bool
	}{
		{name: "lead ends with wrap-up", actor: "lead", state: store.ProjectStateActive, wantWrapUp: true},
		{name: "lead ends without wrap-up", actor: "lead", state: store.ProjectStateActive, skipWrapUp: true},
		{name: "non-lead without channel roles is refused", actor: "other", state: store.ProjectStateActive, wantErr: ErrForbidden},
		{name: "guest is refused", actor: "guest", state: store.ProjectStateActive, wantErr: ErrForbidden},
		{name: "ending project can't be ended again", actor: "lead", state: store.ProjectStateEnding, wantErr: ErrNotActive},
		{name: "ended project can't be ended again", actor: "lead", state: store.ProjectStateEnded, wantErr: ErrNotActive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := activeProject()
			p.State = tc.state
			p.BoardChannelID = "B"
			f := newFixture(t, true, p)
			f.platform.members["P"] = append(f.platform.members["P"], "lead")

			ended, err := f.svc.EndProject(context.Background(), tc.actor, "P", tc.skipWrapUp)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				current, getErr := f.store.GetProject("P")
				require.NoError(t, getErr)
				assert.Equal(t, tc.state, current.State)
				assert.Empty(t, f.platform.archived)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, store.ProjectStateEnded, ended.State)
			assert.Equal(t, !tc.wantWrapUp, ended.EndedWithoutWrapUp)
			assert.ElementsMatch(t, []string{"B", "P"}, f.platform.archived)
			assert.Equal(t, []string{"agent"}, f.store.deleted)
			assert.Equal(t, []string{"botuser"}, f.platform.deactivated)
			if tc.wantWrapUp {
				assert.Equal(t, 1, f.wrapUps.calls)
			} else {
				assert.Zero(t, f.wrapUps.calls)
			}
		})
	}
}

func TestEndProjectConcurrentOnlyOneWins(t *testing.T) {
	f := newFixture(t, true, activeProject())
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = f.svc.EndProject(context.Background(), "lead", "P", false)
		})
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else {
			assert.ErrorIs(t, err, ErrNotActive)
		}
	}
	assert.Equal(t, 1, wins)
	assert.Equal(t, 1, f.wrapUps.calls)
}

func TestOnChannelArchived(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		state     string
		wantState string
	}{
		{name: "archiving the project channel ends it without wrap-up", channelID: "P", state: store.ProjectStateActive, wantState: store.ProjectStateEnded},
		{name: "already ended project is left alone", channelID: "P", state: store.ProjectStateEnded, wantState: store.ProjectStateEnded},
		{name: "non-project channel is ignored", channelID: "X", state: store.ProjectStateActive, wantState: store.ProjectStateActive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := activeProject()
			p.State = tc.state
			f := newFixture(t, true, p)
			require.NoError(t, f.svc.OnChannelArchived(context.Background(), tc.channelID, "lead"))
			current, err := f.store.GetProject("P")
			require.NoError(t, err)
			assert.Equal(t, tc.wantState, current.State)
			assert.Zero(t, f.wrapUps.calls)
			assert.NotContains(t, f.platform.archived, "P", "the project channel is already archived")
		})
	}
}

func TestCheckins(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		lead       string
		wantThread bool
		wantNext   bool
	}{
		{name: "runs as the lead and schedules the next one", lead: "lead", wantThread: true, wantNext: true},
		{name: "guest lead pauses check-ins", lead: "guest"},
		{name: "lead who left pauses check-ins", lead: "gone"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := activeProject()
			p.LeadUserID = tc.lead
			p.CheckinCron = "0 9 * * *"
			p.NextCheckinAt = now.UnixMilli()
			f := newFixture(t, true, p)
			f.platform.users["gone"] = &model.User{Id: "gone"}

			f.svc.RunDueCheckins(context.Background(), now)
			f.svc.RunDueCheckins(context.Background(), now)

			current, err := f.store.GetProject("P")
			require.NoError(t, err)
			if tc.wantThread {
				require.Len(t, f.threads.calls, 1, "a claimed check-in runs once")
				assert.Equal(t, "lead", f.threads.calls[0].User.Id)
				assert.Equal(t, llm.OperationProjectCheckin, f.threads.calls[0].Operation)
			} else {
				assert.Empty(t, f.threads.calls)
			}
			if tc.wantNext {
				assert.Equal(t, now.Add(24*time.Hour).UnixMilli(), current.NextCheckinAt)
			} else {
				assert.Zero(t, current.NextCheckinAt)
			}
		})
	}
}

func TestParseSchedule(t *testing.T) {
	from := time.Date(2026, 11, 20, 10, 30, 0, 0, time.UTC) // a Friday
	tests := []struct {
		expr    string
		tz      string
		want    time.Time
		wantErr bool
	}{
		{expr: "0 9 * * 1-5", want: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)},
		{expr: "*/15 * * * *", want: time.Date(2026, 11, 20, 10, 45, 0, 0, time.UTC)},
		{expr: "0 9 * * *", tz: "America/New_York", want: time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)},
		{expr: "0 9 * *", wantErr: true},
		{expr: "61 9 * * *", wantErr: true},
		{expr: "0 9 * * *", tz: "Mars/Olympus", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.expr+" "+tc.tz, func(t *testing.T) {
			s, err := ParseSchedule(tc.expr, tc.tz)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			next, ok := s.Next(from)
			require.True(t, ok)
			assert.True(t, tc.want.Equal(next), "got %s", next.UTC())
		})
	}
}
