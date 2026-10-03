// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"fmt"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/trace"
)

// DelegationMaxToolTurns caps the tool rounds of a delegated sub-turn,
// independent of the target agent's own (possibly higher) limit. Guards
// against runaway delegated work; the effective cap is the minimum of this
// and the target agent's configured limit.
const DelegationMaxToolTurns = 10

// DelegationAskAgentToolName is the bare name of the embedded delegation tool.
const DelegationAskAgentToolName = "ask_agent"

func maxToolTurnsForConversation(configured int, conv *store.Conversation) int {
	if conv != nil && conv.Operation == llm.OperationDelegation && configured > DelegationMaxToolTurns {
		return DelegationMaxToolTurns
	}
	return configured
}

// DelegationNotifier is notified when a delegated sub-turn finishes a resumed
// round (e.g. after the initiator approves a tool call in the delegation
// thread). The delegation service implements this to wake the waiting parent.
type DelegationNotifier interface {
	SubTurnCompleted(conversationID string)
}

// SetDelegationNotifier sets the notifier for delegated sub-turn completions.
func (c *Conversations) SetDelegationNotifier(n DelegationNotifier) {
	c.delegationNotifier = n
}

// keepNonDelegationTool filters out the embedded ask_agent tool. Delegated
// sub-turns must never see it (delegation depth is 1 in v1) — the filter runs
// before both the plain visible tool store and the strict dynamic-loading
// registry are built, so the tool cannot be loaded dynamically either.
func keepNonDelegationTool(tool llm.Tool) bool {
	if llm.NormalizeMCPServerOrigin(tool.ServerOrigin) != mcp.EmbeddedClientKey {
		return true
	}
	return llm.BareMCPToolName(tool.Name) != DelegationAskAgentToolName
}

// delegationConversationContextOptions returns extra context options for
// conversations created by delegation: the ask_agent exclusion must also
// apply on every resume path (tool approval, follow-up, regeneration) or the
// sub-agent would regain the tool after its first pending approval.
func (c *Conversations) delegationConversationContextOptions(conv *store.Conversation) []llm.ContextOption {
	if conv == nil || conv.Operation != llm.OperationDelegation || c.contextBuilder == nil {
		return nil
	}
	return []llm.ContextOption{
		c.contextBuilder.WithLLMContextMCPToolFilter(keepNonDelegationTool),
	}
}

// notifyDelegationSubTurnCompleted signals the delegation service that a
// resumed round of a delegated sub-turn finished streaming. No-op for
// non-delegation conversations.
func (c *Conversations) notifyDelegationSubTurnCompleted(conv *store.Conversation) {
	if conv == nil || conv.Operation != llm.OperationDelegation || c.delegationNotifier == nil {
		return
	}
	c.delegationNotifier.SubTurnCompleted(conv.ID)
}

// BuildDelegatedContext assembles the LLM context for a delegated sub-turn:
// the target agent's tools built for the initiator, interactive (the
// initiator can answer approvals in the delegation thread), with ask_agent
// excluded. Exposed so the delegation service can format the conversation's
// system prompt from the same context the sub-turn executes with.
func (c *Conversations) BuildDelegatedContext(ctx context.Context, bot *bots.Bot, initiator *model.User, channel *model.Channel) *llm.Context {
	llmContext := c.buildConversationContextWithTools(
		ctx,
		bot, initiator, channel,
		"Failed to load user tool preferences for delegation",
		c.contextBuilder.WithLLMContextInteractive(),
		c.contextBuilder.WithLLMContextMCPToolFilter(keepNonDelegationTool),
	)
	ensureDMWebSearchTracking(llmContext)
	return llmContext
}

// DelegatedSubTurnParams describes one delegated sub-turn execution.
type DelegatedSubTurnParams struct {
	// Bot is the target agent executing the task.
	Bot *bots.Bot
	// Initiator is the human user the delegation runs on behalf of.
	Initiator *model.User
	// Channel is the initiator's DM channel with the target agent.
	Channel *model.Channel
	// ConversationID is the delegation conversation (Operation "delegation").
	ConversationID string
	// ResponsePost is the pre-created placeholder post the sub-turn streams into.
	ResponsePost *model.Post
	// LLMContext, when set, is the pre-built context from
	// BuildDelegatedContext; nil rebuilds it.
	LLMContext *llm.Context
	// OnStreamEvent, when set, observes every stream event (for progress
	// reporting). It must not block.
	OnStreamEvent func(llm.TextStreamEvent)
}

// DelegatedSubTurnOutcome is the result of a delegated sub-turn execution.
type DelegatedSubTurnOutcome struct {
	// FinalText is the sub-agent's final answer. Empty when the sub-turn
	// stopped on unresolved tool calls (PendingApproval) or produced no text.
	FinalText string
	// PendingApproval is true when the sub-turn ended awaiting the
	// initiator's decision (tool approval or question) in the delegation
	// thread.
	PendingApproval bool
}

