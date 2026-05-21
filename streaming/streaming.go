// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/i18n"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost-plugin-agents/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/trace"
)

// Client defines the minimal client interface needed for streaming operations.
type Client interface {
	PublishWebSocketEvent(event string, payload map[string]interface{}, broadcast *model.WebsocketBroadcast)
	UpdatePost(post *model.Post) error
	CreatePost(post *model.Post) error
	DM(senderID, receiverID string, post *model.Post) error
	GetUser(userID string) (*model.User, error)
	GetChannel(channelID string) (*model.Channel, error)
	GetConfig() *model.Config
	KVSet(key string, value interface{}) error
	LogError(msg string, keyValuePairs ...interface{})
	LogDebug(msg string, keyValuePairs ...interface{})
}

const PostStreamingControlCancel = "cancel"
const PostStreamingControlEnd = "end"
const PostStreamingControlStart = "start"

// PostStreamingControlContinue signals a tool-approval resume stream onto a
// post that already has content. The webapp clears the visible message but
// keeps the resolved tool cards.
const PostStreamingControlContinue = "continue"

// WebSearchContextProp is still read by conversations/web_search_context.go when
// extracting web search state from legacy thread posts.
const WebSearchContextProp = "web_search_context"

type Service interface {
	StreamToNewPost(ctx context.Context, botID string, requesterUserID string, stream *llm.TextStreamResult, post *model.Post, respondingToPostID string) error
	StreamToNewDM(ctx context.Context, botID string, stream *llm.TextStreamResult, userID string, post *model.Post, respondingToPostID string) error
	StreamToPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, userLocale string, requesterUserID string)

	// StreamContinuationToPost streams a follow-up round onto a post that
	// already has an assistant turn (tool-approval resume). Finalize demotes
	// the prior anchor so both rounds render. Do not use for regeneration.
	StreamContinuationToPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, userLocale string, requesterUserID string)

	StopStreaming(postID string)
	GetStreamingContext(inCtx context.Context, postID string) (context.Context, error)
	FinishStreaming(postID string)
}

type postStreamContext struct {
	cancel context.CancelFunc
}

// TurnStore is the subset of store operations needed by the streaming layer.
// Finalize either creates a fresh anchor (first stream / regen, with the caller
// having scrubbed any prior turns) or demotes the existing anchor and creates
// a new one (continuation).
type TurnStore interface {
	CreateTurnAutoSequence(turn *store.Turn) error
	GetTurnByPostID(postID string) (*store.Turn, error)
	UpdateTurnPostID(id string, postID *string) error
}

// turnAccumulator collects stream state. The turn is not written to the
// database until finalizeTurn runs at stream end/error/cancel.
type turnAccumulator struct {
	conversationID string
	postID         string
	isDM           bool // true for DM channels; controls shared flag on tool_use blocks

	// existingAnchorID is the prior anchor for this post, looked up at stream
	// start. Used only by continuation finalize to demote the prior anchor.
	existingAnchorID string
	isContinuation   bool

	// Accumulated content
	text          strings.Builder
	reasoning     strings.Builder
	reasoningData llm.ReasoningData
	annotations   []llm.Annotation
	toolCalls     []llm.ToolCall

	// Token usage
	tokensIn  int64
	tokensOut int64
}

// buildContentBlocks constructs content blocks from accumulated stream state.
// Always returns a non-nil slice so that json.Marshal yields "[]" rather than
// "null" for empty accumulator state; the webapp iterates turn.content and
// crashes on null.
func (a *turnAccumulator) buildContentBlocks() []conversation.ContentBlock {
	blocks := []conversation.ContentBlock{}

	// 1. Thinking block (if reasoning completed)
	if a.reasoningData.Text != "" {
		blocks = append(blocks, conversation.ContentBlock{
			Type:      conversation.BlockTypeThinking,
			Text:      a.reasoningData.Text,
			Signature: a.reasoningData.Signature,
		})
	} else if a.reasoning.Len() > 0 {
		// Partial reasoning (error/cancel before ReasoningEnd)
		blocks = append(blocks, conversation.ContentBlock{
			Type: conversation.BlockTypeThinking,
			Text: a.reasoning.String(),
		})
	}

	// 2. Text block
	if a.text.Len() > 0 {
		blocks = append(blocks, conversation.ContentBlock{
			Type: conversation.BlockTypeText,
			Text: a.text.String(),
		})
	}

	// 3. Annotations block (web search context)
	if len(a.annotations) > 0 {
		resultsJSON, err := json.Marshal(a.annotations)
		if err == nil {
			blocks = append(blocks, conversation.ContentBlock{
				Type: conversation.BlockTypeAnnotations,
				WebSearchContext: &conversation.WebSearchContext{
					Results: resultsJSON,
					Count:   len(a.annotations),
				},
			})
		}
	}

	// 4. Tool call blocks
	for _, tc := range a.toolCalls {
		blocks = append(blocks, conversation.ContentBlock{
			Type:         conversation.BlockTypeToolUse,
			ID:           tc.ID,
			Name:         tc.Name,
			ServerOrigin: tc.ServerOrigin,
			Input:        tc.Arguments,
			Status:       conversation.StatusToString(tc.Status),
			Shared:       conversation.BoolPtr(a.isDM),
		})
	}

	return blocks
}

