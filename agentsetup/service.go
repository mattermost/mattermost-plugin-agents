// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package agentsetup runs agent auto-setup: a conversation in which an agent
// interviews the person managing it and writes its own display name,
// instructions, and tool settings with setup-only tools.
package agentsetup

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentconfig"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost/server/public/model"
)

// RootPostAgentIDProp records, on a setup thread's root post, the agent the
// thread sets up. It is informational: setup conversations are recognized by
// their server-side operation, never by post props.
const RootPostAgentIDProp = "agent_setup_agent_id"

// extraInstructionsParam is the prompt parameter extension instructions are
// rendered from.
const extraInstructionsParam = "SetupExtraInstructions"

var (
	// ErrDisabled means agent auto-setup is turned off for the agent.
	ErrDisabled = errors.New("agent auto-setup is turned off")
	// ErrAgentNotRunning means the agent has no running bot to talk to, for
	// example because its service is unavailable.
	ErrAgentNotRunning = errors.New("the agent is not running, so it cannot be set up by chatting with it")
)

// Config reads the plugin settings auto-setup depends on.
type Config interface {
	EnableAgentAutoSetup() bool
}

// Store records setup completion.
type Store interface {
	MarkAgentSetupCompleted(agentID string, completedAt int64) (bool, error)
}

// Bots looks up running agents.
type Bots interface {
	GetBotByID(botUserID string) *bots.Bot
}

// Threads starts agent threads.
type Threads interface {
	StartAgentThread(ctx context.Context, req conversations.AgentThreadRequest) (*conversations.AgentThread, error)
}

// AuditRecorder emits server audit records.
type AuditRecorder interface {
	Record(rec *model.AuditRecord)
}

// Deps are the Service's collaborators. Every field is required.
type Deps struct {
	Configs *agentconfig.Service
	Threads Threads
	Bots    Bots
	Store   Store
	Config  Config
	Prompts *llm.Prompts
	I18n    *i18n.Bundle
	Audit   AuditRecorder
}

// Service starts setup conversations and supplies their tools.
type Service struct {
	deps Deps

	extensionsMu sync.RWMutex
	extensions   []Extension
}

// New returns a Service. Register it with conversations.RegisterOperation for
// llm.OperationAgentSetup so setup conversations get their tools.
func New(deps Deps) *Service {
	if deps.Configs == nil || deps.Threads == nil || deps.Bots == nil || deps.Store == nil ||
		deps.Config == nil || deps.Prompts == nil || deps.I18n == nil || deps.Audit == nil {
		panic("agentsetup: New requires every dependency")
	}
	return &Service{deps: deps}
}

// RegisterExtension adds ext. The first registered extension that claims an
// agent handles it.
func (s *Service) RegisterExtension(ext Extension) {
	s.extensionsMu.Lock()
	defer s.extensionsMu.Unlock()
	s.extensions = append(s.extensions, ext)
}

func (s *Service) extensionFor(agent *llm.BotConfig) Extension {
	s.extensionsMu.RLock()
	defer s.extensionsMu.RUnlock()
	for _, ext := range s.extensions {
		if ext.Claims(agent) {
			return ext
		}
	}
	return nil
}

// enabledFor reports whether agent may be set up, and the extension that
// claims it, if any.
func (s *Service) enabledFor(agent *llm.BotConfig) (Extension, bool) {
	if ext := s.extensionFor(agent); ext != nil {
		return ext, true
	}
	return nil, s.deps.Config.EnableAgentAutoSetup()
}

// Available reports whether actorID could start setting up agent now, for
// clients deciding whether to offer it.
func (s *Service) Available(ctx context.Context, actorID string, agent *llm.BotConfig) bool {
	if agent == nil || agent.ID == "" || agent.BotUserID == "" {
		return false
	}
	if _, enabled := s.enabledFor(agent); !enabled {
		return false
	}
	return s.deps.Configs.Authorizer().CanManage(ctx, actorID, agent)
}

// Start opens a setup thread with agentID in actor's DM with it. The agent
// answers in the background. ctx must outlive the request and carry the
// actor's session ID. Errors are agentconfig errors, ErrDisabled,
// ErrAgentNotRunning, or the turn policy's refusal (bots.ErrUsageRestriction).
func (s *Service) Start(ctx context.Context, actor *model.User, agentID string) (*conversations.AgentThread, error) {
	agent, err := s.deps.Configs.Get(ctx, actor.Id, agentID)
	if err != nil {
		return nil, err
	}
	ext, enabled := s.enabledFor(agent)
	if !enabled {
		return nil, ErrDisabled
	}
	bot := s.deps.Bots.GetBotByID(agent.BotUserID)
	if bot == nil || bot.GetConfig().ID != agent.ID {
		return nil, ErrAgentNotRunning
	}

	displayName := agent.DisplayName
	if displayName == "" {
		displayName = agent.Name
	}
	message := i18n.LocalizerFunc(s.deps.I18n, actor.Locale)("agents.agent_setup_root_message", "Help me set up %s.", displayName)

	extraInstructions := ""
	if ext != nil {
		extraInstructions = ext.Instructions(agent)
	}
	return s.deps.Threads.StartAgentThread(ctx, conversations.AgentThreadRequest{
		Bot:        bot,
		User:       actor,
		Operation:  llm.OperationAgentSetup,
		Message:    message,
		PostAsUser: true,
		RootProps:  map[string]any{RootPostAgentIDProp: agent.ID},
		SystemPrompt: func(llmCtx *llm.Context) (string, error) {
			return s.systemPrompt(llmCtx, extraInstructions)
		},
	})
}

