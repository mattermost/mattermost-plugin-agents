// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentconfig

import (
	"context"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAgentID = "agent-1"
	testManager = "manager"
)

// fakeBackend is an in-memory implementation of every Deps collaborator.
type fakeBackend struct {
	agents   map[string]*llm.BotConfig
	metas    []store.AgentVersionMeta
	managers map[string]bool
	admins   map[string]bool
	changed  []string

	// updateErrs pops an error for each UpdateAgent call while non-empty.
	updateErrs []error
	// deletePolicyErr fails DeleteAgentPolicy.
	deletePolicyErr error
	// beforeConditionalUpdate runs at the start of each UpdateAgentIfUnchanged.
	beforeConditionalUpdate func()
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		agents: map[string]*llm.BotConfig{testAgentID: {
			ID: testAgentID, Name: "agent", DisplayName: "Agent", ServiceID: "svc-1", BotUserID: "bot-1",
			CustomInstructions: "original",
			EnabledNativeTools: []string{"web_search", "code_interpreter"},
			EnabledMCPTools:    []llm.EnabledMCPTool{{ServerOrigin: "https://a", ToolName: "one"}, {ServerOrigin: "https://a", ToolName: "two"}},
			ChannelAccessLevel: llm.ChannelAccessLevelAll,
		}},
		managers: map[string]bool{testManager: true},
		admins:   map[string]bool{},
	}
}

func (f *fakeBackend) GetAgent(id string) (*llm.BotConfig, error) {
	cfg, ok := f.agents[id]
	if !ok {
		return nil, nil
	}
	c := *cfg
	return &c, nil
}

func (f *fakeBackend) UpdateAgent(cfg *llm.BotConfig, meta store.AgentVersionMeta) error {
	if len(f.updateErrs) > 0 {
		err := f.updateErrs[0]
		f.updateErrs = f.updateErrs[1:]
		if err != nil {
			return err
		}
	}
	cfg.UpdateAt = f.agents[cfg.ID].UpdateAt + 1
	c := *cfg
	f.agents[cfg.ID] = &c
	f.metas = append(f.metas, meta)
	return nil
}

func (f *fakeBackend) UpdateAgentIfUnchanged(cfg *llm.BotConfig, expectedUpdateAt int64, meta store.AgentVersionMeta) error {
	if f.beforeConditionalUpdate != nil {
		f.beforeConditionalUpdate()
	}
	if f.agents[cfg.ID].UpdateAt != expectedUpdateAt {
		return store.ErrAgentChanged
	}
	return f.UpdateAgent(cfg, meta)
}

// concurrentSave changes the stored agent the way another manager's save would.
func (f *fakeBackend) concurrentSave(change func(cfg *llm.BotConfig)) {
	cfg := f.agents[testAgentID]
	change(cfg)
	cfg.UpdateAt++
}

func (f *fakeBackend) PluginConfig() (*config.Config, error) {
	return &config.Config{Services: []llm.ServiceConfig{{ID: "svc-1"}}}, nil
}

func (f *fakeBackend) CheckAgentTransition(llm.BotConfig, *llm.BotConfig) error { return nil }

func (f *fakeBackend) ValidateAgentWrite(context.Context, string, *llm.BotConfig, *llm.BotConfig) error {
	return nil
}

func (f *fakeBackend) DeleteAgentPolicy(context.Context, string, string) error {
	return f.deletePolicyErr
}

func (f *fakeBackend) GetUserByUsername(string) (*model.User, error) {
	return nil, pluginapi.ErrNotFound
}

func (f *fakeBackend) PatchBot(string, *model.BotPatch) error { return nil }

func (f *fakeBackend) BindAgentConfigReferences(string, string) error { return nil }

func (f *fakeBackend) AgentChanged(agentID string) bool {
	f.changed = append(f.changed, agentID)
	return true
}

func (f *fakeBackend) CanManage(_ context.Context, actorID string, _ *llm.BotConfig) bool {
	return f.managers[actorID]
}