var ErrAlreadyStreamingToPost = fmt.Errorf("already streaming to post")

type MMPostStreamService struct {
	contexts      map[string]postStreamContext
	contextsMutex sync.Mutex
	mmClient      Client
	i18n          *i18n.Bundle
	turnStore     TurnStore
}

func NewMMPostStreamService(mmClient Client, i18n *i18n.Bundle) *MMPostStreamService {
	return &MMPostStreamService{
		contexts: make(map[string]postStreamContext),
		mmClient: mmClient,
		i18n:     i18n,
	}
}

// SetTurnStore sets the store used for persisting assistant turns.
// When nil (the default), turn persistence is silently skipped.
func (p *MMPostStreamService) SetTurnStore(ts TurnStore) {
	p.turnStore = ts
}

func (p *MMPostStreamService) StreamToNewPost(ctx context.Context, botID string, requesterUserID string, stream *llm.TextStreamResult, post *model.Post, respondingToPostID string) error {
	// We use ModifyPostForBot directly here to add the responding to post ID
	ModifyPostForBot(botID, requesterUserID, post, respondingToPostID)

	if err := p.mmClient.CreatePost(post); err != nil {
		return fmt.Errorf("unable to create post: %w", err)
	}

	ctx, err := p.GetStreamingContext(ctx, post.Id)
	if err != nil {
		return err
	}

	go func() {
		defer p.FinishStreaming(post.Id)
		user, err := p.mmClient.GetUser(requesterUserID)
		locale := *p.mmClient.GetConfig().LocalizationSettings.DefaultServerLocale
		if err != nil {
			p.StreamToPost(ctx, stream, post, locale, requesterUserID)
			return
		}

		channel, err := p.mmClient.GetChannel(post.ChannelId)
		if err != nil {
			p.StreamToPost(ctx, stream, post, locale, requesterUserID)
			return
		}

		if channel.Type == model.ChannelTypeDirect {
			if channel.Name == botID+"__"+user.Id || channel.Name == user.Id+"__"+botID {
				p.StreamToPost(ctx, stream, post, user.Locale, requesterUserID)
				return
			}
		}
		p.StreamToPost(ctx, stream, post, locale, requesterUserID)
	}()

	return nil
}

func (p *MMPostStreamService) StreamToNewDM(ctx context.Context, botID string, stream *llm.TextStreamResult, userID string, post *model.Post, respondingToPostID string) error {
	// We use ModifyPostForBot directly here to add the responding to post ID
	ModifyPostForBot(botID, userID, post, respondingToPostID)

	if err := p.mmClient.DM(botID, userID, post); err != nil {
		return fmt.Errorf("failed to post DM: %w", err)
	}

	ctx, err := p.GetStreamingContext(ctx, post.Id)
	if err != nil {
		return err
	}

	go func() {
		defer p.FinishStreaming(post.Id)
		user, err := p.mmClient.GetUser(userID)
		locale := *p.mmClient.GetConfig().LocalizationSettings.DefaultServerLocale
		if err != nil {
			p.StreamToPost(ctx, stream, post, locale, userID)
			return
		}

		channel, err := p.mmClient.GetChannel(post.ChannelId)
		if err != nil {
			p.StreamToPost(ctx, stream, post, locale, userID)
			return
		}

		if channel.Type == model.ChannelTypeDirect {
			if channel.Name == botID+"__"+user.Id || channel.Name == user.Id+"__"+botID {
				p.StreamToPost(ctx, stream, post, user.Locale, userID)
				return
			}
		}
		p.StreamToPost(ctx, stream, post, locale, userID)
	}()

	return nil
}

