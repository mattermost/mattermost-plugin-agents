// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/projects"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	projectChannelID = "projchan000000000000000000"
	projectTeamID    = "projteam000000000000000000"
	projectAgentID   = "projagent00000000000000000"
	projectBotUserID = "projbot0000000000000000000"
	projectLeadID    = "projlead000000000000000000"
	projectSentinel  = "SENTINEL-PROJECT-CONTENT"
)

// fakeProjects records calls and answers with err, or a project.
type fakeProjects struct {
	available  bool
	err        error
	list       []*store.Project
	calls      []string
	lastCreate projects.CreateRequest
	lastSkip   bool
}

func (f *fakeProjects) project() *store.Project {
	return &store.Project{ChannelID: projectChannelID, TeamID: projectTeamID, AgentID: projectAgentID, AgentBotUserID: projectBotUserID,
		State: store.ProjectStateActive, LeadUserID: projectLeadID, CreatedFrom: store.ProjectCreatedFromModal}
}

func (f *fakeProjects) result(call string) (*store.Project, error) {
	f.calls = append(f.calls, call)
	if f.err != nil {
		return nil, f.err
	}
	return f.project(), nil
}

func (f *fakeProjects) CreateProject(_ context.Context, _ string, req projects.CreateRequest) (*store.Project, error) {
	f.lastCreate = req
	return f.result("create")
}
func (f *fakeProjects) GetProject(context.Context, string, string) (*store.Project, error) {
	return f.result("get")
}
func (f *fakeProjects) ListProjects(context.Context, string, string) ([]*store.Project, error) {
	f.calls = append(f.calls, "list")
	return f.list, f.err
}
func (f *fakeProjects) EndProject(_ context.Context, _, _ string, skip bool) (*store.Project, error) {
	f.lastSkip = skip
	return f.result("end")
}
func (f *fakeProjects) TransferLead(context.Context, string, string, string) (*store.Project, error) {
	return f.result("transfer")
}
func (f *fakeProjects) UpdateSchedule(context.Context, string, string, string, string) (*store.Project, error) {
	return f.result("schedule")
}
func (f *fakeProjects) GetChecklistSummary(context.Context, string, string) (*projects.ChecklistSummary, error) {
	f.calls = append(f.calls, "checklist")
	if f.err != nil {
		return nil, f.err
	}
	return &projects.ChecklistSummary{Available: true, RunID: "run", TotalTasks: 3, CompletedTasks: 1}, nil
}
func (f *fakeProjects) ProjectResponse(p *store.Project) projects.Response {
	r := projects.Response{Project: *p, AgentDisplayName: "Launch agent", AgentUsername: "project-launch"}
	r.CreatedFrom = projects.ResponseCreatedFromModal
	return r
}
func (f *fakeProjects) Available() bool { return f.available }

