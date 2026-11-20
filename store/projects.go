// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
)

// Project states. Transitions are compare-and-set on the expected prior state.
const (
	ProjectStateProvisioning = "provisioning"
	ProjectStateSetup        = "setup"
	ProjectStateActive       = "active"
	ProjectStateEnding       = "ending"
	ProjectStateEnded        = "ended"
)

// Where a project was started from.
const (
	ProjectCreatedFromModal = "modal"
	ProjectCreatedFromDM    = "dm"
)

// Project reply modes.
const (
	ProjectReplyAmbient  = "ambient"
	ProjectReplyMentions = "mentions"
)

// Project grant target types and roles.
const (
	ProjectGrantTeam     = "team"
	ProjectGrantChannel  = "channel"
	ProjectGrantRunOwner = "run_owner"

	ProjectRoleMember       = "member"
	ProjectRoleChannelAdmin = "channel_admin"
	ProjectRoleOwner        = "owner"
)

// ProjectThreadKindCursor is a run thread for a Cursor cloud agent.
const ProjectThreadKindCursor = "cursor"

var (
	// ErrProjectExists is returned when a channel already has a project.
	ErrProjectExists = errors.New("this channel is already a project")
	// ErrProjectNotFound is returned when a channel has no project.
	ErrProjectNotFound = errors.New("project not found")
	// ErrProjectStateChanged is returned when a compare-and-set lost to a
	// concurrent change.
	ErrProjectStateChanged = errors.New("the project changed concurrently")
	// ErrProjectLinkSet is returned when a write-once link is already set.
	ErrProjectLinkSet = errors.New("the project already has this link")
)

// Project is one project channel.
type Project struct {
	ChannelID          string `db:"channelid" json:"channel_id"`
	TeamID             string `db:"teamid" json:"team_id"`
	AgentID            string `db:"agentid" json:"agent_id"`
	AgentBotUserID     string `db:"agentbotuserid" json:"agent_bot_user_id"`
	State              string `db:"state" json:"state"`
	LeadUserID         string `db:"leaduserid" json:"lead_user_id"`
	ReplyMode          string `db:"replymode" json:"reply_mode"`
	SetupRootPostID    string `db:"setuprootpostid" json:"setup_root_post_id"`
	BoardChannelID     string `db:"boardchannelid" json:"board_channel_id"`
	PlaybookRunID      string `db:"playbookrunid" json:"playbook_run_id"`
	CharterArtifactID  string `db:"charterartifactid" json:"charter_artifact_id"`
	WrapUpArtifactID   string `db:"wrapupartifactid" json:"wrap_up_artifact_id"`
	StatusNote         string `db:"statusnote" json:"status_note"`
	StatusUpdateAt     int64  `db:"statusupdateat" json:"status_update_at"`
	CheckinCron        string `db:"checkincron" json:"checkin_cron"`
	CheckinTimezone    string `db:"checkintimezone" json:"checkin_timezone"`
	NextCheckinAt      int64  `db:"nextcheckinat" json:"next_checkin_at"`
	LastCheckinAt      int64  `db:"lastcheckinat" json:"last_checkin_at"`
	LastCheckinPostID  string `db:"lastcheckinpostid" json:"last_checkin_post_id"`
	CreatedBy          string `db:"createdby" json:"created_by"`
	CreatedFrom        string `db:"createdfrom" json:"created_from"`
	CreateAt           int64  `db:"createat" json:"create_at"`
	UpdateAt           int64  `db:"updateat" json:"update_at"`
	EndedBy            string `db:"endedby" json:"ended_by"`
	EndedAt            int64  `db:"endedat" json:"ended_at"`
	EndedWithoutWrapUp bool   `db:"endedwithoutwrapup" json:"ended_without_wrap_up"`
}

// Live reports whether the project has not ended.
func (p *Project) Live() bool {
	return p != nil && p.State != ProjectStateEnded
}

// ProjectThread is a run thread the project agent opened.
type ProjectThread struct {
	RootPostID string `db:"rootpostid" json:"root_post_id"`
	ChannelID  string `db:"channelid" json:"channel_id"`
	Kind       string `db:"kind" json:"kind"`
	ExternalID string `db:"externalid" json:"external_id"`
	CardID     string `db:"cardid" json:"card_id"`
	StartedBy  string `db:"startedby" json:"started_by"`
	CreateAt   int64  `db:"createat" json:"create_at"`
}

