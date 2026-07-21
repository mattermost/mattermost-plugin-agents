// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package delegation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	// defaultSubTurnRunTimeout bounds one delegated sub-turn run segment (LLM
	// streaming plus auto-executed tool rounds). Generous by design: approval
	// waits happen after the stream ends and are not under this timeout.
	defaultSubTurnRunTimeout = 30 * time.Minute

	// defaultMaxLifetime is a safety ceiling for the whole delegation,
	// including waits on the initiator. Purely an abuse/leak backstop — the
	// parent otherwise waits for as long as the sub-agent needs.
	defaultMaxLifetime = 24 * time.Hour

	// defaultKeepaliveInterval refreshes the initiator's MCP client activity
	// so the idle sweep cannot sever the in-flight ask_agent call.
	defaultKeepaliveInterval = 5 * time.Minute
)

// Metrics is the subset of the plugin metrics service used by delegation.
type Metrics interface {
	ObserveDelegation(sourceBot, targetBot, outcome string)
	ObserveDelegationDuration(seconds float64)
}

// ClusterNotifier broadcasts sub-turn completion to peer nodes, where the
// waiting parent may live when the approval HTTP request landed elsewhere.
type ClusterNotifier interface {
	PublishDelegationComplete(conversationID string) error
}

// ActivityToucher keeps the initiator's MCP clients alive during a delegation.
// Implemented by *mcp.ClientManager.
type ActivityToucher interface {
	TouchUserActivity(userID string)
}

// Service runs agent-to-agent delegations. It is constructed early (before
// the embedded MCP server, so ask_agent can be registered) and completed
// later via Complete once the conversation machinery exists. Delegate fails
// with ErrNotConfigured until then.
type Service struct {
	mmClient  mmapi.Client
	bots      *bots.MMBots
	streaming streaming.Service
	prompts   *llm.Prompts
	i18n      *i18n.Bundle
	metrics   Metrics

	mu             sync.RWMutex
	convService    *conversation.Service
	conversations  *conversations.Conversations
	contextBuilder *llmcontext.Builder
	cluster        ClusterNotifier
	activity       ActivityToucher

	waiters *waiterRegistry

	subTurnRunTimeout time.Duration
	maxLifetime       time.Duration
	keepaliveInterval time.Duration
}

// New creates the delegation service skeleton with the dependencies available
// early in plugin activation.
func New(
	mmClient mmapi.Client,
	botsService *bots.MMBots,
	streamingService streaming.Service,
	promptsService *llm.Prompts,
	i18nBundle *i18n.Bundle,
	metricsService Metrics,
) *Service {
	return &Service{
		mmClient:          mmClient,
		bots:              botsService,
		streaming:         streamingService,
		prompts:           promptsService,
		i18n:              i18nBundle,
		metrics:           metricsService,
		waiters:           newWaiterRegistry(),
		subTurnRunTimeout: defaultSubTurnRunTimeout,
		maxLifetime:       defaultMaxLifetime,
		keepaliveInterval: defaultKeepaliveInterval,
	}
}

// Complete wires the late-constructed dependencies. Until it is called,
// Delegate returns ErrNotConfigured.
func (s *Service) Complete(
	convService *conversation.Service,
	conversationsService *conversations.Conversations,
	contextBuilder *llmcontext.Builder,
	cluster ClusterNotifier,
	activity ActivityToucher,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convService = convService
	s.conversations = conversationsService
	s.contextBuilder = contextBuilder
	s.cluster = cluster
	s.activity = activity
}

// Available reports whether the service is fully configured.
func (s *Service) Available() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.convService != nil && s.conversations != nil && s.contextBuilder != nil &&
		s.mmClient != nil && s.bots != nil && s.streaming != nil && s.prompts != nil
}

// serviceDeps is an immutable snapshot of the late-bound dependencies.
type serviceDeps struct {
	convService   *conversation.Service
	conversations *conversations.Conversations
	cluster       ClusterNotifier
	activity      ActivityToucher
}