func (f *fakeBackend) CanManageServiceAccounts(_ context.Context, actorID string, _ *llm.BotConfig) bool {
	return f.admins[actorID]
}

func (f *fakeBackend) Error(string, ...any) {}

func newTestService(f *fakeBackend) *Service {
	return New(Deps{Store: f, Settings: f, License: f, Access: f, Accounts: f, References: f, Notifier: f, Authorizer: f, Logger: f})
}

func selfSetup(actorID string) store.AgentVersionMeta {
	return store.AgentVersionMeta{ActorID: actorID, Source: store.AgentVersionSourceSelfSetup}
}

func TestSelfSetupWrites(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(cfg *llm.BotConfig)
		apply    func(cfg *llm.BotConfig)
		username string
		wantKind Kind
		wantErr  error
	}{
		{name: "instructions", apply: func(cfg *llm.BotConfig) { cfg.CustomInstructions = "new" }},
		{name: "display name", apply: func(cfg *llm.BotConfig) { cfg.DisplayName = "Helper" }},
		{name: "turning all tools off", apply: func(cfg *llm.BotConfig) { cfg.DisableTools = true }},
		{name: "narrowing native tools", apply: func(cfg *llm.BotConfig) { cfg.EnabledNativeTools = []string{"web_search"} }},
		{name: "narrowing MCP tools", apply: func(cfg *llm.BotConfig) { cfg.EnabledMCPTools = cfg.EnabledMCPTools[:1] }},
		{
			name: "turning tools back on", wantKind: KindForbidden, wantErr: ErrSelfSetupFieldNotAllowed,
			prepare: func(cfg *llm.BotConfig) { cfg.DisableTools = true },
			apply:   func(cfg *llm.BotConfig) { cfg.DisableTools = false },
		},
		{
			name: "adding a native tool", wantKind: KindForbidden, wantErr: ErrSelfSetupFieldNotAllowed,
			apply: func(cfg *llm.BotConfig) { cfg.EnabledNativeTools = append(cfg.EnabledNativeTools, "image_generation") },
		},
		{
			name: "adding an MCP tool", wantKind: KindForbidden, wantErr: ErrSelfSetupFieldNotAllowed,
			apply: func(cfg *llm.BotConfig) {
				cfg.EnabledMCPTools = append(cfg.EnabledMCPTools, llm.EnabledMCPTool{ServerOrigin: "https://b", ToolName: "x"})
			},
		},
		{
			name: "replacing an every-MCP-tool grant with a list", wantKind: KindForbidden, wantErr: ErrSelfSetupFieldNotAllowed,
			prepare: func(cfg *llm.BotConfig) { cfg.AutoEnableNewMCPTools = true },
			apply:   func(cfg *llm.BotConfig) { cfg.EnabledMCPTools = cfg.EnabledMCPTools[:1] },
		},
		{
			name: "access lists", wantKind: KindForbidden, wantErr: ErrSelfSetupFieldNotAllowed,
			apply: func(cfg *llm.BotConfig) { cfg.UserAccessLevel = llm.UserAccessLevelAllow; cfg.UserIDs = []string{"x"} },
		},
		{
			name: "the agent's admins", wantKind: KindForbidden, wantErr: ErrSelfSetupFieldNotAllowed,
			apply: func(cfg *llm.BotConfig) { cfg.AdminUserIDs = []string{"attacker"} },
		},
		{
			name: "the model", wantKind: KindForbidden, wantErr: ErrSelfSetupFieldNotAllowed,
			apply: func(cfg *llm.BotConfig) { cfg.Model = "other" },
		},
		{
			name: "service account auth", wantKind: KindForbidden,
			apply: func(cfg *llm.BotConfig) { cfg.UseServiceAccountAuth = true },
		},
		{
			name: "the username", wantKind: KindForbidden, wantErr: ErrSelfSetupFieldNotAllowed, username: "renamed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newFakeBackend()
			if tt.prepare != nil {
				tt.prepare(backend.agents[testAgentID])
			}
			before := *backend.agents[testAgentID]
			req := UpdateRequest{ActorID: testManager, AgentID: testAgentID, Meta: selfSetup(testManager)}
			if tt.apply != nil {
				req.Apply = func(cfg *llm.BotConfig) error { tt.apply(cfg); return nil }
			}
			if tt.username != "" {
				req.Username = &tt.username
			}

			_, err := newTestService(backend).Update(context.Background(), req)
			if tt.wantKind != KindInternal {
				require.Error(t, err)
				assert.Equal(t, tt.wantKind, KindOf(err))
				if tt.wantErr != nil {
					require.ErrorIs(t, err, tt.wantErr)
				}
				assert.Equal(t, before, *backend.agents[testAgentID], "nothing is written")
				assert.Empty(t, backend.metas)
				return
			}
			require.NoError(t, err)
			require.Len(t, backend.metas, 1)
			assert.Equal(t, selfSetup(testManager), backend.metas[0])
			assert.Equal(t, []string{testAgentID}, backend.changed, "the change is applied on every node")
		})
	}
}