// RunDelegatedSubTurn executes one delegated sub-turn through the normal DM
// conversation machinery: the target agent's tools are built for the
// initiator (interactive, ask_agent excluded), the tool loop runs with DM
// auto-execution semantics, and the response streams into the delegation
// thread. The call is synchronous — it returns once the stream has fully
// rendered into the response post.
func (c *Conversations) RunDelegatedSubTurn(ctx context.Context, p DelegatedSubTurnParams) (*DelegatedSubTurnOutcome, error) {
	if p.Bot == nil || p.Initiator == nil || p.Channel == nil || p.ResponsePost == nil {
		return nil, fmt.Errorf("delegated sub-turn requires bot, initiator, channel, and response post")
	}
	if c.convService == nil {
		return nil, fmt.Errorf("conversation service not configured")
	}

	ctx, span := telemetry.Tracer().Start(ctx, "run delegated sub-turn",
		trace.WithAttributes(
			telemetry.AgentID.String(p.Bot.GetMMBot().UserId),
			telemetry.UserID.String(p.Initiator.Id),
			telemetry.ChannelID.String(p.Channel.Id),
		),
	)
	defer span.End()

	// The sub-turn is interactively answerable: the initiator can respond to
	// approvals and questions directly in the delegation thread.
	llmContext := p.LLMContext
	if llmContext == nil {
		llmContext = c.BuildDelegatedContext(ctx, p.Bot, p.Initiator, p.Channel)
	}

	conv, err := c.convService.GetConversation(p.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get delegation conversation: %w", err)
	}

	completionReq, err := c.convService.BuildCompletionRequest(conv, llmContext)
	if err != nil {
		return nil, fmt.Errorf("failed to build delegation completion request: %w", err)
	}

	maxRounds := maxToolTurnsForConversation(p.Bot.GetConfig().EffectiveMaxToolTurns(), conv)
	runner := toolrunner.New(p.Bot.LLM(), toolrunner.WithMaxRounds(maxRounds))
	runResult, err := runner.Run(ctx, *completionReq, c.shouldAutoExecuteTool(llmContext, true), func(turns []toolrunner.ToolTurn) {
		if writeErr := c.convService.WriteToolTurns(p.ConversationID, turns, true); writeErr != nil {
			c.mmClient.LogError("Failed to write delegation tool turns", "error", writeErr, "conversation_id", p.ConversationID)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("delegation tool runner failed: %w", err)
	}

	stream := runResult.Stream
	if webSearchData := mmtools.ConsumeWebSearchContexts(llmContext); len(webSearchData) > 0 {
		stream = mmtools.DecorateStreamWithAnnotations(stream, webSearchData, nil)
	}

	observer := &delegationStreamObserver{onEvent: p.OnStreamEvent}
	stream = teeTextStream(stream, observer.observe)

	streamCtx, err := c.streamingService.GetStreamingContext(ctx, p.ResponsePost.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to get delegation streaming context: %w", err)
	}
	defer c.streamingService.FinishStreaming(p.ResponsePost.Id)

	// Synchronous: StreamToPost consumes the stream to completion, which is
	// exactly the await point the delegation pipeline needs.
	c.streamingService.StreamToPost(streamCtx, stream, p.ResponsePost, c.responseLocale(p.Initiator, p.Channel), p.Initiator.Id)

	return &DelegatedSubTurnOutcome{
		// Safe to read after the stream has been fully consumed.
		FinalText:       runResult.FinalText,
		PendingApproval: observer.endedPending(),
	}, nil
}

// delegationStreamObserver tracks whether the sub-turn's last tool-calls
// event was still unresolved when the stream ended, and forwards events to an
// optional external observer.
type delegationStreamObserver struct {
	onEvent func(llm.TextStreamEvent)

	mu                   sync.Mutex
	lastToolCallsPending bool
}

func (o *delegationStreamObserver) observe(event llm.TextStreamEvent) {
	if event.Type == llm.EventTypeToolCalls {
		if toolCalls, ok := event.Value.([]llm.ToolCall); ok {
			o.mu.Lock()
			o.lastToolCallsPending = anyUnresolvedToolCall(toolCalls)
			o.mu.Unlock()
		}
	}
	if o.onEvent != nil {
		o.onEvent(event)
	}
}

func (o *delegationStreamObserver) endedPending() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastToolCallsPending
}

// anyUnresolvedToolCall reports whether any tool call in the batch is still
// awaiting a user decision (mirror of the streaming layer's resolved-event
// predicate, inverted).
func anyUnresolvedToolCall(toolCalls []llm.ToolCall) bool {
	for _, tc := range toolCalls {
		switch tc.Status {
		case llm.ToolCallStatusSuccess,
			llm.ToolCallStatusError,
			llm.ToolCallStatusAutoApproved,
			llm.ToolCallStatusRejected:
			// terminal
		default:
			return true
		}
	}
	return false
}

// teeTextStream forwards every event of src through observe before handing it
// to the returned stream. observe runs on the streaming goroutine and must
// not block.
func teeTextStream(src *llm.TextStreamResult, observe func(llm.TextStreamEvent)) *llm.TextStreamResult {
	out := make(chan llm.TextStreamEvent)
	go func() {
		defer close(out)
		for event := range src.Stream {
			observe(event)
			out <- event
		}
	}()
	return &llm.TextStreamResult{Stream: out}
}