// ProjectGrant is a membership or role granted to the project agent's bot.
type ProjectGrant struct {
	ProjectChannelID string `db:"projectchannelid"`
	TargetType       string `db:"targettype"`
	TargetID         string `db:"targetid"`
	Role             string `db:"role"`
	GrantedAt        int64  `db:"grantedat"`
	RevokedAt        int64  `db:"revokedat"`
}

const projectColumns = `ChannelID, TeamID, AgentID, AgentBotUserID, State, LeadUserID, ReplyMode,
	SetupRootPostID, BoardChannelID, PlaybookRunID, CharterArtifactID, WrapUpArtifactID,
	StatusNote, StatusUpdateAt, CheckinCron, CheckinTimezone, NextCheckinAt, LastCheckinAt,
	LastCheckinPostID, CreatedBy, CreatedFrom, CreateAt, UpdateAt, EndedBy, EndedAt, EndedWithoutWrapUp`

// ClaimProject inserts p in the provisioning state. A channel that already
// has a project, in any state, returns ErrProjectExists.
func (s *Store) ClaimProject(p *Project) error {
	res, err := s.db.Exec(`INSERT INTO Agents_Projects (ChannelID, TeamID, State, LeadUserID, ReplyMode, CreatedBy, CreatedFrom, CreateAt, UpdateAt)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
		ON CONFLICT (ChannelID) DO NOTHING`,
		p.ChannelID, p.TeamID, ProjectStateProvisioning, p.LeadUserID, p.ReplyMode, p.CreatedBy, p.CreatedFrom, p.CreateAt)
	if err != nil {
		return fmt.Errorf("failed to claim project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrProjectExists
	}
	return nil
}

// DeleteProvisioningProject removes a project that never finished
// provisioning, for undoing a failed create.
func (s *Store) DeleteProvisioningProject(channelID string) error {
	_, err := s.db.Exec(`DELETE FROM Agents_Projects WHERE ChannelID = $1 AND State = $2`, channelID, ProjectStateProvisioning)
	if err != nil {
		return fmt.Errorf("failed to delete provisioning project: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM Agents_ProjectGrants WHERE ProjectChannelID = $1`, channelID); err != nil {
		return fmt.Errorf("failed to delete provisioning project grants: %w", err)
	}
	return nil
}

// GetProject returns the project of channelID, or ErrProjectNotFound.
func (s *Store) GetProject(channelID string) (*Project, error) {
	var p Project
	err := s.db.Get(&p, `SELECT `+projectColumns+` FROM Agents_Projects WHERE ChannelID = $1`, channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrProjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	return &p, nil
}

// ListLiveProjects returns every project that has not ended.
func (s *Store) ListLiveProjects() ([]*Project, error) {
	var projects []*Project
	if err := s.db.Select(&projects, `SELECT `+projectColumns+` FROM Agents_Projects WHERE State <> $1 ORDER BY CreateAt`, ProjectStateEnded); err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}
	return projects, nil
}

// SetProjectAgent records the project's agent and moves it from provisioning
// to setup.
func (s *Store) SetProjectAgent(channelID, agentID, botUserID, setupRootPostID string) error {
	return s.updateProject(`UPDATE Agents_Projects SET AgentID = $1, AgentBotUserID = $2, SetupRootPostID = $3, State = $4, UpdateAt = $5
		WHERE ChannelID = $6 AND State = $7`,
		agentID, botUserID, setupRootPostID, ProjectStateSetup, model.GetMillis(), channelID, ProjectStateProvisioning)
}

// SetProjectSetupRoot records the setup thread's root post.
func (s *Store) SetProjectSetupRoot(channelID, rootPostID string) error {
	return s.updateProject(`UPDATE Agents_Projects SET SetupRootPostID = $1, UpdateAt = $2 WHERE ChannelID = $3 AND State <> $4`,
		rootPostID, model.GetMillis(), channelID, ProjectStateEnded)
}

// TransitionProject moves the project from one state to another. It returns
// ErrProjectStateChanged when the project is not in from.
func (s *Store) TransitionProject(channelID, from, to string) error {
	return s.updateProject(`UPDATE Agents_Projects SET State = $1, UpdateAt = $2 WHERE ChannelID = $3 AND State = $4`,
		to, model.GetMillis(), channelID, from)
}

// EndProject moves a live project to ended.
func (s *Store) EndProject(channelID, endedBy string, withoutWrapUp bool) error {
	now := model.GetMillis()
	return s.updateProject(`UPDATE Agents_Projects SET State = $1, EndedBy = $2, EndedAt = $3, EndedWithoutWrapUp = $4,
		NextCheckinAt = 0, UpdateAt = $3 WHERE ChannelID = $5 AND State <> $1`,
		ProjectStateEnded, endedBy, now, withoutWrapUp, channelID)
}

// SetProjectBoard records the project's board channel once.
func (s *Store) SetProjectBoard(channelID, boardChannelID string) error {
	return s.setLinkOnce(channelID, "BoardChannelID", boardChannelID)
}

// SetProjectRun records the project's checklist run once.
func (s *Store) SetProjectRun(channelID, runID string) error {
	return s.setLinkOnce(channelID, "PlaybookRunID", runID)
}

// SetProjectArtifact records the charter or wrap-up artifact.
func (s *Store) SetProjectArtifact(channelID string, wrapUp bool, artifactID string) error {
	column := "CharterArtifactID"
	if wrapUp {
		column = "WrapUpArtifactID"
	}
	//nolint:gosec // column is one of two constants
	return s.updateProject(`UPDATE Agents_Projects SET `+column+` = $1, UpdateAt = $2 WHERE ChannelID = $3 AND State <> $4`,
		artifactID, model.GetMillis(), channelID, ProjectStateEnded)
}

func (s *Store) setLinkOnce(channelID, column, value string) error {
	//nolint:gosec // column is a constant chosen by the caller
	res, err := s.db.Exec(`UPDATE Agents_Projects SET `+column+` = $1, UpdateAt = $2
		WHERE ChannelID = $3 AND State <> $4 AND `+column+` = ''`,
		value, model.GetMillis(), channelID, ProjectStateEnded)
	if err != nil {
		return fmt.Errorf("failed to set project %s: %w", column, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrProjectLinkSet
	}
	return nil
}

// SetProjectStatusNote replaces the project's status note.
func (s *Store) SetProjectStatusNote(channelID, note string) error {
	now := model.GetMillis()
	return s.updateProject(`UPDATE Agents_Projects SET StatusNote = $1, StatusUpdateAt = $2, UpdateAt = $2 WHERE ChannelID = $3 AND State <> $4`,
		note, now, channelID, ProjectStateEnded)
}

// SetProjectReplyMode changes how the agent replies in the channel.
func (s *Store) SetProjectReplyMode(channelID, mode string) error {
	return s.updateProject(`UPDATE Agents_Projects SET ReplyMode = $1, UpdateAt = $2 WHERE ChannelID = $3 AND State <> $4`,
		mode, model.GetMillis(), channelID, ProjectStateEnded)
}

// SetProjectLead transfers the project to a new lead.
func (s *Store) SetProjectLead(channelID, leadUserID string) error {
	return s.updateProject(`UPDATE Agents_Projects SET LeadUserID = $1, UpdateAt = $2 WHERE ChannelID = $3 AND State <> $4`,
		leadUserID, model.GetMillis(), channelID, ProjectStateEnded)
}

// SetProjectSchedule changes the check-in schedule. nextAt 0 turns it off.
func (s *Store) SetProjectSchedule(channelID, cron, timezone string, nextAt int64) error {
	return s.updateProject(`UPDATE Agents_Projects SET CheckinCron = $1, CheckinTimezone = $2, NextCheckinAt = $3, UpdateAt = $4
		WHERE ChannelID = $5 AND State <> $6`,
		cron, timezone, nextAt, model.GetMillis(), channelID, ProjectStateEnded)
}

// DueCheckins returns active projects whose next check-in is at or before now.
func (s *Store) DueCheckins(now int64) ([]*Project, error) {
	var projects []*Project
	if err := s.db.Select(&projects, `SELECT `+projectColumns+` FROM Agents_Projects
		WHERE NextCheckinAt > 0 AND NextCheckinAt <= $1 AND State = $2`, now, ProjectStateActive); err != nil {
		return nil, fmt.Errorf("failed to list due check-ins: %w", err)
	}
	return projects, nil
}

// ClaimCheckin moves a due check-in from expectedNextAt to nextAt. Only one
// caller, on any node, wins the claim for a given expectedNextAt.
func (s *Store) ClaimCheckin(channelID string, expectedNextAt, nextAt, now int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE Agents_Projects SET NextCheckinAt = $1, LastCheckinAt = $2, UpdateAt = $2
		WHERE ChannelID = $3 AND NextCheckinAt = $4 AND State = $5`,
		nextAt, now, channelID, expectedNextAt, ProjectStateActive)
	if err != nil {
		return false, fmt.Errorf("failed to claim check-in: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// SetProjectLastCheckinPost records the root post of the latest check-in.
func (s *Store) SetProjectLastCheckinPost(channelID, postID string) error {
	return s.updateProject(`UPDATE Agents_Projects SET LastCheckinPostID = $1 WHERE ChannelID = $2`, postID, channelID)
}

func (s *Store) updateProject(query string, args ...any) error {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrProjectStateChanged
	}
	return nil
}

// AddProjectThread records a run thread.
func (s *Store) AddProjectThread(t *ProjectThread) error {
	_, err := s.db.Exec(`INSERT INTO Agents_ProjectThreads (RootPostID, ChannelID, Kind, ExternalID, CardID, StartedBy, CreateAt)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		t.RootPostID, t.ChannelID, t.Kind, t.ExternalID, t.CardID, t.StartedBy, t.CreateAt)
	if err != nil {
		return fmt.Errorf("failed to add project thread: %w", err)
	}
	return nil
}

// SetProjectThreadCard records the board card that tracks a run thread.
func (s *Store) SetProjectThreadCard(channelID, rootPostID, cardID string) error {
	return s.updateProject(`UPDATE Agents_ProjectThreads SET CardID = $1 WHERE RootPostID = $2 AND ChannelID = $3`, cardID, rootPostID, channelID)
}

// ListProjectThreads returns the run threads of a project, oldest first.
func (s *Store) ListProjectThreads(channelID string) ([]*ProjectThread, error) {
	var threads []*ProjectThread
	if err := s.db.Select(&threads, `SELECT RootPostID, ChannelID, Kind, ExternalID, CardID, StartedBy, CreateAt
		FROM Agents_ProjectThreads WHERE ChannelID = $1 ORDER BY CreateAt`, channelID); err != nil {
		return nil, fmt.Errorf("failed to list project threads: %w", err)
	}
	return threads, nil
}

// RecordProjectGrant records a membership or role granted to the project
// agent's bot. Recording the same grant again refreshes it.
func (s *Store) RecordProjectGrant(g ProjectGrant) error {
	_, err := s.db.Exec(`INSERT INTO Agents_ProjectGrants (ProjectChannelID, TargetType, TargetID, Role, GrantedAt, RevokedAt)
		VALUES ($1, $2, $3, $4, $5, 0)
		ON CONFLICT (ProjectChannelID, TargetType, TargetID) DO UPDATE SET Role = EXCLUDED.Role, GrantedAt = EXCLUDED.GrantedAt, RevokedAt = 0`,
		g.ProjectChannelID, g.TargetType, g.TargetID, g.Role, g.GrantedAt)
	if err != nil {
		return fmt.Errorf("failed to record project grant: %w", err)
	}
	return nil
}

// ListProjectGrants returns the project's grants that are not revoked.
func (s *Store) ListProjectGrants(channelID string) ([]ProjectGrant, error) {
	var grants []ProjectGrant
	if err := s.db.Select(&grants, `SELECT ProjectChannelID, TargetType, TargetID, Role, GrantedAt, RevokedAt
		FROM Agents_ProjectGrants WHERE ProjectChannelID = $1 AND RevokedAt = 0`, channelID); err != nil {
		return nil, fmt.Errorf("failed to list project grants: %w", err)
	}
	return grants, nil
}

// RevokeProjectGrant marks a grant revoked.
func (s *Store) RevokeProjectGrant(channelID, targetType, targetID string) error {
	_, err := s.db.Exec(`UPDATE Agents_ProjectGrants SET RevokedAt = $1
		WHERE ProjectChannelID = $2 AND TargetType = $3 AND TargetID = $4 AND RevokedAt = 0`,
		model.GetMillis(), channelID, targetType, targetID)
	if err != nil {
		return fmt.Errorf("failed to revoke project grant: %w", err)
	}
	return nil
}