func TestProjectRoutes(t *testing.T) {
	base := "/projects/" + projectChannelID
	tests := []struct {
		name       string
		method     string
		path       string
		body       any
		err        error
		available  bool
		wantStatus int
		wantCall   string
		wantAudit  string
	}{
		{name: "create a new channel", method: http.MethodPost, path: "/projects", available: true,
			body:       map[string]any{"team_id": projectTeamID, "name": "launch", "display_name": projectSentinel, "purpose": projectSentinel},
			wantStatus: http.StatusCreated, wantCall: "create", wantAudit: AuditEventCreateProject},
		{name: "attach a channel just created", method: http.MethodPost, path: "/projects", available: true,
			body:       map[string]any{"team_id": projectTeamID, "channel_id": projectChannelID, "display_name": projectSentinel},
			wantStatus: http.StatusCreated, wantCall: "create", wantAudit: AuditEventCreateProject},
		{name: "create refused", method: http.MethodPost, path: "/projects", available: true, err: projects.ErrForbidden,
			body: map[string]any{"team_id": projectTeamID, "display_name": projectSentinel}, wantStatus: http.StatusForbidden, wantCall: "create", wantAudit: AuditEventCreateProject},
		{name: "create while disabled", method: http.MethodPost, path: "/projects",
			body: map[string]any{"team_id": projectTeamID, "display_name": projectSentinel}, wantStatus: http.StatusNotFound, wantAudit: AuditEventCreateProject},
		{name: "list", method: http.MethodGet, path: "/projects?team_id=" + projectTeamID, available: true, wantStatus: http.StatusOK, wantCall: "list"},
		{name: "list needs a team", method: http.MethodGet, path: "/projects", available: true, wantStatus: http.StatusBadRequest},
		{name: "get", method: http.MethodGet, path: base, available: true, wantStatus: http.StatusOK, wantCall: "get"},
		{name: "get a non-project channel", method: http.MethodGet, path: base, available: true, err: store.ErrProjectNotFound, wantStatus: http.StatusNotFound, wantCall: "get"},
		{name: "get an unreadable channel", method: http.MethodGet, path: base, available: true, err: projects.ErrForbidden, wantStatus: http.StatusForbidden, wantCall: "get"},
		{name: "end", method: http.MethodPost, path: base + "/end", available: true, body: map[string]any{"skip_wrap_up": true}, wantStatus: http.StatusOK, wantCall: "end", wantAudit: AuditEventEndProject},
		{name: "end twice conflicts", method: http.MethodPost, path: base + "/end", available: true, err: projects.ErrNotActive, wantStatus: http.StatusConflict, wantCall: "end", wantAudit: AuditEventEndProject},
		{name: "end by a non-lead", method: http.MethodPost, path: base + "/end", available: true, err: projects.ErrForbidden, wantStatus: http.StatusForbidden, wantCall: "end", wantAudit: AuditEventEndProject},
		{name: "transfer lead", method: http.MethodPost, path: base + "/transfer-lead", available: true, body: map[string]any{"lead_user_id": testUserID}, wantStatus: http.StatusOK, wantCall: "transfer", wantAudit: AuditEventTransferProjectLead},
		{name: "transfer lead needs a user", method: http.MethodPost, path: base + "/transfer-lead", available: true, body: map[string]any{"lead_user_id": "nope"}, wantStatus: http.StatusBadRequest, wantAudit: AuditEventTransferProjectLead},
		{name: "schedule", method: http.MethodPut, path: base + "/schedule", available: true, body: map[string]any{"cron": "0 9 * * 1-5", "timezone": "UTC"}, wantStatus: http.StatusOK, wantCall: "schedule", wantAudit: AuditEventUpdateProjectSchedule},
		{name: "bad schedule", method: http.MethodPut, path: base + "/schedule", available: true, err: projects.ErrInvalidRequest, body: map[string]any{"cron": "nope"}, wantStatus: http.StatusBadRequest, wantCall: "schedule", wantAudit: AuditEventUpdateProjectSchedule},
		{name: "checklist", method: http.MethodGet, path: base + "/checklist", available: true, wantStatus: http.StatusOK, wantCall: "checklist"},
		{name: "checklist while disabled", method: http.MethodGet, path: base + "/checklist", wantStatus: http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)
			fake := &fakeProjects{available: tc.available, err: tc.err}
			e.api.SetProjects(fake)
			records := e.CaptureAuditRecords()

			recorder := doRequest(e.api, tc.method, tc.path, tc.body, testUserID)
			require.Equal(t, tc.wantStatus, recorder.Code, recorder.Body.String())
			if tc.wantCall != "" {
				assert.Equal(t, []string{tc.wantCall}, fake.calls)
			} else {
				assert.Empty(t, fake.calls)
			}

			if tc.wantAudit == "" {
				assert.Empty(t, *records, "reads are not audited")
			} else {
				require.Len(t, *records, 1)
				rec := (*records)[0]
				assert.Equal(t, tc.wantAudit, rec.EventName)
				assert.Equal(t, testUserID, rec.Actor.UserId)
				assert.NotContains(t, marshaledRecords(t, *records), projectSentinel, "request content never enters the record")
				if tc.wantStatus < 300 {
					assert.Equal(t, model.AuditStatusSuccess, rec.Status)
					assert.Equal(t, projectChannelID, rec.EventData.Parameters[audit.KeyChannelID])
				} else {
					assert.Equal(t, model.AuditStatusFail, rec.Status)
				}
			}

			if tc.wantStatus == http.StatusCreated || (tc.wantStatus == http.StatusOK && tc.wantCall != "list" && tc.wantCall != "checklist") {
				var resp map[string]any
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
				assert.Equal(t, projectChannelID, resp["channel_id"])
				assert.Equal(t, "Launch agent", resp["agent_display_name"])
				assert.Equal(t, "channel_modal", resp["created_from"])
			}
		})
	}
}

