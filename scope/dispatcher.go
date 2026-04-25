// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scope

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"text/template"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
)

// PublishingContract is prepended to every scoped run's prompt so the LLM
// understands it must act via create_post (or another post-writing tool) to
// produce any visible output. Channel-automation's ai_prompt does not
// auto-post the model's reply; the contract makes that explicit.
const PublishingContract = `You are running without a human in the conversation. Any plain-text reply you return is discarded and will not be seen by anyone. To publish anything, call the create_post tool directly. The channel_id, channel_display_name, and team_display_name parameters are fixed by the system; you cannot change them and must not call get_channel_info first.

`

// MaxPerAgentFiresPerMinute bounds how many dispatches one agent can do per
// rolling minute. Beyond this, additional fires are dropped.
const MaxPerAgentFiresPerMinute = 10

// ToolStoreBuilder builds the full, un-scoped tool store for a bot. The
// dispatcher applies ApplyToolScope to this store before each run.
type ToolStoreBuilder interface {
	BuildLLMContextUserRequest(bot *bots.Bot, requestingUser *model.User, channel *model.Channel, opts ...llm.ContextOption) *llm.Context
	WithLLMContextTools(bot *bots.Bot) llm.ContextOption
}

// Ensure llmcontext.Builder satisfies ToolStoreBuilder at compile time.
var _ ToolStoreBuilder = (*llmcontext.Builder)(nil)

// AgentGetter loads an agent by ID for dispatch.
type AgentGetter interface {
	GetAgent(id string) (*llm.BotConfig, error)
}

// BotRegistry gets a live Bot handle (with wired LanguageModel) by bot user ID.
type BotRegistry interface {
	GetBotByID(botID string) *bots.Bot
}

// Dispatcher coordinates scoped, stateless agent runs. One instance per
// plugin, shared by SubscriptionsService and Scheduler.
type Dispatcher struct {
	agents         AgentGetter
	botRegistry    BotRegistry
	contextBuilder ToolStoreBuilder
	convService    *conversation.Service
	mmClient       mmapi.Client
	pluginAPI      *pluginapi.Client
	log            Logger

	// perAgent tracks concurrency (cap 1 per agent) and rate-limit state.
	mu       sync.Mutex
	inflight map[string]struct{}
	fires    map[string][]time.Time // rolling window
}

// NewDispatcher wires up a Dispatcher. All arguments are required.
func NewDispatcher(
	agents AgentGetter,
	botRegistry BotRegistry,
	contextBuilder ToolStoreBuilder,
	convService *conversation.Service,
	mmClient mmapi.Client,
	pluginAPI *pluginapi.Client,
	log Logger,
) *Dispatcher {
	return &Dispatcher{
		agents:         agents,
		botRegistry:    botRegistry,
		contextBuilder: contextBuilder,
		convService:    convService,
		mmClient:       mmClient,
		pluginAPI:      pluginAPI,
		log:            log,
		inflight:       make(map[string]struct{}),
		fires:          make(map[string][]time.Time),
	}
}

// admitFire returns true iff this agent has no in-flight dispatch and has
// fired fewer than MaxPerAgentFiresPerMinute times in the last minute.
// On success it records a new fire timestamp and marks the agent in-flight.
func (d *Dispatcher) admitFire(agentID string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, busy := d.inflight[agentID]; busy {
		return false
	}

	// Trim old fire timestamps (older than 60s).
	cutoff := now.Add(-time.Minute)
	recent := d.fires[agentID][:0]
	for _, t := range d.fires[agentID] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= MaxPerAgentFiresPerMinute {
		d.fires[agentID] = recent
		return false
	}

	recent = append(recent, now)
	d.fires[agentID] = recent
	d.inflight[agentID] = struct{}{}
	return true
}

func (d *Dispatcher) releaseFire(agentID string) {
	d.mu.Lock()
	delete(d.inflight, agentID)
	d.mu.Unlock()
}