// projectAuthorizer lets one extra actor manage the agent and delegates the
// rest, as a project-channel authorizer would.
type projectAuthorizer struct {
	next       Authorizer
	channelAdm string
}

func (p projectAuthorizer) CanManage(ctx context.Context, actorID string, agent *llm.BotConfig) bool {
	return actorID == p.channelAdm || p.next.CanManage(ctx, actorID, agent)
}

func (p projectAuthorizer) CanManageServiceAccounts(ctx context.Context, actorID string, agent *llm.BotConfig) bool {
	return p.next.CanManageServiceAccounts(ctx, actorID, agent)
}

func TestSetAuthorizerIsConsultedOnEveryWrite(t *testing.T) {
	tests := []struct {
		name      string
		actorID   string
		install   bool
		wantKind  Kind
		wantWrite bool
	}{
		{name: "default authorizer denies a channel admin", actorID: "channel-admin", wantKind: KindForbidden},
		{name: "installed authorizer allows the channel admin", actorID: "channel-admin", install: true, wantWrite: true},
		{name: "installed authorizer still delegates managers", actorID: testManager, install: true, wantWrite: true},
		{name: "installed authorizer denies everyone else", actorID: "stranger", install: true, wantKind: KindForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newFakeBackend()
			svc := newTestService(backend)
			if tt.install {
				svc.SetAuthorizer(projectAuthorizer{next: svc.Authorizer(), channelAdm: "channel-admin"})
			}

			_, getErr := svc.Get(context.Background(), tt.actorID, testAgentID)
			_, err := svc.Update(context.Background(), UpdateRequest{
				ActorID: tt.actorID, AgentID: testAgentID, Meta: selfSetup(tt.actorID),
				Apply: func(cfg *llm.BotConfig) error { cfg.CustomInstructions = "new"; return nil },
			})
			if !tt.wantWrite {
				assert.Equal(t, tt.wantKind, KindOf(getErr))
				assert.Equal(t, tt.wantKind, KindOf(err))
				require.ErrorIs(t, err, ErrForbidden)
				assert.Equal(t, "original", backend.agents[testAgentID].CustomInstructions)
				return
			}
			require.NoError(t, getErr)
			require.NoError(t, err)
			assert.Equal(t, "new", backend.agents[testAgentID].CustomInstructions)
		})
	}
}

func TestUpdateErrorKinds(t *testing.T) {
	tests := []struct {
		name     string
		agentID  string
		apply    func(cfg *llm.BotConfig) error
		wantKind Kind
	}{
		{name: "missing agent", agentID: "missing", wantKind: KindNotFound},
		{name: "apply refuses", agentID: testAgentID, wantKind: KindInvalid, apply: func(*llm.BotConfig) error { return errors.New("no") }},
		{name: "unknown service", agentID: testAgentID, wantKind: KindInvalid, apply: func(cfg *llm.BotConfig) error { cfg.ServiceID = "gone"; return nil }},
		{name: "invalid configuration", agentID: testAgentID, wantKind: KindInvalid, apply: func(cfg *llm.BotConfig) error { cfg.DisplayName = ""; return nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newFakeBackend()
			_, err := newTestService(backend).Update(context.Background(), UpdateRequest{
				ActorID: testManager, AgentID: tt.agentID, Apply: tt.apply,
				Meta: store.AgentVersionMeta{ActorID: testManager, Source: store.AgentVersionSourceUpdate},
			})
			require.Error(t, err)
			assert.Equal(t, tt.wantKind, KindOf(err))
			assert.Empty(t, backend.metas)
		})
	}
}

