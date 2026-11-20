// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentconfig"
	"github.com/mattermost/mattermost-plugin-agents/v2/agentsetup"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// WebSocketEventProjectUpdated is broadcast to the project channel whenever
// the project changes. The plugin API prefixes it with custom_mattermost-ai_.
const WebSocketEventProjectUpdated = "project_updated"

const projectAgentMaxToolTurns = 60

// newChannelWindow is how recently a channel must have been created for its
// creator to turn it into a project.
const newChannelWindow = 10 * time.Minute

var (
	// ErrDisabled is returned while project channels are off or have no
	// default LLM service.
	ErrDisabled = errors.New("project channels are not enabled")
	// ErrForbidden is returned when the actor may not do this to the project.
	ErrForbidden = errors.New("you don't have permission to do this for this project")
	// ErrInvalidRequest is returned for a malformed request.
	ErrInvalidRequest = errors.New("invalid project request")
	// ErrNotActive is returned when the project is not in a state that allows
	// the change.
	ErrNotActive = errors.New("the project is not active")
	// ErrExtraMembership is returned when the project bot belongs to a channel
	// outside its project, which means its scope can no longer be trusted.
	ErrExtraMembership = errors.New("the project agent belongs to channels outside its project")
)

// Store is the persistence the projects service uses.
type Store interface {
	ClaimProject(p *store.Project) error
	DeleteProvisioningProject(channelID string) error
	GetProject(channelID string) (*store.Project, error)
	ListLiveProjects() ([]*store.Project, error)
	ListTeamProjects(teamID string) ([]*store.Project, error)
	SetProjectAgent(channelID, agentID, botUserID, setupRootPostID string) error
	SetProjectSetupRoot(channelID, rootPostID string) error
	TransitionProject(channelID, from, to string) error
	EndProject(channelID, endedBy string, withoutWrapUp bool) error
	SetProjectBoard(channelID, boardChannelID string) error
	SetProjectRun(channelID, runID string) error
	SetProjectArtifact(channelID string, wrapUp bool, artifactID string) error
	SetProjectStatusNote(channelID, note string) error
	SetProjectLead(channelID, leadUserID string) error
	SetProjectSchedule(channelID, cron, timezone string, nextAt int64) error
	DueCheckins(now int64) ([]*store.Project, error)
	ClaimCheckin(channelID string, expectedNextAt, nextAt, now int64) (bool, error)
	SetProjectLastCheckinPost(channelID, postID string) error
	AddProjectThread(t *store.ProjectThread) error
	RecordProjectGrant(g store.ProjectGrant) error
	ListProjectGrants(channelID string) ([]store.ProjectGrant, error)
	RevokeProjectGrant(channelID, targetType, targetID string) error
	CreateAgent(cfg *llm.BotConfig, meta store.AgentVersionMeta) error
	DeleteAgent(id string) error
}

// Platform is the Mattermost server API the projects service uses.
type Platform interface {
	GetUser(userID string) (*model.User, error)
	GetChannel(channelID string) (*model.Channel, error)
	CreateChannel(channel *model.Channel) (*model.Channel, error)
	ArchiveChannel(channelID string) error
	AddTeamMember(teamID, userID string) error
	RemoveTeamMember(teamID, userID string) error
	AddChannelMember(channelID, userID string) error
	RemoveChannelMember(channelID, userID string) error
	GetChannelMember(channelID, userID string) (*model.ChannelMember, error)
	SetChannelAdmin(channelID, userID string, admin bool) error
	// BotChannelIDs returns every channel, on every team, userID belongs to.
	BotChannelIDs(userID string) ([]string, error)
	CreateBot(bot *model.Bot) error
	DeactivateBot(botUserID string) error
	CreatePost(post *model.Post) error
	PublishWebSocketEvent(event string, payload map[string]any, broadcast *model.WebsocketBroadcast)
	HasPermissionToTeam(userID, teamID string, permission *model.Permission) bool
	HasPermissionToChannel(userID, channelID string, permission *model.Permission) bool
	LogError(msg string, keyValuePairs ...any)
}

// Bots looks up and reloads running agents.
type Bots interface {
	GetBotByID(botUserID string) *bots.Bot
	EnsureBots() error
}

// Threads starts agent threads.
type Threads interface {
	StartAgentThread(ctx context.Context, req conversations.AgentThreadRequest) (*conversations.AgentThread, error)
}

// Config reads the project settings.
type Config interface {
	EnableProjectChannels() bool
	ProjectDefaultServiceID() string
	ProjectDefaultModel() string
}

// AutoReply turns on ambient replies for an agent in a channel.
type AutoReply interface {
	EnableAmbient(ctx context.Context, channelID, botUserID, actorID string) error
}

// WrapUps runs a project agent's unattended, non-streaming turn and returns
// its text.
type WrapUps interface {
	WriteWrapUp(ctx context.Context, bot *bots.Bot, project *store.Project, instruction string) (string, error)
}