func TestProjectCreateAttachesProvidedChannel(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	fake := &fakeProjects{available: true}
	e.api.SetProjects(fake)
	recorder := doRequest(e.api, http.MethodPost, "/projects", map[string]any{
		"team_id": projectTeamID, "channel_id": projectChannelID, "name": "launch", "display_name": "Launch", "private": true,
	}, testUserID)
	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, projects.CreateRequest{TeamID: projectTeamID, ChannelID: projectChannelID, Name: "launch", DisplayName: "Launch", Private: true, CreatedFrom: store.ProjectCreatedFromModal}, fake.lastCreate)
}

func TestListProjectsNeverNull(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.api.SetProjects(&fakeProjects{available: true})
	recorder := doRequest(e.api, http.MethodGet, "/projects?team_id="+projectTeamID, nil, testUserID)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"projects":[]}`, recorder.Body.String())
}

func TestProjectAgentsHiddenFromAgentLists(t *testing.T) {
	regularID := "regularbot0000000000000000"
	tests := []struct {
		name            string
		available       bool
		canReadProject  bool
		wantProjectBots int
	}{
		{name: "member sees the project agent only under projectAgents", available: true, canReadProject: true, wantProjectBots: 1},
		{name: "non-member does not see it at all", available: true},
		{name: "feature off hides it", canReadProject: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupAgentTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.SetProjects(&fakeProjects{available: tc.available})
			projectCfg := llm.BotConfig{ID: projectAgentID, Name: "project-launch", DisplayName: "Launch agent", BotUserID: projectBotUserID, ProjectChannelID: projectChannelID}
			e.bots.SetBotsForTesting([]*bots.Bot{
				bots.NewBot(llm.BotConfig{Name: "regular", DisplayName: "Regular", BotUserID: regularID}, llm.ServiceConfig{}, &model.Bot{UserId: regularID, Username: "regular"}, nil),
				bots.NewBot(projectCfg, llm.ServiceConfig{}, &model.Bot{UserId: projectBotUserID, Username: "project-launch", DisplayName: "Launch agent"}, nil),
			})
			e.agentStore.agents[projectAgentID] = &projectCfg
			e.mockAPI.On("GetChannelByName", "", mock.AnythingOfType("string"), false).Return(nil, &model.AppError{}).Maybe()
			e.mockAPI.On("HasPermissionToChannel", testUserID, projectChannelID, model.PermissionReadChannelContent).Return(tc.canReadProject).Maybe()
			e.mockAPI.On("GetUser", mock.Anything).Return(&model.User{Id: testUserID}, nil).Maybe()
			e.mockAPI.On("HasPermissionTo", mock.Anything, mock.Anything).Return(false).Maybe()

			recorder := doRequest(e.api, http.MethodGet, "/ai_bots", nil, testUserID)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var resp AIBotsResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
			for _, bot := range resp.Bots {
				assert.NotEqual(t, projectBotUserID, bot.ID, "project agents are never in the bot list")
			}
			require.NotNil(t, resp.ProjectAgents)
			assert.Len(t, resp.ProjectAgents, tc.wantProjectBots)

			recorder = doRequest(e.api, http.MethodGet, "/agents", nil, testUserID)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			assert.NotContains(t, recorder.Body.String(), projectAgentID)
		})
	}
}