// DispatchSubscription fires a subscription in response to a new post.
// post and postingUser are the inbound trigger; channel is the scope channel.
func (d *Dispatcher) DispatchSubscription(
	ctx context.Context,
	agentID string,
	sub llm.AgentSubscription,
	post *model.Post,
	postingUser *model.User,
	scopeChannel *model.Channel,
) {
	now := time.Now()
	if !d.admitFire(agentID, now) {
		d.log.Warn("scope: dispatch dropped (rate/concurrency)", "agent", agentID, "trigger", "subscription", "id", sub.ID)
		return
	}
	defer d.releaseFire(agentID)

	vars := llm.TriggerVars{
		TargetChannelID: sub.TargetChannelID,
		Now:             now.UTC().Format(time.RFC3339),
	}
	if post != nil {
		vars.PostID = post.Id
		vars.ChannelID = post.ChannelId
		vars.UserID = post.UserId
		vars.Message = post.Message
	}
	if postingUser != nil {
		vars.Username = postingUser.Username
	}

	rootPostID := fmt.Sprintf("sub-%s-%s", sub.ID, vars.PostID)
	d.run(ctx, runInput{
		agentID:         agentID,
		prompt:          sub.Prompt,
		vars:            vars,
		allowedTools:    sub.AllowedTools,
		boundParams:     sub.BoundParams,
		targetChannelID: sub.TargetChannelID,
		scopeChannel:    scopeChannel,
		rootPostID:      rootPostID,
		triggerLabel:    fmt.Sprintf("subscription/%s", sub.ID),
	})
}

// DispatchSchedule fires a schedule at a scheduler tick.
// firedAt is the tick timestamp used for the idempotent RootPostID.
func (d *Dispatcher) DispatchSchedule(
	ctx context.Context,
	agentID string,
	sched llm.AgentSchedule,
	firedAt time.Time,
) {
	if !d.admitFire(agentID, firedAt) {
		d.log.Warn("scope: dispatch dropped (rate/concurrency)", "agent", agentID, "trigger", "schedule", "id", sched.ID)
		return
	}
	defer d.releaseFire(agentID)

	vars := llm.TriggerVars{
		TargetChannelID: sched.TargetChannelID,
		Now:             firedAt.UTC().Format(time.RFC3339),
	}

	rootPostID := fmt.Sprintf("sched-%s-%d", sched.ID, firedAt.Unix())
	d.run(ctx, runInput{
		agentID:         agentID,
		prompt:          sched.Prompt,
		vars:            vars,
		allowedTools:    sched.AllowedTools,
		boundParams:     sched.BoundParams,
		targetChannelID: sched.TargetChannelID,
		rootPostID:      rootPostID,
		triggerLabel:    fmt.Sprintf("schedule/%s", sched.ID),
	})
}

type runInput struct {
	agentID         string
	prompt          string
	vars            llm.TriggerVars
	allowedTools    []string
	boundParams     map[string]map[string]interface{}
	targetChannelID string
	scopeChannel    *model.Channel // optional, for subscription runs
	rootPostID      string
	triggerLabel    string
}