func (p *MMPostStreamService) sendPostStreamingUpdateEventWithBroadcast(post *model.Post, message string, broadcast *model.WebsocketBroadcast) {
	p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
		"post_id": post.Id,
		"next":    message,
	}, broadcast)
}

func (p *MMPostStreamService) sendPostStreamingControlEventWithBroadcast(post *model.Post, control string, broadcast *model.WebsocketBroadcast) {
	p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
		"post_id": post.Id,
		"control": control,
	}, broadcast)
}

func (p *MMPostStreamService) sendPostStreamingReasoningEventWithBroadcast(post *model.Post, reasoning string, control string, broadcast *model.WebsocketBroadcast) {
	p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
		"post_id":   post.Id,
		"control":   control,
		"reasoning": reasoning,
	}, broadcast)
}

func (p *MMPostStreamService) sendPostStreamingAnnotationsEventWithBroadcast(post *model.Post, annotations string, broadcast *model.WebsocketBroadcast) {
	p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
		"post_id":     post.Id,
		"control":     "annotations",
		"annotations": annotations,
	}, broadcast)
}

func (p *MMPostStreamService) StopStreaming(postID string) {
	p.contextsMutex.Lock()
	defer p.contextsMutex.Unlock()
	if streamContext, ok := p.contexts[postID]; ok {
		streamContext.cancel()
	}
	delete(p.contexts, postID)
}

func (p *MMPostStreamService) GetStreamingContext(inCtx context.Context, postID string) (context.Context, error) {
	p.contextsMutex.Lock()
	defer p.contextsMutex.Unlock()

	if _, ok := p.contexts[postID]; ok {
		return nil, ErrAlreadyStreamingToPost
	}

	ctx, cancel := context.WithCancel(inCtx)

	streamingContext := postStreamContext{
		cancel: cancel,
	}

	p.contexts[postID] = streamingContext

	return ctx, nil
}

// FinishStreaming should be called when a post streaming operation is finished on success or failure.
// It is safe to call multiple times, must be called at least once.
func (p *MMPostStreamService) FinishStreaming(postID string) {
	p.contextsMutex.Lock()
	defer p.contextsMutex.Unlock()
	if streamContext, ok := p.contexts[postID]; ok {
		streamContext.cancel()
	}
	delete(p.contexts, postID)
}

// newTurnAccumulator constructs an in-memory accumulator. Nothing is persisted
// until finalizeTurn runs.
func newTurnAccumulator(conversationID, postID, existingAnchorID string, isContinuation, isDM bool) *turnAccumulator {
	return &turnAccumulator{
		conversationID:   conversationID,
		postID:           postID,
		existingAnchorID: existingAnchorID,
		isContinuation:   isContinuation,
		isDM:             isDM,
	}
}

// finalizeTurn writes the accumulated content as a new assistant turn. For
// continuation streams it first demotes the prior anchor.
func (p *MMPostStreamService) finalizeTurn(acc *turnAccumulator) {
	blocks := acc.buildContentBlocks()

	contentJSON, err := json.Marshal(blocks)
	if err != nil {
		p.mmClient.LogError("Failed to marshal turn content blocks", "error", err, "post_id", acc.postID)
		return
	}

	if acc.existingAnchorID != "" && acc.isContinuation {
		// Demote the prior anchor so the new turn becomes the post's anchor.
		if demoteErr := p.turnStore.UpdateTurnPostID(acc.existingAnchorID, nil); demoteErr != nil {
			p.mmClient.LogError("Failed to demote prior anchor turn", "error", demoteErr, "post_id", acc.postID, "turn_id", acc.existingAnchorID)
		}
	}

	postIDCopy := acc.postID
	turn := &store.Turn{
		ID:             model.NewId(),
		ConversationID: acc.conversationID,
		PostID:         &postIDCopy,
		Role:           "assistant",
		Content:        contentJSON,
		TokensIn:       acc.tokensIn,
		TokensOut:      acc.tokensOut,
		CreatedAt:      model.GetMillis(),
	}

	if err := p.turnStore.CreateTurnAutoSequence(turn); err != nil {
		p.mmClient.LogError("Failed to create finalized assistant turn", "error", err, "post_id", acc.postID)
	}
}