func (s *Service) deps() (serviceDeps, error) {
	if !s.Available() {
		return serviceDeps{}, ErrNotConfigured
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return serviceDeps{
		convService:   s.convService,
		conversations: s.conversations,
		cluster:       s.cluster,
		activity:      s.activity,
	}, nil
}

// Delegate runs the full delegation pipeline: validate → surface → run →
// await → return. It blocks until the target agent produced its final answer
// (however long that takes, including waits on the initiator) and returns the
// model-visible result text. Errors carry model-visible guidance.
func (s *Service) Delegate(ctx context.Context, req Request) (string, error) {
	deps, err := s.deps()
	if err != nil {
		return "", err
	}

	start := time.Now()
	ctx, span := telemetry.Tracer().Start(ctx, "delegate to agent",
		trace.WithAttributes(
			telemetry.UserID.String(req.InitiatorUserID),
			telemetry.ToolID.String(req.ParentToolCallID),
		),
	)
	defer span.End()

	fail := func(sourceBotName, targetBotName, outcome string, failErr error) (string, error) {
		span.RecordError(failErr)
		span.SetStatus(otelcodes.Error, failErr.Error())
		if s.metrics != nil {
			s.metrics.ObserveDelegation(sourceBotName, targetBotName, outcome)
		}
		return "", failErr
	}

	sourceBot, targetBot, initiator, err := s.validate(req)
	if err != nil {
		return fail(botUsername(sourceBot), req.TargetAgent, "failed", err)
	}
	sourceBotName := botUsername(sourceBot)
	targetBotName := botUsername(targetBot)
	span.SetAttributes(
		telemetry.AgentID.String(targetBot.GetMMBot().UserId),
		telemetry.AgentName.String(targetBotName),
	)
	if s.metrics != nil {
		s.metrics.ObserveDelegation(sourceBotName, targetBotName, "started")
	}

	run, err := s.surface(ctx, deps, req, sourceBot, targetBot, initiator)
	if err != nil {
		return fail(sourceBotName, targetBotName, "failed", fmt.Errorf("failed to start the delegation: %w", err))
	}

	s.mmClient.LogDebug("Delegation started",
		"initiator_user_id", initiator.Id,
		"source_bot", sourceBotName,
		"target_bot", targetBotName,
		"delegation_id", run.record.DelegationID,
		"parent_tool_call_id", req.ParentToolCallID,
	)

	// Register the waiter before the sub-turn runs so a completion signal
	// from a fast approval can never slip between run and await.
	wake := s.waiters.register(run.record.DelegationID)
	defer s.waiters.deregister(run.record.DelegationID)

	// Keep the initiator's MCP clients alive for the duration: the parent's
	// in-flight ask_agent call rides on the embedded session, which the idle
	// sweep would otherwise close during long waits.
	stopKeepalive := s.startKeepalive(deps, initiator.Id)
	defer stopKeepalive()

	deadline := start.Add(s.maxLifetime)

	outcome, runErr := s.runSubTurn(ctx, deps, run)
	if runErr != nil {
		run.emit(PhaseFailed, "", "")
		return fail(sourceBotName, targetBotName, "failed",
			fmt.Errorf("the delegated agent failed to run: %s. The conversation may have partial results: %s", runErr.Error(), run.permalink))
	}

	finalText := outcome.FinalText
	if outcome.PendingApproval {
		finalText, err = s.await(ctx, deps, run, wake, deadline)
		if err != nil {
			return fail(sourceBotName, targetBotName, outcomeForAwaitError(err), err)
		}
	} else if strings.TrimSpace(finalText) == "" {
		run.emit(PhaseFailed, "", "")
		return fail(sourceBotName, targetBotName, "failed",
			fmt.Errorf("the delegated agent did not produce an answer. See the conversation: %s", run.permalink))
	}

	run.emit(PhaseCompleted, "", "")
	if s.metrics != nil {
		s.metrics.ObserveDelegation(sourceBotName, targetBotName, "completed")
		s.metrics.ObserveDelegationDuration(time.Since(start).Seconds())
	}
	s.mmClient.LogDebug("Delegation completed",
		"delegation_id", run.record.DelegationID,
		"duration_seconds", time.Since(start).Seconds(),
	)

	return format.DelegationResult(run.record.TargetBotDisplayName, run.record.TargetBotUsername, run.permalink, finalText), nil
}

func outcomeForAwaitError(err error) string {
	if strings.Contains(err.Error(), ErrTimedOut.Error()) {
		return "timed_out"
	}
	return "failed"
}

func botUsername(bot *bots.Bot) string {
	if bot == nil {
		return ""
	}
	return bot.GetConfig().Name
}

// validate resolves and authorizes the delegation participants.
func (s *Service) validate(req Request) (sourceBot *bots.Bot, targetBot *bots.Bot, initiator *model.User, err error) {
	if req.InitiatorUserID == "" {
		return nil, nil, nil, fmt.Errorf("%w: no authenticated user for this delegation", ErrAccessDenied)
	}

	sourceBot = s.bots.GetBotByID(req.DelegatingBotUserID)
	if sourceBot == nil {
		return nil, nil, nil, fmt.Errorf("%w: only agents on this server can delegate tasks", ErrAccessDenied)
	}

	// The initiator must be the human the delegation runs on behalf of.
	// Sub-turns never see ask_agent, so a bot initiator would indicate
	// something forged a session — refuse outright.
	initiator, userErr := s.mmClient.GetUser(req.InitiatorUserID)
	if userErr != nil {
		return nil, nil, nil, fmt.Errorf("%w: unable to resolve the requesting user", ErrAccessDenied)
	}
	if initiator.IsBot || s.bots.IsAnyBot(initiator.Id) {
		return nil, nil, nil, fmt.Errorf("%w: delegation requires a human initiator", ErrAccessDenied)
	}

	target := strings.TrimPrefix(strings.TrimSpace(req.TargetAgent), "@")
	targetBot = s.bots.GetBotByUsername(target)
	if targetBot == nil {
		targetBot = s.bots.GetBotByID(target)
	}
	if targetBot == nil {
		return nil, nil, nil, fmt.Errorf("%w: no agent named %q. Use list_agents to discover available agents", ErrUnknownAgent, req.TargetAgent)
	}

	if targetBot.GetMMBot().UserId == sourceBot.GetMMBot().UserId {
		return nil, nil, nil, fmt.Errorf("%w: answer directly instead, or pick a different agent with list_agents", ErrSelfDelegation)
	}

	if restrictionErr := s.bots.CheckUsageRestrictionsForUser(targetBot, initiator.Id); restrictionErr != nil {
		return nil, nil, nil, fmt.Errorf("%w: @%s cannot be used by this user", ErrAccessDenied, targetBot.GetConfig().Name)
	}

	return sourceBot, targetBot, initiator, nil
}

// delegationRun bundles the per-delegation state created by surface.
type delegationRun struct {
	svc          *Service
	record       Record
	initiator    *model.User
	targetBot    *bots.Bot
	dmChannel    *model.Channel
	responsePost *model.Post
	llmContext   *llm.Context
	permalink    string
}

func (r *delegationRun) emit(phase, activity, tools string) {
	emitUpdate(r.svc.mmClient, r.record, phase, activity, tools, r.permalink)
}

// surface creates the visible delegation artifacts: the labeled task post in
// the initiator's DM with the target agent, the delegation conversation, the
// KV record, and the response placeholder the sub-turn streams into.
func (s *Service) surface(ctx context.Context, deps serviceDeps, req Request, sourceBot, targetBot *bots.Bot, initiator *model.User) (*delegationRun, error) {
	targetBotUserID := targetBot.GetMMBot().UserId

	dmChannel, err := s.mmClient.GetDirectChannel(initiator.Id, targetBotUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get the DM channel with the target agent: %w", err)
	}

	// Labeled task root post, authored by the target agent.
	T := i18n.LocalizerFunc(s.i18n, initiator.Locale)
	taskPost := &model.Post{
		UserId:    targetBotUserID,
		ChannelId: dmChannel.Id,
		Message: fmt.Sprintf("%s\n\n%s",
			T("agents.delegation_task_from", "Task from @%s on behalf of @%s:", sourceBot.GetConfig().Name, initiator.Username),
			req.Task,
		),
	}
	taskPost.AddProp("delegation_from_bot_id", sourceBot.GetMMBot().UserId)
	taskPost.AddProp(streaming.UnsafeLinksPostProp, "true")
	if createErr := s.mmClient.CreatePost(taskPost); createErr != nil {
		return nil, fmt.Errorf("failed to create the delegation task post: %w", createErr)
	}

	// Build the sub-turn context once; the system prompt is formatted from
	// the same context the sub-turn executes with.
	llmContext := deps.conversations.BuildDelegatedContext(ctx, targetBot, initiator, dmChannel)
	if llmContext.Parameters == nil {
		llmContext.Parameters = map[string]interface{}{}
	}
	llmContext.Parameters["DelegatingAgentUsername"] = sourceBot.GetConfig().Name

	systemPrompt := ""
	if personaPrompt, promptErr := s.prompts.Format(prompts.PromptDirectMessageQuestionSystem, llmContext); promptErr == nil {
		systemPrompt = personaPrompt
	} else {
		return nil, fmt.Errorf("failed to format the target agent system prompt: %w", promptErr)
	}
	if preamble, promptErr := s.prompts.Format(prompts.PromptDelegatedTaskSystem, llmContext); promptErr == nil {
		systemPrompt = systemPrompt + "\n\n" + preamble
	} else {
		return nil, fmt.Errorf("failed to format the delegated task preamble: %w", promptErr)
	}

	taskPostID := taskPost.Id
	channelID := dmChannel.Id
	convResult, err := deps.convService.CreateConversation(conversation.CreateConversationParams{
		UserID:       initiator.Id,
		BotID:        targetBotUserID,
		ChannelID:    &channelID,
		RootPostID:   &taskPostID,
		Operation:    llm.OperationDelegation,
		SystemPrompt: systemPrompt,
		UserMessage:  req.Task,
		UserPostID:   &taskPostID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create the delegation conversation: %w", err)
	}

	// Stamp the delegation ID onto the task post now that it exists
	// (best-effort; the KV record is the source of truth).
	taskPost.AddProp("delegation_id", convResult.ConversationID)
	if updateErr := s.mmClient.UpdatePost(taskPost); updateErr != nil {
		s.mmClient.LogWarn("Failed to stamp delegation ID on task post", "error", updateErr, "post_id", taskPost.Id)
	}

	record := Record{
		DelegationID:         convResult.ConversationID,
		ParentToolCallID:     req.ParentToolCallID,
		InitiatorUserID:      initiator.Id,
		SourceBotID:          sourceBot.GetMMBot().UserId,
		SourceBotUsername:    sourceBot.GetConfig().Name,
		TargetBotID:          targetBotUserID,
		TargetBotUsername:    targetBot.GetConfig().Name,
		TargetBotDisplayName: targetBot.GetConfig().DisplayName,
		TaskPostID:           taskPost.Id,
		ChannelID:            dmChannel.Id,
		CreatedAt:            model.GetMillis(),
	}
	// The record is what reload reconciliation, cross-node completion, and
	// the status endpoint recover state from — without it the delegation
	// would run in a degraded, unrecoverable mode, so fail up front instead.
	if err := saveRecord(s.mmClient, record); err != nil {
		return nil, fmt.Errorf("failed to persist the delegation record: %w", err)
	}

	// Response placeholder the sub-turn streams into.
	responsePost := &model.Post{
		ChannelId: dmChannel.Id,
		RootId:    taskPost.Id,
	}
	responsePost.AddProp(streaming.ConversationIDProp, convResult.ConversationID)
	streaming.ModifyPostForBot(targetBotUserID, initiator.Id, responsePost, taskPost.Id)
	if err := s.mmClient.CreatePost(responsePost); err != nil {
		return nil, fmt.Errorf("failed to create the delegation response placeholder: %w", err)
	}

	run := &delegationRun{
		svc:          s,
		record:       record,
		initiator:    initiator,
		targetBot:    targetBot,
		dmChannel:    dmChannel,
		responsePost: responsePost,
		llmContext:   llmContext,
		permalink:    s.permalink(taskPost.Id),
	}
	run.emit(PhaseStarting, "", "")

	// Title generation is best-effort and asynchronous.
	go func() {
		if titleErr := deps.convService.GenerateTitle(convResult.ConversationID, targetBot.LLM(), req.Task, llmContext); titleErr != nil {
			s.mmClient.LogWarn("Failed to generate delegation conversation title", "error", titleErr, "delegation_id", convResult.ConversationID)
		}
	}()

	return run, nil
}

// runSubTurn executes the first delegated sub-turn under a detached context:
// the parent caller being canceled must not tear the visible thread
// mid-stream, and the run segment gets its own generous wall-clock ceiling.
func (s *Service) runSubTurn(ctx context.Context, deps serviceDeps, run *delegationRun) (*conversations.DelegatedSubTurnOutcome, error) {
	runCtx, cancel := context.WithTimeout(telemetry.DetachContext(ctx), s.subTurnRunTimeout)
	defer cancel()

	observer := newProgressObserver(func(activity, tools string) {
		run.emit(PhaseRunning, activity, tools)
	})

	return deps.conversations.RunDelegatedSubTurn(runCtx, conversations.DelegatedSubTurnParams{
		Bot:            run.targetBot,
		Initiator:      run.initiator,
		Channel:        run.dmChannel,
		ConversationID: run.record.DelegationID,
		ResponsePost:   run.responsePost,
		LLMContext:     run.llmContext,
		OnStreamEvent:  observer.observe,
	})
}

// await blocks until the sub-turn produces its final answer. There is no
// park: the parent waits for as long as the sub-agent (and the initiator)
// need, bounded only by the delegation lifetime safety ceiling.
func (s *Service) await(ctx context.Context, deps serviceDeps, run *delegationRun, wake <-chan struct{}, deadline time.Time) (string, error) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	for {
		state, finalText, stateErr := latestSubTurnState(deps.convService, run.record.DelegationID)
		switch {
		case stateErr != nil:
			// A store hiccup must not be mistaken for a settled delegation;
			// keep waiting (without emitting a misleading phase) and re-read
			// on the next wake — or eventually time out.
			s.mmClient.LogWarn("Failed to derive delegation state; still waiting", "error", stateErr, "delegation_id", run.record.DelegationID)
		case state == subTurnStateCompleted:
			return finalText, nil
		case state == subTurnStateWaiting:
			run.emit(PhaseWaitingOnYou, "", "")
		case state == subTurnStateNone:
			// The last round settled without a follow-up answer (e.g. the
			// initiator rejected the sub-agent's only pending action, or the
			// follow-up failed).
			run.emit(PhaseFailed, "", "")
			return "", fmt.Errorf("the delegated agent finished without an answer (the user may have declined a required action). See the conversation: %s", run.permalink)
		}

		select {
		case <-wake:
			// Re-read the conversation state; the database is the source of truth.
		case <-timer.C:
			run.emit(PhaseTimedOut, "", "")
			return "", fmt.Errorf("%w after %s while waiting for @%s. The conversation remains available: %s",
				ErrTimedOut, s.maxLifetime, run.record.TargetBotUsername, run.permalink)
		case <-ctx.Done():
			run.emit(PhaseFailed, "", "")
			return "", fmt.Errorf("the delegation was canceled while waiting for @%s. The conversation remains available: %s",
				run.record.TargetBotUsername, run.permalink)
		}
	}
}

// SubTurnCompleted implements conversations.DelegationNotifier: a resumed
// round of the delegation conversation finished streaming on this node.
func (s *Service) SubTurnCompleted(conversationID string) {
	s.handleCompletion(conversationID, true)
}

// HandleClusterCompletion processes a completion signal from a peer node.
func (s *Service) HandleClusterCompletion(conversationID string) {
	s.handleCompletion(conversationID, false)
}

func (s *Service) handleCompletion(conversationID string, publish bool) {
	if conversationID == "" {
		return
	}

	if !s.waiters.signal(conversationID) {
		// No local waiter: the parent may live on a peer node, or it is gone
		// (restart / lifetime ceiling). If the conversation now holds a final
		// answer, still flip the card so the initiator sees "completed".
		s.emitOrphanedCompletion(conversationID)
	}

	if publish {
		deps, err := s.deps()
		if err == nil && deps.cluster != nil {
			if publishErr := deps.cluster.PublishDelegationComplete(conversationID); publishErr != nil {
				s.mmClient.LogWarn("Failed to publish delegation completion cluster event", "error", publishErr, "delegation_id", conversationID)
			}
		}
	}
}

func (s *Service) emitOrphanedCompletion(conversationID string) {
	deps, err := s.deps()
	if err != nil {
		return
	}
	record, err := loadRecordByConversation(s.mmClient, conversationID)
	if err != nil || record == nil {
		return
	}
	state, _, stateErr := latestSubTurnState(deps.convService, conversationID)
	if stateErr != nil || state != subTurnStateCompleted {
		return
	}
	emitUpdate(s.mmClient, *record, PhaseCompleted, "", "", s.permalink(record.TaskPostID))
}

// startKeepalive periodically refreshes the initiator's MCP client activity
// until the returned stop function is called.
func (s *Service) startKeepalive(deps serviceDeps, initiatorUserID string) func() {
	if deps.activity == nil {
		return func() {}
	}

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(s.keepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				deps.activity.TouchUserActivity(initiatorUserID)
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

func (s *Service) permalink(postID string) string {
	siteURL := ""
	if config := s.mmClient.GetConfig(); config != nil && config.ServiceSettings.SiteURL != nil {
		siteURL = strings.TrimRight(*config.ServiceSettings.SiteURL, "/")
	}
	if siteURL == "" || postID == "" {
		return ""
	}
	return fmt.Sprintf("%s/_redirect/pl/%s", siteURL, postID)
}

// Status is the reconciliation view of a delegation for the initiator's UI.
type Status struct {
	DelegationID           string `json:"delegation_id"`
	ParentToolCallID       string `json:"parent_tool_call_id"`
	Phase                  string `json:"phase"`
	TaskPostID             string `json:"task_post_id"`
	Permalink              string `json:"permalink"`
	TargetAgentID          string `json:"target_agent_id"`
	TargetAgentUsername    string `json:"target_agent_username"`
	TargetAgentDisplayName string `json:"target_agent_displayname"`
	CreatedAt              int64  `json:"created_at"`
	AnswerPreview          string `json:"answer_preview,omitempty"`
}

// StatusByParentToolCall derives the live phase of a delegation for the
// reconciliation endpoint. Only the initiator may read it; anyone else (or an
// unknown ID) gets nil.
func (s *Service) StatusByParentToolCall(userID, parentToolCallID string) (*Status, error) {
	deps, err := s.deps()
	if err != nil {
		return nil, err
	}
	if userID == "" || parentToolCallID == "" {
		return nil, nil
	}

	record, err := loadRecordByParentToolCall(s.mmClient, parentToolCallID)
	if err != nil {
		return nil, err
	}
	if record == nil || record.InitiatorUserID != userID {
		return nil, nil
	}

	state, finalText, stateErr := latestSubTurnState(deps.convService, record.DelegationID)
	if stateErr != nil {
		return nil, stateErr
	}
	phase := PhaseRunning
	preview := ""
	switch state {
	case subTurnStateWaiting:
		phase = PhaseWaitingOnYou
	case subTurnStateCompleted:
		phase = PhaseCompleted
		preview = finalText
	case subTurnStateNone:
		// Either still streaming the first round or settled without an
		// answer; the parent tool result disambiguates, so report running.
		phase = PhaseRunning
	}

	return &Status{
		DelegationID:           record.DelegationID,
		ParentToolCallID:       record.ParentToolCallID,
		Phase:                  phase,
		TaskPostID:             record.TaskPostID,
		Permalink:              s.permalink(record.TaskPostID),
		TargetAgentID:          record.TargetBotID,
		TargetAgentUsername:    record.TargetBotUsername,
		TargetAgentDisplayName: record.TargetBotDisplayName,
		CreatedAt:              record.CreatedAt,
		AnswerPreview:          preview,
	}, nil
}

// subTurnState summarizes the delegation conversation after the last user turn.
type subTurnState int

const (
	// subTurnStateNone: no assistant answer and nothing pending (still
	// streaming, or the last round settled without a follow-up).
	subTurnStateNone subTurnState = iota
	// subTurnStateWaiting: an assistant turn holds tool_use blocks awaiting
	// the initiator (pending approval or question, or claimed-accepted).
	subTurnStateWaiting
	// subTurnStateCompleted: the last assistant turn is a plain text answer.
	subTurnStateCompleted
)

// turnSource is the subset of the conversation service used to derive the
// sub-turn state. *conversation.Service implements it.
type turnSource interface {
	GetTurns(conversationID string) ([]store.Turn, error)
}

// latestSubTurnState inspects the delegation conversation's turns after the
// last user turn. The database is the single source of truth for the await
// loop, the reconciliation endpoint, and orphaned-completion flips, so all
// three agree regardless of which node executed what. A non-nil error means
// the state could not be derived (store failure, corrupt content) — callers
// must not treat it as a settled delegation.
func latestSubTurnState(src turnSource, conversationID string) (subTurnState, string, error) {
	turns, err := src.GetTurns(conversationID)
	if err != nil {
		return subTurnStateNone, "", fmt.Errorf("failed to get delegation turns: %w", err)
	}

	lastUserIdx := -1
	for i := range turns {
		if turns[i].Role == "user" {
			lastUserIdx = i
		}
	}

	lastAssistantIdx := -1
	for i := lastUserIdx + 1; i < len(turns); i++ {
		if turns[i].Role == "assistant" {
			lastAssistantIdx = i
		}
	}
	if lastAssistantIdx == -1 {
		return subTurnStateNone, "", nil
	}

	var blocks []conversation.ContentBlock
	if err := json.Unmarshal(turns[lastAssistantIdx].Content, &blocks); err != nil {
		return subTurnStateNone, "", fmt.Errorf("failed to unmarshal delegation turn content: %w", err)
	}

	hasToolUse := false
	waiting := false
	var text strings.Builder
	for _, block := range blocks {
		switch block.Type {
		case conversation.BlockTypeToolUse:
			hasToolUse = true
			if block.Status == conversation.StatusPending || block.Status == conversation.StatusAccepted {
				waiting = true
			}
		case conversation.BlockTypeText:
			text.WriteString(block.Text)
		}
	}

	if waiting {
		return subTurnStateWaiting, "", nil
	}
	// A turn that called tools is never the final answer — the follow-up
	// round produces a separate text-only assistant turn.
	if !hasToolUse && strings.TrimSpace(text.String()) != "" {
		return subTurnStateCompleted, text.String(), nil
	}
	return subTurnStateNone, "", nil
}