func (d *Dispatcher) run(ctx context.Context, in runInput) {
	cfg, err := d.agents.GetAgent(in.agentID)
	if err != nil {
		d.log.Error("scope: failed to load agent", "agent", in.agentID, "trigger", in.triggerLabel, "error", err.Error())
		return
	}
	if cfg == nil {
		d.log.Warn("scope: dispatched agent not found or deleted", "agent", in.agentID, "trigger", in.triggerLabel)
		return
	}

	bot := d.botRegistry.GetBotByID(cfg.BotUserID)
	if bot == nil {
		d.log.Warn("scope: bot not loaded", "agent", in.agentID, "bot_user_id", cfg.BotUserID)
		return
	}
	botUser, err := d.pluginAPI.User.Get(cfg.BotUserID)
	if err != nil || botUser == nil {
		d.log.Warn("scope: bot user not found", "agent", in.agentID, "bot_user_id", cfg.BotUserID, "error", errString(err))
		return
	}

	// Dispatch-time permission check: bot must still be a member of target channel.
	if _, err := d.pluginAPI.Channel.GetMember(in.targetChannelID, cfg.BotUserID); err != nil {
		d.log.Warn("scope: bot is not a member of target channel, skipping",
			"agent", in.agentID, "bot_user_id", cfg.BotUserID, "target_channel", in.targetChannelID, "error", err.Error())
		return
	}

	targetChannel, err := d.mmClient.GetChannel(in.targetChannelID)
	if err != nil || targetChannel == nil {
		d.log.Error("scope: failed to fetch target channel", "agent", in.agentID, "target_channel", in.targetChannelID, "error", errString(err))
		return
	}
	targetTeam, err := d.pluginAPI.Team.Get(targetChannel.TeamId)
	if err != nil || targetTeam == nil {
		d.log.Error("scope: failed to fetch target team", "agent", in.agentID, "target_channel", in.targetChannelID, "team_id", targetChannel.TeamId, "error", errString(err))
		return
	}

	renderedPrompt, err := renderPrompt(in.prompt, in.vars)
	if err != nil {
		d.log.Error("scope: prompt render failed", "agent", in.agentID, "trigger", in.triggerLabel, "error", err.Error())
		return
	}

	// Build LLM context against the target channel so tool resolution
	// references the channel the bot will post to.
	llmCtx := d.contextBuilder.BuildLLMContextUserRequest(
		bot,
		botUser,
		targetChannel,
		d.contextBuilder.WithLLMContextTools(bot),
	)
	if llmCtx == nil {
		d.log.Error("scope: nil LLM context", "agent", in.agentID, "trigger", in.triggerLabel)
		return
	}
	llmCtx.Tools = ApplyToolScopeWithTarget(llmCtx.Tools, in.allowedTools, in.boundParams, in.targetChannelID, targetChannel, targetTeam, nil)

	systemPrompt := PublishingContract + renderedPrompt

	// Fresh stateless conversation. Keyed by synthetic RootPostID so the
	// same trigger+bucket never creates two conversations.
	convResult, err := d.convService.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       cfg.BotUserID, // self: no real user drives the run
		BotID:        cfg.BotUserID,
		ChannelID:    in.targetChannelID,
		RootPostID:   in.rootPostID,
		Operation:    "scoped_run",
		SystemPrompt: systemPrompt,
		UserMessage:  "Begin.",
	})
	if err != nil {
		d.log.Error("scope: conversation setup failed", "agent", in.agentID, "trigger", in.triggerLabel, "error", err.Error())
		return
	}

	request, err := d.convService.BuildCompletionRequest(convResult.Conversation, llmCtx)
	if err != nil {
		d.log.Error("scope: completion request build failed", "agent", in.agentID, "trigger", in.triggerLabel, "error", err.Error())
		return
	}

	shouldExecute := BuildScopedShouldExecute(in.allowedTools, d.log)
	runner := toolrunner.New(bot.LLM())
	result, err := runner.Run(*request, shouldExecute, nil)
	if err != nil {
		d.log.Error("scope: initial LLM call failed", "agent", in.agentID, "trigger", in.triggerLabel, "error", err.Error())
		return
	}

	// Drain the stream. Scoped runs don't stream to a user — tools either
	// acted or they didn't; the final text goes nowhere.
	var streamErr error
	for event := range result.Stream.Stream {
		if event.Type == llm.EventTypeError {
			if e, ok := event.Value.(error); ok {
				streamErr = e
			}
		}
	}
	if streamErr != nil {
		d.log.Error("scope: stream error", "agent", in.agentID, "trigger", in.triggerLabel, "error", streamErr.Error())
		return
	}

	// Record best-effort "did we actually post anything?" signal.
	// For now, we log; persistence of LastFireAt/LastError is a follow-up
	// (needs a narrow store update to avoid racing UpdateAgent).
	posted := postWritingToolWasCalled(result.ToolTurns)
	if !posted {
		d.log.Info("scope: run completed without calling a post-writing tool (output discarded)",
			"agent", in.agentID, "trigger", in.triggerLabel)
	} else {
		d.log.Info("scope: run completed", "agent", in.agentID, "trigger", in.triggerLabel)
	}

	_ = ctx // reserved for future cancellation paths
}

// postWritingToolWasCalled reports whether any tool turn included a call to
// a tool that produces a post. The set is intentionally small and explicit —
// growing it as new post-writing tools are added.
func postWritingToolWasCalled(turns []toolrunner.ToolTurn) bool {
	for i := range turns {
		for _, tc := range turns[i].AssistantToolCalls {
			switch tc.Name {
			case "create_post", "create_post_in_thread":
				return true
			}
		}
	}
	return false
}

func renderPrompt(raw string, vars llm.TriggerVars) (string, error) {
	tpl, err := template.New("trigger-prompt").Option("missingkey=error").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("execute: %w", err)
	}
	return buf.String(), nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// DispatchSubscriptionInput and DispatchScheduleInput errors are sentinel
// values callers may check; today nothing returns them but the package
// reserves the names for future non-silent dispatch paths.
var (
	errAgentNotFound = errors.New("agent not found")
	_                = errAgentNotFound
)