func TestSelfSetupWriteKeepsConcurrentSaves(t *testing.T) {
	tests := []struct {
		name string
		// conflicts is how many conditional writes race with another save.
		conflicts   int
		preloaded   bool
		wantKind    Kind
		wantWritten bool
	}{
		{name: "a save between read and write is kept and the change re-applied", conflicts: 1, wantWritten: true},
		{name: "a caller that preloaded the agent gets a conflict", conflicts: 1, preloaded: true, wantKind: KindConflict},
		{name: "persistent races give up with a conflict", conflicts: selfSetupWriteAttempts, wantKind: KindConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newFakeBackend()
			races := 0
			backend.beforeConditionalUpdate = func() {
				if races < tt.conflicts {
					races++
					backend.concurrentSave(func(cfg *llm.BotConfig) { cfg.Model = "chosen-by-manager" })
				}
			}
			req := UpdateRequest{
				ActorID: testManager, AgentID: testAgentID, Meta: selfSetup(testManager),
				Apply: func(cfg *llm.BotConfig) error { cfg.CustomInstructions = "new"; return nil },
			}
			if tt.preloaded {
				req.Stored, _ = backend.GetAgent(testAgentID)
			}

			_, err := newTestService(backend).Update(context.Background(), req)
			stored := backend.agents[testAgentID]
			assert.Equal(t, "chosen-by-manager", stored.Model, "the concurrent save must never be overwritten")
			if !tt.wantWritten {
				require.Error(t, err)
				assert.Equal(t, tt.wantKind, KindOf(err))
				require.ErrorIs(t, err, store.ErrAgentChanged)
				assert.Equal(t, "original", stored.CustomInstructions)
				assert.Empty(t, backend.metas)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "new", stored.CustomInstructions)
			assert.Equal(t, []store.AgentVersionMeta{selfSetup(testManager)}, backend.metas)
		})
	}
}

func TestPolicyDeleteFailureKeepsRegistriesInSyncWithTheStore(t *testing.T) {
	tests := []struct {
		name            string
		rollbackErr     error
		wantStoredLevel llm.UserAccessLevel
		wantRefreshed   bool
	}{
		{name: "rollback succeeds: the store and registries keep the old config", wantStoredLevel: llm.UserAccessLevelAttributeBased},
		{name: "rollback fails: registries load the stored new config", rollbackErr: errors.New("db down"), wantStoredLevel: llm.UserAccessLevelAll, wantRefreshed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newFakeBackend()
			backend.agents[testAgentID].UserAccessLevel = llm.UserAccessLevelAttributeBased
			backend.deletePolicyErr = errors.New("policy service down")
			backend.updateErrs = []error{nil, tt.rollbackErr}

			_, err := newTestService(backend).Update(context.Background(), UpdateRequest{
				ActorID: testManager, AgentID: testAgentID,
				Meta:  store.AgentVersionMeta{ActorID: testManager, Source: store.AgentVersionSourceUpdate},
				Apply: func(cfg *llm.BotConfig) error { cfg.UserAccessLevel = llm.UserAccessLevelAll; return nil },
			})
			require.Error(t, err)
			assert.Equal(t, KindPolicy, KindOf(err))
			assert.Equal(t, tt.wantStoredLevel, backend.agents[testAgentID].UserAccessLevel)
			if tt.wantRefreshed {
				assert.Equal(t, []string{testAgentID}, backend.changed)
			} else {
				assert.Empty(t, backend.changed)
			}
		})
	}
}