// SystemPrompt renders the setup system prompt with extra instructions, for
// setup conversations started outside the requester's DM.
func (s *Service) SystemPrompt(llmCtx *llm.Context, extraInstructions string) (string, error) {
	return s.systemPrompt(llmCtx, extraInstructions)
}

func (s *Service) systemPrompt(llmCtx *llm.Context, extraInstructions string) (string, error) {
	if extraInstructions != "" {
		if llmCtx.Parameters == nil {
			llmCtx.Parameters = make(map[string]any)
		}
		llmCtx.Parameters[extraInstructionsParam] = extraInstructions
	}
	prompt, err := s.deps.Prompts.Format(prompts.PromptAgentSetupSystem, llmCtx)
	if err != nil {
		return "", fmt.Errorf("failed to format agent setup system prompt: %w", err)
	}
	return prompt, nil
}

// isRequesterDM reports whether llmCtx runs in the requester's own DM with
// botUserID.
func isRequesterDM(botUserID string, llmCtx *llm.Context) bool {
	channel := llmCtx.Channel
	return channel != nil && channel.Type == model.ChannelTypeDirect &&
		channel.Name == model.GetDMNameFromIds(botUserID, llmCtx.RequestingUser.Id)
}

// inClaimedProjectChannel reports whether llmCtx is an attended turn in the
// project channel of a project agent an extension claims.
func (s *Service) inClaimedProjectChannel(agent *llm.BotConfig, llmCtx *llm.Context) bool {
	return agent.IsProjectAgent() && llmCtx.Channel != nil && llmCtx.Channel.Id == agent.ProjectChannelID &&
		llmCtx.ToolCatalog.InteractiveUserPresent && s.extensionFor(agent) != nil
}

// OperationTools returns the setup tools for a turn of a setup conversation.
// They are offered only in the requester's DM with a user-created agent the
// requester may manage, or, for a project agent, in an attended turn in its
// project channel, while setup is enabled for it; each call re-checks
// authorization at the time it runs.
func (s *Service) OperationTools(ctx context.Context, bot *bots.Bot, llmCtx *llm.Context) []llm.Tool {
	if bot == nil || llmCtx == nil || llmCtx.RequestingUser == nil {
		return nil
	}
	agent := bot.GetConfig()
	if agent.ID == "" || (!isRequesterDM(bot.BotUserID(), llmCtx) && !s.inClaimedProjectChannel(&agent, llmCtx)) {
		return nil
	}
	ext, enabled := s.enabledFor(&agent)
	if !enabled || !s.deps.Configs.Authorizer().CanManage(ctx, llmCtx.RequestingUser.Id, &agent) {
		return nil
	}

	// Outside the requester's DM other people's posts are in context, so only
	// someone the extension trusts with the channel gets the setup tools there.
	if !isRequesterDM(bot.BotUserID(), llmCtx) &&
		(ext == nil || !ext.CanSetUpInChannel(ctx, &agent, llmCtx.RequestingUser.Id)) {
		return nil
	}

	tools := s.setupTools(agent.ID)
	if !llmCtx.ToolCatalog.InteractiveUserPresent {
		// The writes run without approval, so they need the requester present.
		tools = slices.DeleteFunc(tools, func(tool llm.Tool) bool { return tool.Name != ToolGetMyConfig })
	}
	if ext != nil {
		tools = append(tools, ext.Tools(ctx, &agent, llmCtx)...)
	}
	return tools
}

// KeepOperationTool narrows the catalog of a setup turn so content the
// agent's other tools fetch cannot reach a turn that saves the agent's
// instructions on its own. In a direct message only the setup tools remain;
// elsewhere the built-in web search and fetch are dropped.
func (s *Service) KeepOperationTool(_ context.Context, _ *bots.Bot, llmCtx *llm.Context, tool llm.Tool) bool {
	inDM := llmCtx == nil || llmCtx.Channel == nil || llmCtx.Channel.Type == model.ChannelTypeDirect
	switch {
	case inDM:
		return tool.ServerOrigin == "" && slices.Contains(setupToolNames, tool.Name)
	case tool.ServerOrigin != "":
		return true
	default:
		return tool.Name != mmtools.WebSearchToolName && tool.Name != mmtools.WebSearchSourceToolName
	}
}