// broadcastToolCalls sends tool call WebSocket events with privacy scoping.
// The requester receives full tool call data (arguments, results).
// Other channel members receive redacted tool calls (names and status only).
func (p *MMPostStreamService) broadcastToolCalls(post *model.Post, toolCalls []llm.ToolCall, requesterUserID string) {
	// Full data to the requester only.
	fullJSON, err := json.Marshal(toolCalls)
	if err != nil {
		p.mmClient.LogError("Failed to marshal tool calls", "error", err)
		return
	}
	p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
		"post_id":   post.Id,
		"control":   "tool_call",
		"tool_call": string(fullJSON),
	}, &model.WebsocketBroadcast{
		ChannelId: post.ChannelId,
		UserId:    requesterUserID,
	})

	// Redacted data to the rest of the channel (omit requester to avoid duplicates).
	redacted := redactToolCalls(toolCalls)
	redactedJSON, err := json.Marshal(redacted)
	if err != nil {
		p.mmClient.LogError("Failed to marshal redacted tool calls", "error", err)
		return
	}
	p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
		"post_id":   post.Id,
		"control":   "tool_call",
		"tool_call": string(redactedJSON),
	}, &model.WebsocketBroadcast{
		ChannelId: post.ChannelId,
		OmitUsers: map[string]bool{requesterUserID: true},
	})
}

// isResolvedToolCallsEvent reports whether a ToolCalls event represents the
// post-execution "resolved" broadcast (every call has a terminal status
// assigned by toolrunner after execution) rather than the pre-execution
// "pending" broadcast. toolrunner.buildResolvedToolCalls tags successful
// auto-run tools as AutoApproved (not Success) and errored ones as Error;
// user-approved tools are later tagged Success by the approval flow. Anything
// else — most commonly Pending — indicates the event hasn't been executed yet.
func isResolvedToolCallsEvent(toolCalls []llm.ToolCall) bool {
	if len(toolCalls) == 0 {
		return false
	}
	for _, tc := range toolCalls {
		switch tc.Status {
		case llm.ToolCallStatusSuccess,
			llm.ToolCallStatusError,
			llm.ToolCallStatusAutoApproved:
			// terminal status after execution
		default:
			return false
		}
	}
	return true
}

// redactToolCalls returns a copy of the tool calls with Arguments and Result
// cleared so that non-requesters see tool names and status but not payloads.
func redactToolCalls(toolCalls []llm.ToolCall) []llm.ToolCall {
	redacted := make([]llm.ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		redacted[i] = llm.ToolCall{
			ID:           tc.ID,
			Name:         tc.Name,
			ServerOrigin: tc.ServerOrigin,
			Status:       tc.Status,
		}
	}
	return redacted
}

// StreamToPost streams a fresh response onto a post (first stream or regen,
// where the caller has already scrubbed prior turns). For tool-approval resume
// use StreamContinuationToPost.
func (p *MMPostStreamService) StreamToPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, userLocale string, requesterUserID string) {
	p.streamToPostImpl(ctx, stream, post, userLocale, requesterUserID, false)
}

func (p *MMPostStreamService) StreamContinuationToPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, userLocale string, requesterUserID string) {
	p.streamToPostImpl(ctx, stream, post, userLocale, requesterUserID, true)
}