// Artifacts saves Markdown to a channel's artifact library.
type Artifacts interface {
	SaveMarkdown(ctx context.Context, actor channelartifacts.Actor, channelID, title string, body []byte) (string, error)
}

// ChecklistSummary is the linked checklist run, as the viewer sees it.
type ChecklistSummary struct {
	Available      bool   `json:"available"`
	RunID          string `json:"run_id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	CompletedTasks int    `json:"completed_tasks"`
	TotalTasks     int    `json:"total_tasks"`
	URL            string `json:"url"`
}

// Playbooks reads and finishes checklist runs.
type Playbooks interface {
	FinishRun(ctx context.Context, runID, actorUserID string) error
	RunSummary(ctx context.Context, runID, viewerUserID string) (*ChecklistSummary, error)
}

// Deps are the Service's collaborators. Playbooks, WrapUps, and Artifacts may
// be nil; the rest are required.
type Deps struct {
	Store     Store
	Platform  Platform
	Bots      Bots
	Threads   Threads
	Config    Config
	AutoReply AutoReply
	WrapUps   WrapUps
	Artifacts Artifacts
	Playbooks Playbooks
	// SetupSystemPrompt renders the setup conversation's system prompt with
	// extra instructions.
	SetupSystemPrompt func(llmCtx *llm.Context, extraInstructions string) (string, error)
}

// Service runs project channels.
type Service struct {
	deps Deps

	tickerMu   sync.Mutex
	tickerStop chan struct{}
	tickerDone chan struct{}
}

var (
	_ llmcontext.ProjectRuntime    = (*Service)(nil)
	_ conversations.ProjectGate    = (*Service)(nil)
	_ conversations.OperationTools = (*Service)(nil)
	_ agentsetup.Extension         = (*Service)(nil)
)

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Store == nil || deps.Platform == nil || deps.Bots == nil || deps.Threads == nil ||
		deps.Config == nil || deps.AutoReply == nil {
		panic("projects: New requires Store, Platform, Bots, Threads, Config, and AutoReply")
	}
	return &Service{deps: deps}
}

func (s *Service) enabled() bool {
	return s.deps.Config.EnableProjectChannels()
}

// CreateRequest creates a project channel, or turns a channel its creator
// just created into one.
type CreateRequest struct {
	TeamID      string `json:"team_id"`
	ChannelID   string `json:"channel_id,omitempty"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Purpose     string `json:"purpose,omitempty"`
	Header      string `json:"header,omitempty"`
	Private     bool   `json:"private,omitempty"`
	CreatedFrom string `json:"-"`
}