func (p *MMPostStreamService) streamToPostImpl(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, userLocale string, requesterUserID string, isContinuation bool) {
	// Top-level posts are their own thread root, so falling back to post.Id
	// keeps the attribute populated and makes "all spans for this thread"
	// queries work uniformly for both replies and root posts.
	rootPostID := post.RootId
	if rootPostID == "" {
		rootPostID = post.Id
	}
	ctx, span := telemetry.Tracer().Start(ctx, "stream to post",
		trace.WithAttributes(
			telemetry.PostID.String(post.Id),
			telemetry.ChannelID.String(post.ChannelId),
			telemetry.ThreadRootPostID.String(rootPostID),
		),
	)
	defer span.End()

	broadcast := &model.WebsocketBroadcast{ChannelId: post.ChannelId}

	// Look up any prior anchor; only continuation uses it (to demote at
	// finalize). First stream and regen find none.
	controlEvent := PostStreamingControlStart
	existingAnchorID := ""
	if p.turnStore != nil {
		if existing, lookupErr := p.turnStore.GetTurnByPostID(post.Id); lookupErr == nil && existing != nil && existing.Role == "assistant" {
			existingAnchorID = existing.ID
			if isContinuation {
				controlEvent = PostStreamingControlContinue
				post.Message = ""
			}
		}
	}
	p.sendPostStreamingControlEventWithBroadcast(post, controlEvent, broadcast)

	// Create turn accumulator if turn persistence is enabled and a conversation_id is set
	var acc *turnAccumulator
	if p.turnStore != nil {
		if convID, ok := post.GetProp(ConversationIDProp).(string); ok && convID != "" {
			// Match mmapi.IsDMWith across the codebase: only true 1-1 DMs between
			// the requester and the bot count as DMs here. Group DMs follow the
			// channel share-flow, so their tool_use blocks default to unshared.
			isDM := false
			if ch, chErr := p.mmClient.GetChannel(post.ChannelId); chErr == nil {
				isDM = mmapi.IsDMWith(requesterUserID, ch)
			}
			acc = newTurnAccumulator(convID, post.Id, existingAnchorID, isContinuation, isDM)
		}
	}

	defer func() {
		if acc != nil {
			p.finalizeTurn(acc)
		}
		p.sendPostStreamingControlEventWithBroadcast(post, PostStreamingControlEnd, broadcast)
	}()

	var messageBuilder strings.Builder
	messageBuilder.Grow(4096) // Pre-allocate for typical response size
	var reasoningBuffer strings.Builder

	for {
		select {
		case event, ok := <-stream.Stream:
			if !ok {
				// Stream channel closed - persist final state
				if err := p.mmClient.UpdatePost(post); err != nil {
					p.mmClient.LogError("Streaming failed to update post on channel close", "error", err)
				}
				return
			}
			switch event.Type {
			case llm.EventTypeText:
				// Handle text event
				if textChunk, ok := event.Value.(string); ok {
					messageBuilder.WriteString(textChunk)
					post.Message = messageBuilder.String()
					p.sendPostStreamingUpdateEventWithBroadcast(post, post.Message, broadcast)
					if acc != nil {
						acc.text.WriteString(textChunk)
					}
				}
			case llm.EventTypeEnd:
				// Stream has closed cleanly. The "empty" fallback message only
				// applies when the LLM truly produced nothing; a stream that
				// stopped after emitting tool_use blocks (e.g. awaiting user
				// approval) is a valid response rendered via the tool UI.
				hasToolCalls := acc != nil && len(acc.toolCalls) > 0
				if strings.TrimSpace(post.Message) == "" && !hasToolCalls {
					p.mmClient.LogError("LLM closed stream with no result")
					T := i18n.LocalizerFunc(p.i18n, userLocale)
					emptyText := T("agents.stream_to_post_llm_not_return", "Sorry! The LLM did not return a result.")
					post.Message = emptyText
					// Mirror into the accumulator so the turn carries the fallback.
					if acc != nil {
						acc.text.WriteString(emptyText)
					}
					p.sendPostStreamingUpdateEventWithBroadcast(post, post.Message, broadcast)
				}

				if err := p.mmClient.UpdatePost(post); err != nil {
					p.mmClient.LogError("Streaming failed to update post", "error", err)
					return
				}
				return
			case llm.EventTypeError:
				// Handle error event
				var err error
				if errValue, ok := event.Value.(error); ok {
					err = errValue
				} else {
					err = fmt.Errorf("unknown error from LLM")
				}

				// Handle partial results
				var separator string
				if strings.TrimSpace(post.Message) == "" {
					post.Message = ""
				} else {
					separator = "\n\n"
					post.Message += separator
				}
				p.mmClient.LogError("Streaming result to post failed partway", "error", err)
				T := i18n.LocalizerFunc(p.i18n, userLocale)
				errorText := T("agents.stream_to_post_access_llm_error", "Sorry! An error occurred while accessing the LLM. See server logs for details.")
				post.Message += errorText
				// Mirror into the accumulator so the turn carries the error.
				if acc != nil {
					if separator != "" {
						acc.text.WriteString(separator)
					}
					acc.text.WriteString(errorText)
				}

				if err := p.mmClient.UpdatePost(post); err != nil {
					p.mmClient.LogError("Error recovering from streaming error", "error", err)
					return
				}
				p.sendPostStreamingUpdateEventWithBroadcast(post, post.Message, broadcast)
				return
			case llm.EventTypeReasoning:
				// Handle reasoning summary chunk - accumulate and stream
				if reasoningChunk, ok := event.Value.(string); ok {
					reasoningBuffer.WriteString(reasoningChunk)
					// Send reasoning event with accumulated text so far
					p.sendPostStreamingReasoningEventWithBroadcast(post, reasoningBuffer.String(), "reasoning_summary", broadcast)
					if acc != nil {
						acc.reasoning.WriteString(reasoningChunk)
					}
				}
			case llm.EventTypeReasoningEnd:
				// Reasoning summary completed - stream final event and accumulate for turn persistence
				if reasoningData, ok := event.Value.(llm.ReasoningData); ok {
					p.sendPostStreamingReasoningEventWithBroadcast(post, reasoningData.Text, "reasoning_summary_done", broadcast)
					reasoningBuffer.Reset()
					if acc != nil {
						acc.reasoningData = reasoningData
					}
				}
			case llm.EventTypeToolCalls:
				// Tool calls are handled by toolrunner before streaming begins.
				// Here we only accumulate them for turn persistence and send a
				// WebSocket event so the webapp can display progress.
				if toolCalls, ok := event.Value.([]llm.ToolCall); ok {
					for i := range toolCalls {
						toolCalls[i].SanitizeArguments()
					}
					if acc != nil {
						// On resolved, reset the accumulator: toolrunner
						// persists the just-completed round separately via
						// onToolTurns, and only the final round's content
						// belongs on the anchor. Do NOT broadcast next: ""
						// here — the webapp snapshots the round's preamble
						// at the resolved tool_call event. On pending,
						// retain the calls so a rejected-approval turn
						// keeps them.
						if isResolvedToolCallsEvent(toolCalls) {
							acc.text.Reset()
							acc.reasoning.Reset()
							acc.reasoningData = llm.ReasoningData{}
							acc.annotations = nil
							acc.toolCalls = nil
							messageBuilder.Reset()
							post.Message = ""
						} else {
							acc.toolCalls = toolCalls
						}
					}
					p.broadcastToolCalls(post, toolCalls, requesterUserID)
				}
			case llm.EventTypeAnnotations:
				// Handle annotations - might include cleaned message for web search citations
				if annotationMap, ok := event.Value.(map[string]interface{}); ok {
					// Web search annotations with cleaned message
					if annotations, hasAnnotations := annotationMap["annotations"].([]llm.Annotation); hasAnnotations {
						if cleanedMsg, hasCleaned := annotationMap["cleanedMessage"].(string); hasCleaned {
							// Replace post message with cleaned version (citation markers removed).
							// Reset messageBuilder so subsequent text events append to the cleaned content.
							messageBuilder.Reset()
							messageBuilder.WriteString(cleanedMsg)
							post.Message = cleanedMsg
							p.sendPostStreamingUpdateEventWithBroadcast(post, post.Message, broadcast)
							if acc != nil {
								acc.text.Reset()
								acc.text.WriteString(cleanedMsg)
							}
						}

						annotationsJSON, err := json.Marshal(annotations)
						if err != nil {
							p.mmClient.LogError("Failed to marshal annotations", "error", err)
						} else {
							p.sendPostStreamingAnnotationsEventWithBroadcast(post, string(annotationsJSON), broadcast)
						}
						if acc != nil {
							acc.annotations = annotations
						}
					}
				} else if annotations, ok := event.Value.([]llm.Annotation); ok {
					// Regular annotations without cleaned message
					annotationsJSON, err := json.Marshal(annotations)
					if err != nil {
						p.mmClient.LogError("Failed to marshal annotations", "error", err)
					} else {
						p.sendPostStreamingAnnotationsEventWithBroadcast(post, string(annotationsJSON), broadcast)
					}
					if acc != nil {
						acc.annotations = annotations
					}
				}
			case llm.EventTypeUsage:
				// Handle token usage data
				if usage, ok := event.Value.(llm.TokenUsage); ok {
					if acc != nil {
						acc.tokensIn += usage.InputTokens
						acc.tokensOut += usage.OutputTokens
					}
				}
			}
		case <-ctx.Done():
			if err := p.mmClient.UpdatePost(post); err != nil {
				p.mmClient.LogError("Error updating post on stop signaled", "error", err)
				return
			}
			p.sendPostStreamingControlEventWithBroadcast(post, PostStreamingControlCancel, broadcast)
			return
		}
	}
}