// CreateProject creates the project channel, its hidden agent, and starts the
// setup thread. Any failure before the project is recorded undoes it.
func (s *Service) CreateProject(ctx context.Context, actorID string, req CreateRequest) (project *store.Project, err error) {
	if !s.enabled() || s.deps.Config.ProjectDefaultServiceID() == "" {
		return nil, ErrDisabled
	}
	actor, err := s.deps.Platform.GetUser(actorID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if actor.IsGuest() || actor.IsBot {
		return nil, ErrForbidden
	}
	channel, err := s.projectChannel(actor, req)
	if err != nil {
		return nil, err
	}
	createdFrom := req.CreatedFrom
	if createdFrom == "" {
		createdFrom = store.ProjectCreatedFromModal
	}

	now := model.GetMillis()
	project = &store.Project{
		ChannelID:   channel.Id,
		TeamID:      channel.TeamId,
		LeadUserID:  actor.Id,
		ReplyMode:   store.ProjectReplyAmbient,
		CreatedBy:   actor.Id,
		CreatedFrom: createdFrom,
		CreateAt:    now,
	}
	if err = s.deps.Store.ClaimProject(project); err != nil {
		return nil, err
	}

	var agent *llm.BotConfig
	defer func() {
		if err == nil {
			return
		}
		s.undoCreate(project, agent)
	}()

	agent, err = s.createAgent(actor, channel)
	if err != nil {
		return nil, err
	}
	if err = s.grantProjectAccess(project.ChannelID, channel.TeamId, agent.BotUserID); err != nil {
		return nil, err
	}
	if err = s.deps.Store.SetProjectAgent(channel.Id, agent.ID, agent.BotUserID, ""); err != nil {
		return nil, err
	}
	if err = s.deps.Store.TransitionProject(channel.Id, store.ProjectStateSetup, store.ProjectStateActive); err != nil {
		return nil, err
	}
	if err = s.deps.AutoReply.EnableAmbient(ctx, channel.Id, agent.BotUserID, actor.Id); err != nil {
		return nil, fmt.Errorf("failed to turn on replies in the project channel: %w", err)
	}

	bot := s.deps.Bots.GetBotByID(agent.BotUserID)
	if bot == nil || bot.GetConfig().ID != agent.ID {
		return nil, errors.New("the project agent is not running")
	}
	thread, err := s.deps.Threads.StartAgentThread(ctx, conversations.AgentThreadRequest{
		Bot:          bot,
		User:         actor,
		Channel:      channel,
		Operation:   llm.OperationAgentSetup,
		Message:     "Let's set up this project. Tell me what it is for and what done looks like.",
		Instruction: "Start the project setup with the people in this channel.",
		RootProps: map[string]any{
			conversations.ProjectThreadOperationProp: llm.OperationAgentSetup,
			agentsetup.RootPostAgentIDProp:           agent.ID,
		},
		SystemPrompt: s.setupSystemPrompt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start the setup thread: %w", err)
	}
	if rootErr := s.deps.Store.SetProjectSetupRoot(channel.Id, thread.RootPost.Id); rootErr != nil {
		s.deps.Platform.LogError("Failed to record project setup thread", "channel_id", channel.Id, "error", rootErr)
	}

	created, err := s.deps.Store.GetProject(channel.Id)
	if err != nil {
		return nil, err
	}
	s.publish(created)
	return created, nil
}

// setupSystemPrompt renders the setup prompt. The project block is appended
// to every project turn by the conversation service, so it is not added here.
func (s *Service) setupSystemPrompt(llmCtx *llm.Context) (string, error) {
	if s.deps.SetupSystemPrompt == nil {
		return setupInstructions, nil
	}
	return s.deps.SetupSystemPrompt(llmCtx, setupInstructions)
}

// projectChannel creates the project channel, or returns the channel the
// actor just created when the request names one.
func (s *Service) projectChannel(actor *model.User, req CreateRequest) (*model.Channel, error) {
	if req.ChannelID != "" {
		channel, err := s.deps.Platform.GetChannel(req.ChannelID)
		if err != nil {
			return nil, fmt.Errorf("failed to get channel: %w", err)
		}
		if channel.DeleteAt != 0 || (channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate) {
			return nil, ErrInvalidRequest
		}
		if channel.CreatorId != actor.Id || model.GetMillis()-channel.CreateAt > newChannelWindow.Milliseconds() {
			return nil, ErrForbidden
		}
		if req.TeamID != "" && req.TeamID != channel.TeamId {
			return nil, ErrInvalidRequest
		}
		return channel, nil
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if req.TeamID == "" || displayName == "" {
		return nil, ErrInvalidRequest
	}
	channelType := model.ChannelTypeOpen
	permission := model.PermissionCreatePublicChannel
	if req.Private {
		channelType = model.ChannelTypePrivate
		permission = model.PermissionCreatePrivateChannel
	}
	if !s.deps.Platform.HasPermissionToTeam(actor.Id, req.TeamID, permission) {
		return nil, ErrForbidden
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = channelNameFrom(displayName)
	}
	if name == "" || !model.IsValidChannelIdentifier(name) {
		name = model.NewId()
	}
	channel, err := s.deps.Platform.CreateChannel(&model.Channel{
		TeamId:      req.TeamID,
		Type:        channelType,
		Name:        name,
		DisplayName: displayName,
		Purpose:     req.Purpose,
		Header:      req.Header,
		CreatorId:   actor.Id,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create the project channel: %w", err)
	}
	return channel, nil
}

func channelNameFrom(displayName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(displayName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *Service) createAgent(actor *model.User, channel *model.Channel) (*llm.BotConfig, error) {
	username := "project-" + strings.ToLower(model.NewId()[:12])
	displayName := strings.TrimSpace(channel.DisplayName) + " agent"
	mmBot := &model.Bot{
		Username:    username,
		DisplayName: displayName,
		Description: "Project agent for ~" + channel.Name,
		OwnerId:     actor.Id,
	}
	if err := s.deps.Platform.CreateBot(mmBot); err != nil {
		return nil, fmt.Errorf("failed to create the project agent's bot: %w", err)
	}
	agent := &llm.BotConfig{
		BotUserID:             mmBot.UserId,
		CreatorID:             actor.Id,
		Name:                  username,
		DisplayName:           displayName,
		ServiceID:             s.deps.Config.ProjectDefaultServiceID(),
		Model:                 s.deps.Config.ProjectDefaultModel(),
		ProjectChannelID:      channel.Id,
		UseServiceAccountAuth: true,
		ScopedChannelMode:     true,
		MaxToolTurns:          projectAgentMaxToolTurns,
	}
	if err := s.deps.Store.CreateAgent(agent, store.AgentVersionMeta{ActorID: actor.Id, Source: store.AgentVersionSourceCreate}); err != nil {
		if deactivateErr := s.deps.Platform.DeactivateBot(mmBot.UserId); deactivateErr != nil {
			s.deps.Platform.LogError("Failed to deactivate unused project bot", "bot_user_id", mmBot.UserId, "error", deactivateErr)
		}
		return nil, fmt.Errorf("failed to save the project agent: %w", err)
	}
	if err := s.deps.Bots.EnsureBots(); err != nil {
		return agent, fmt.Errorf("failed to start the project agent: %w", err)
	}
	return agent, nil
}

// grantProjectAccess adds the bot to the team and the project channel as a
// channel admin, and removes it from every other channel joining the team
// added it to.
func (s *Service) grantProjectAccess(channelID, teamID, botUserID string) error {
	now := model.GetMillis()
	if err := s.deps.Platform.AddTeamMember(teamID, botUserID); err != nil {
		return fmt.Errorf("failed to add the project agent to the team: %w", err)
	}
	if err := s.deps.Store.RecordProjectGrant(store.ProjectGrant{ProjectChannelID: channelID, TargetType: store.ProjectGrantTeam, TargetID: teamID, Role: store.ProjectRoleMember, GrantedAt: now}); err != nil {
		return err
	}
	if err := s.grantChannelAdmin(channelID, channelID, botUserID); err != nil {
		return err
	}
	memberships, err := s.deps.Platform.BotChannelIDs(botUserID)
	if err != nil {
		return fmt.Errorf("failed to check the project agent's channels: %w", err)
	}
	for _, id := range memberships {
		if id == channelID {
			continue
		}
		if err := s.deps.Platform.RemoveChannelMember(id, botUserID); err != nil {
			return fmt.Errorf("failed to remove the project agent from a channel outside the project: %w", err)
		}
	}
	return nil
}

func (s *Service) grantChannelAdmin(projectChannelID, channelID, botUserID string) error {
	if err := s.deps.Platform.AddChannelMember(channelID, botUserID); err != nil {
		return fmt.Errorf("failed to add the project agent to the channel: %w", err)
	}
	if err := s.deps.Platform.SetChannelAdmin(channelID, botUserID, true); err != nil {
		return fmt.Errorf("failed to make the project agent a channel admin: %w", err)
	}
	return s.deps.Store.RecordProjectGrant(store.ProjectGrant{
		ProjectChannelID: projectChannelID, TargetType: store.ProjectGrantChannel, TargetID: channelID,
		Role: store.ProjectRoleChannelAdmin, GrantedAt: model.GetMillis(),
	})
}

func (s *Service) undoCreate(project *store.Project, agent *llm.BotConfig) {
	if agent != nil {
		if err := s.deps.Store.DeleteAgent(agent.ID); err != nil {
			s.deps.Platform.LogError("Failed to delete project agent after failed create", "agent_id", agent.ID, "error", err)
		}
		if err := s.deps.Platform.DeactivateBot(agent.BotUserID); err != nil {
			s.deps.Platform.LogError("Failed to deactivate project bot after failed create", "bot_user_id", agent.BotUserID, "error", err)
		}
		if err := s.deps.Bots.EnsureBots(); err != nil {
			s.deps.Platform.LogError("Failed to reload agents after failed project create", "error", err)
		}
	}
	current, err := s.deps.Store.GetProject(project.ChannelID)
	if err != nil {
		s.deps.Platform.LogError("Failed to read project after failed create", "channel_id", project.ChannelID, "error", err)
		return
	}
	if current.State == store.ProjectStateProvisioning {
		if err := s.deps.Store.DeleteProvisioningProject(project.ChannelID); err != nil {
			s.deps.Platform.LogError("Failed to delete project after failed create", "channel_id", project.ChannelID, "error", err)
		}
		return
	}
	if err := s.deps.Store.EndProject(project.ChannelID, project.CreatedBy, true); err != nil {
		s.deps.Platform.LogError("Failed to end project after failed create", "channel_id", project.ChannelID, "error", err)
	}
}

// GetProject returns the project of channelID for a viewer who can read the
// channel.
func (s *Service) GetProject(_ context.Context, viewerID, channelID string) (*store.Project, error) {
	if !s.deps.Platform.HasPermissionToChannel(viewerID, channelID, model.PermissionReadChannelContent) {
		return nil, ErrForbidden
	}
	return s.deps.Store.GetProject(channelID)
}

// ListProjects returns the team's projects whose channels viewerID can read.
// It never returns nil.
func (s *Service) ListProjects(_ context.Context, viewerID, teamID string) ([]*store.Project, error) {
	projects, err := s.deps.Store.ListTeamProjects(teamID)
	if err != nil {
		return nil, err
	}
	visible := make([]*store.Project, 0, len(projects))
	for _, p := range projects {
		if p.State == store.ProjectStateProvisioning {
			continue
		}
		if s.deps.Platform.HasPermissionToChannel(viewerID, p.ChannelID, model.PermissionReadChannelContent) {
			visible = append(visible, p)
		}
	}
	return visible, nil
}

// canLead reports whether actorID may manage the project: its lead, or
// someone who manages the channel's roles.
func (s *Service) canLead(actorID string, project *store.Project) bool {
	user, err := s.deps.Platform.GetUser(actorID)
	if err != nil || user.IsGuest() || user.IsBot {
		return false
	}
	if actorID == project.LeadUserID {
		return s.isMember(project.ChannelID, actorID)
	}
	return s.deps.Platform.HasPermissionToChannel(actorID, project.ChannelID, model.PermissionManageChannelRoles)
}

func (s *Service) isMember(channelID, userID string) bool {
	member, err := s.deps.Platform.GetChannelMember(channelID, userID)
	return err == nil && member != nil
}

// EndProject ends an active project: wrap-up, checklist, archive, agent.
func (s *Service) EndProject(ctx context.Context, actorID, channelID string, skipWrapUp bool) (*store.Project, error) {
	project, err := s.deps.Store.GetProject(channelID)
	if err != nil {
		return nil, err
	}
	if !s.canLead(actorID, project) {
		return nil, ErrForbidden
	}
	return s.end(ctx, project, actorID, skipWrapUp, true)
}

// OnChannelArchived ends the project of an archived project channel without
// a wrap-up.
func (s *Service) OnChannelArchived(ctx context.Context, channelID, actorID string) error {
	project, err := s.deps.Store.GetProject(channelID)
	if errors.Is(err, store.ErrProjectNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if project.State != store.ProjectStateActive {
		return nil
	}
	_, err = s.end(ctx, project, actorID, true, false)
	if errors.Is(err, ErrNotActive) {
		return nil
	}
	return err
}

func (s *Service) end(ctx context.Context, project *store.Project, actorID string, skipWrapUp, archiveProjectChannel bool) (*store.Project, error) {
	if err := s.deps.Store.TransitionProject(project.ChannelID, store.ProjectStateActive, store.ProjectStateEnding); err != nil {
		if errors.Is(err, store.ErrProjectStateChanged) {
			return nil, ErrNotActive
		}
		return nil, err
	}
	project.State = store.ProjectStateEnding
	s.publish(project)

	withoutWrapUp := skipWrapUp || !s.writeWrapUp(ctx, project)

	if project.PlaybookRunID != "" && s.deps.Playbooks != nil {
		if err := s.deps.Playbooks.FinishRun(ctx, project.PlaybookRunID, actorID); err != nil {
			s.deps.Platform.LogError("Failed to finish project checklist", "channel_id", project.ChannelID, "error", err)
		}
	}

	if err := s.deps.Store.EndProject(project.ChannelID, actorID, withoutWrapUp); err != nil {
		return nil, err
	}

	s.revokeGrants(project)
	if project.BoardChannelID != "" {
		if err := s.deps.Platform.ArchiveChannel(project.BoardChannelID); err != nil {
			s.deps.Platform.LogError("Failed to archive project board", "channel_id", project.ChannelID, "error", err)
		}
	}
	if archiveProjectChannel {
		if err := s.deps.Platform.ArchiveChannel(project.ChannelID); err != nil {
			s.deps.Platform.LogError("Failed to archive project channel", "channel_id", project.ChannelID, "error", err)
		}
	}
	s.retireAgent(project)

	ended, err := s.deps.Store.GetProject(project.ChannelID)
	if err != nil {
		return nil, err
	}
	s.publish(ended)
	return ended, nil
}

// writeWrapUp posts the wrap-up and saves it as an artifact. It reports
// whether the wrap-up was posted.
func (s *Service) writeWrapUp(ctx context.Context, project *store.Project) bool {
	if s.deps.WrapUps == nil {
		return false
	}
	bot := s.deps.Bots.GetBotByID(project.AgentBotUserID)
	if bot == nil || bot.GetConfig().ID != project.AgentID {
		return false
	}
	summary, err := s.deps.WrapUps.WriteWrapUp(ctx, bot, project, wrapUpPrompt)
	if err != nil || strings.TrimSpace(summary) == "" {
		if err != nil {
			s.deps.Platform.LogError("Failed to write project wrap-up", "channel_id", project.ChannelID, "error", err)
		}
		return false
	}
	if err := s.deps.Platform.CreatePost(&model.Post{ChannelId: project.ChannelID, UserId: project.AgentBotUserID, Message: summary}); err != nil {
		s.deps.Platform.LogError("Failed to post project wrap-up", "channel_id", project.ChannelID, "error", err)
		return false
	}
	if s.deps.Artifacts != nil {
		id, err := s.deps.Artifacts.SaveMarkdown(ctx, s.ArtifactActor(project), project.ChannelID, "Project wrap-up", []byte(summary))
		if err != nil {
			s.deps.Platform.LogError("Failed to save project wrap-up artifact", "channel_id", project.ChannelID, "error", err)
		} else if err := s.deps.Store.SetProjectArtifact(project.ChannelID, true, id); err != nil {
			s.deps.Platform.LogError("Failed to record project wrap-up artifact", "channel_id", project.ChannelID, "error", err)
		}
	}
	return true
}

func (s *Service) revokeGrants(project *store.Project) {
	grants, err := s.deps.Store.ListProjectGrants(project.ChannelID)
	if err != nil {
		s.deps.Platform.LogError("Failed to list project grants", "channel_id", project.ChannelID, "error", err)
		return
	}
	for _, g := range grants {
		var revokeErr error
		switch g.TargetType {
		case store.ProjectGrantChannel:
			revokeErr = s.deps.Platform.SetChannelAdmin(g.TargetID, project.AgentBotUserID, false)
		case store.ProjectGrantTeam:
			revokeErr = s.deps.Platform.RemoveTeamMember(g.TargetID, project.AgentBotUserID)
		}
		if revokeErr != nil {
			s.deps.Platform.LogError("Failed to revoke project grant", "channel_id", project.ChannelID, "target_type", g.TargetType, "error", revokeErr)
		}
		if err := s.deps.Store.RevokeProjectGrant(project.ChannelID, g.TargetType, g.TargetID); err != nil {
			s.deps.Platform.LogError("Failed to record revoked project grant", "channel_id", project.ChannelID, "error", err)
		}
	}
}

func (s *Service) retireAgent(project *store.Project) {
	if project.AgentID == "" {
		return
	}
	if err := s.deps.Store.DeleteAgent(project.AgentID); err != nil {
		s.deps.Platform.LogError("Failed to delete project agent", "agent_id", project.AgentID, "error", err)
	}
	if err := s.deps.Platform.DeactivateBot(project.AgentBotUserID); err != nil {
		s.deps.Platform.LogError("Failed to deactivate project bot", "bot_user_id", project.AgentBotUserID, "error", err)
	}
	if err := s.deps.Bots.EnsureBots(); err != nil {
		s.deps.Platform.LogError("Failed to reload agents after ending project", "error", err)
	}
}

// TransferLead makes another non-guest member of the channel the lead.
func (s *Service) TransferLead(_ context.Context, actorID, channelID, leadUserID string) (*store.Project, error) {
	project, err := s.deps.Store.GetProject(channelID)
	if err != nil {
		return nil, err
	}
	if !project.Live() {
		return nil, ErrNotActive
	}
	if !s.canLead(actorID, project) {
		return nil, ErrForbidden
	}
	lead, err := s.deps.Platform.GetUser(leadUserID)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if lead.IsGuest() || lead.IsBot || lead.DeleteAt != 0 || !s.isMember(channelID, leadUserID) {
		return nil, ErrInvalidRequest
	}
	if err := s.deps.Store.SetProjectLead(channelID, leadUserID); err != nil {
		return nil, err
	}
	if project.CheckinCron != "" && project.NextCheckinAt == 0 {
		if err := s.resumeCheckins(project); err != nil {
			s.deps.Platform.LogError("Failed to resume project check-ins", "channel_id", channelID, "error", err)
		}
	}
	return s.reloadAndPublish(channelID)
}

// resumeCheckins schedules the next check-in of a project whose check-ins
// were paused, for example because its lead left.
func (s *Service) resumeCheckins(project *store.Project) error {
	schedule, err := ParseSchedule(project.CheckinCron, project.CheckinTimezone)
	if err != nil {
		return err
	}
	next, ok := schedule.Next(time.Now())
	if !ok {
		return errors.New("the schedule never runs")
	}
	return s.deps.Store.SetProjectSchedule(project.ChannelID, project.CheckinCron, project.CheckinTimezone, next.UnixMilli())
}

// UpdateSchedule changes the check-in schedule. An empty cron turns
// check-ins off.
func (s *Service) UpdateSchedule(_ context.Context, actorID, channelID, cron, timezone string) (*store.Project, error) {
	project, err := s.deps.Store.GetProject(channelID)
	if err != nil {
		return nil, err
	}
	if !project.Live() {
		return nil, ErrNotActive
	}
	if !s.canLead(actorID, project) {
		return nil, ErrForbidden
	}
	cron = strings.TrimSpace(cron)
	var nextAt int64
	if cron != "" {
		schedule, err := ParseSchedule(cron, timezone)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
		}
		next, ok := schedule.Next(time.Now())
		if !ok {
			return nil, fmt.Errorf("%w: the schedule never runs", ErrInvalidRequest)
		}
		nextAt = next.UnixMilli()
	} else {
		timezone = ""
	}
	if err := s.deps.Store.SetProjectSchedule(channelID, cron, timezone, nextAt); err != nil {
		return nil, err
	}
	return s.reloadAndPublish(channelID)
}

// GetChecklistSummary returns the linked checklist run as the viewer sees it.
func (s *Service) GetChecklistSummary(ctx context.Context, viewerID, channelID string) (*ChecklistSummary, error) {
	project, err := s.GetProject(ctx, viewerID, channelID)
	if err != nil {
		return nil, err
	}
	if project.PlaybookRunID == "" || s.deps.Playbooks == nil {
		return &ChecklistSummary{RunID: project.PlaybookRunID}, nil
	}
	summary, err := s.deps.Playbooks.RunSummary(ctx, project.PlaybookRunID, viewerID)
	if err != nil {
		return nil, err
	}
	summary.RunID = project.PlaybookRunID
	return summary, nil
}

func (s *Service) reloadAndPublish(channelID string) (*store.Project, error) {
	project, err := s.deps.Store.GetProject(channelID)
	if err != nil {
		return nil, err
	}
	s.publish(project)
	return project, nil
}

func (s *Service) broadcast(channelID string) {
	project, err := s.deps.Store.GetProject(channelID)
	if err != nil {
		s.deps.Platform.LogError("Failed to read project for broadcast", "channel_id", channelID, "error", err)
		return
	}
	s.publish(project)
}

func (s *Service) publish(project *store.Project) {
	if project == nil {
		return
	}
	// Plugin RPC gob-encodes the payload, so it carries only plain values.
	raw, err := json.Marshal(s.ProjectResponse(project))
	if err != nil {
		s.deps.Platform.LogError("Failed to encode project update", "channel_id", project.ChannelID, "error", err)
		return
	}
	var flat map[string]any
	if err := json.Unmarshal(raw, &flat); err != nil {
		s.deps.Platform.LogError("Failed to encode project update", "channel_id", project.ChannelID, "error", err)
		return
	}
	payload := map[string]any{"project": flat}
	s.deps.Platform.PublishWebSocketEvent(WebSocketEventProjectUpdated, payload, &model.WebsocketBroadcast{ChannelId: project.ChannelID})
}

// liveProjectFor returns the live project bot leads, failing closed when the
// feature is off, the project is not running, or the bot belongs to any
// channel outside the project.
func (s *Service) liveProjectFor(bot *bots.Bot) (*store.Project, error) {
	if !s.enabled() || !bot.IsProjectAgent() {
		return nil, ErrDisabled
	}
	cfg := bot.GetConfig()
	project, err := s.deps.Store.GetProject(cfg.ProjectChannelID)
	if err != nil {
		return nil, err
	}
	if project.AgentID != cfg.ID || project.AgentBotUserID != bot.BotUserID() {
		return nil, ErrForbidden
	}
	if project.State != store.ProjectStateActive && project.State != store.ProjectStateEnding {
		return nil, ErrNotActive
	}
	memberships, err := s.deps.Platform.BotChannelIDs(bot.BotUserID())
	if err != nil {
		return nil, fmt.Errorf("failed to check the project agent's channels: %w", err)
	}
	allowed := []string{project.ChannelID}
	if project.BoardChannelID != "" {
		allowed = append(allowed, project.BoardChannelID)
	}
	for _, id := range memberships {
		if !slices.Contains(allowed, id) {
			return nil, ErrExtraMembership
		}
	}
	return project, nil
}

// CheckTurn implements conversations.ProjectGate.
func (s *Service) CheckTurn(_ context.Context, bot *bots.Bot, channel *model.Channel, user *model.User) error {
	project, err := s.liveProjectFor(bot)
	if err != nil {
		return err
	}
	if channel == nil || channel.Id != project.ChannelID || channel.DeleteAt != 0 {
		return ErrForbidden
	}
	if user == nil || user.IsBot && user.Id != bot.BotUserID() {
		return ErrForbidden
	}
	if user.Id != bot.BotUserID() && !s.isMember(project.ChannelID, user.Id) {
		return ErrForbidden
	}
	return nil
}

// SystemPromptSuffix implements conversations.ProjectGate.
func (s *Service) SystemPromptSuffix(_ context.Context, bot *bots.Bot) string {
	if bot == nil || !bot.IsProjectAgent() {
		return ""
	}
	project, err := s.deps.Store.GetProject(bot.GetConfig().ProjectChannelID)
	if err != nil || !project.Live() {
		return ""
	}
	return FormatStatusSuffix(project)
}

// Scope implements llmcontext.ProjectRuntime.
func (s *Service) Scope(_ context.Context, bot *bots.Bot, channel *model.Channel) (llmcontext.ProjectScope, error) {
	project, err := s.liveProjectFor(bot)
	if err != nil {
		return llmcontext.ProjectScope{}, err
	}
	if channel == nil || channel.Id != project.ChannelID {
		return llmcontext.ProjectScope{}, llmcontext.ErrProjectTurnUnavailable
	}
	scope := llmcontext.ProjectScope{
		WriteChannelIDs:        []string{project.ChannelID},
		BotPluginOrigins:       []string{BoardsOrigin, PlaybooksOrigin},
		RequesterPluginOrigins: []string{CursorOrigin},
	}
	if project.BoardChannelID != "" {
		scope.ExtraChannelIDs = []string{project.BoardChannelID}
		scope.WriteChannelIDs = append(scope.WriteChannelIDs, project.BoardChannelID)
	}
	return scope, nil
}

// BindPluginTools implements llmcontext.ProjectRuntime.
func (s *Service) BindPluginTools(_ context.Context, bot *bots.Bot, llmCtx *llm.Context, tools []llm.Tool) []llm.Tool {
	project, err := s.liveProjectFor(bot)
	if err != nil {
		return nil
	}
	interactive := llmCtx != nil && llmCtx.ToolCatalog.InteractiveUserPresent
	return NewBinder(project, interactive, s.binderCallbacks(bot)).Bind(tools)
}

func (s *Service) binderCallbacks(bot *bots.Bot) BinderCallbacks {
	return BinderCallbacks{
		Current: s.deps.Store.GetProject,
		BoardCreated: func(_ context.Context, project *store.Project, boardChannelID string) error {
			if err := s.deps.Store.SetProjectBoard(project.ChannelID, boardChannelID); err != nil {
				return err
			}
			if err := s.grantChannelAdmin(project.ChannelID, boardChannelID, bot.BotUserID()); err != nil {
				return err
			}
			s.broadcast(project.ChannelID)
			return nil
		},
		RunCreated: func(_ context.Context, project *store.Project, runID string) error {
			if err := s.deps.Store.SetProjectRun(project.ChannelID, runID); err != nil {
				return err
			}
			s.broadcast(project.ChannelID)
			return nil
		},
	}
}

// OperationTools implements conversations.OperationTools for the project
// operations.
func (s *Service) OperationTools(_ context.Context, bot *bots.Bot, llmCtx *llm.Context) []llm.Tool {
	if _, err := s.liveProjectFor(bot); err != nil {
		return nil
	}
	return s.ToolsForRequest(bot, llmCtx)
}

// Claims implements agentsetup.Extension for project agents.
func (s *Service) Claims(agent *llm.BotConfig) bool {
	return agent != nil && agent.IsProjectAgent()
}

// AutoExecute implements agentsetup.Extension: a project agent writes its
// own name and instructions without asking, within what agentconfig allows.
func (s *Service) AutoExecute(agent *llm.BotConfig, _ string) bool {
	return s.Claims(agent) && s.enabled()
}

// Instructions implements agentsetup.Extension.
func (s *Service) Instructions(agent *llm.BotConfig) string {
	if !s.Claims(agent) {
		return ""
	}
	return setupInstructions
}

// Tools implements agentsetup.Extension.
func (s *Service) Tools(ctx context.Context, agent *llm.BotConfig, llmCtx *llm.Context) []llm.Tool {
	if !s.Claims(agent) {
		return nil
	}
	bot := s.deps.Bots.GetBotByID(agent.BotUserID)
	if bot == nil || bot.GetConfig().ID != agent.ID {
		return nil
	}
	return s.OperationTools(ctx, bot, llmCtx)
}

// ArtifactAutoExecute reports whether a channel artifact tool runs unasked
// for bot: a project agent manages its own project's library.
func (s *Service) ArtifactAutoExecute(bot *bots.Bot, llmCtx *llm.Context) bool {
	if llmCtx == nil || llmCtx.Channel == nil {
		return false
	}
	project, err := s.liveProjectFor(bot)
	return err == nil && llmCtx.Channel.Id == project.ChannelID
}

// ArtifactActor is who project writes to the artifact library are credited
// to when no session is present: the project bot.
func (s *Service) ArtifactActor(project *store.Project) channelartifacts.Actor {
	return channelartifacts.Actor{UserID: project.AgentBotUserID, BotUserID: project.AgentBotUserID}
}

// Authorizer wraps base so a live project's lead, and whoever manages the
// project channel's roles, may manage its agent's non-sensitive settings.
// Sensitive settings stay with base.
func (s *Service) Authorizer(base agentconfig.Authorizer) agentconfig.Authorizer {
	return projectAuthorizer{base: base, service: s}
}

type projectAuthorizer struct {
	base    agentconfig.Authorizer
	service *Service
}

func (a projectAuthorizer) CanManage(ctx context.Context, actorID string, agent *llm.BotConfig) bool {
	if agent == nil || !agent.IsProjectAgent() {
		return a.base != nil && a.base.CanManage(ctx, actorID, agent)
	}
	if !a.service.enabled() {
		return false
	}
	project, err := a.service.deps.Store.GetProject(agent.ProjectChannelID)
	if err != nil || !project.Live() || project.AgentID != agent.ID {
		return false
	}
	return a.service.canLead(actorID, project)
}

func (a projectAuthorizer) CanManageServiceAccounts(ctx context.Context, actorID string, agent *llm.BotConfig) bool {
	return a.base != nil && a.base.CanManageServiceAccounts(ctx, actorID, agent)
}
