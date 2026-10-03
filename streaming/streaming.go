// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/trace"
)

// Client defines the minimal client interface needed for streaming operations.
type Client interface {
	PublishWebSocketEvent(event string, payload map[string]any, broadcast *model.WebsocketBroadcast)
	UpdatePost(post *model.Post) error
	CreatePost(post *model.Post) error
	DM(senderID, receiverID string, post *model.Post) error
	GetUser(userID string) (*model.User, error)
	GetChannel(channelID string) (*model.Channel, error)
	GetConfig() *model.Config
	KVSet(key string, value any) error
	LogError(msg string, keyValuePairs ...any)
	LogWarn(msg string, keyValuePairs ...any)
	LogDebug(msg string, keyValuePairs ...any)
}

// maxPostAttachments independently caps file IDs merged onto a streamed post
// so an emitter cannot grow post.FileIds unboundedly.
const maxPostAttachments = llm.MaxPostAttachments

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

	sequence    llm.TurnSequence
	annotations []llm.Annotation
	toolCalls   []llm.ToolCall
	// serverTools is the latest cumulative snapshot; sequence stores positions only.
	serverTools []llm.ServerToolUse

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

	blocks = append(blocks, conversation.SequenceBlocks(a.sequence.Segments(), a.serverTools)...)

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

	// Tool use ends an assistant turn, so these always come last.
	for _, tc := range a.toolCalls {
		blocks = append(blocks, conversation.ContentBlock{
			Type:             conversation.BlockTypeToolUse,
			ID:               tc.ID,
			Name:             tc.Name,
			ServerOrigin:     tc.ServerOrigin,
			Input:            tc.Arguments,
			MCPBareName:      tc.MCPBareName,
			Status:           conversation.StatusToString(tc.Status),
			Shared:           new(a.isDM),
			UserInteraction:  tc.UserInteraction,
			WouldAutoExecute: tc.WouldAutoExecute,
			Title:            tc.Title,
			Description:      tc.Description,
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
	return p.streamToCreatedPost(ctx, botID, requesterUserID, stream, post, respondingToPostID, func() error {
		if err := p.mmClient.CreatePost(post); err != nil {
			return fmt.Errorf("unable to create post: %w", err)
		}
		return nil
	})
}

func (p *MMPostStreamService) StreamToNewDM(ctx context.Context, botID string, stream *llm.TextStreamResult, userID string, post *model.Post, respondingToPostID string) error {
	return p.streamToCreatedPost(ctx, botID, userID, stream, post, respondingToPostID, func() error {
		if err := p.mmClient.DM(botID, userID, post); err != nil {
			return fmt.Errorf("failed to post DM: %w", err)
		}
		return nil
	})
}

// streamToCreatedPost creates the post via createPost and streams into it,
// using the user's locale only for a 1-1 DM between the user and the bot;
// everything else gets the server default locale.
func (p *MMPostStreamService) streamToCreatedPost(ctx context.Context, botID string, userID string, stream *llm.TextStreamResult, post *model.Post, respondingToPostID string, createPost func() error) error {
	// We use ModifyPostForBot directly here to add the responding to post ID
	ModifyPostForBot(botID, userID, post, respondingToPostID)

	if err := createPost(); err != nil {
		return err
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

// sendPostStreamingEvent publishes a "postupdate" WebSocket event carrying the
// post ID plus the given payload fields.
func (p *MMPostStreamService) sendPostStreamingEvent(post *model.Post, broadcast *model.WebsocketBroadcast, fields map[string]any) {
	payload := map[string]any{"post_id": post.Id}
	maps.Copy(payload, fields)
	p.mmClient.PublishWebSocketEvent("postupdate", payload, broadcast)
}

// StopStreaming cancels any in-flight stream to the given post.
func (p *MMPostStreamService) StopStreaming(postID string) {
	p.FinishStreaming(postID)
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
	p.sendPostStreamingEvent(post, &model.WebsocketBroadcast{
		ChannelId:           post.ChannelId,
		UserId:              requesterUserID,
		ReliableClusterSend: true,
	}, map[string]any{"control": "tool_call", "tool_call": string(fullJSON)})

	// Redacted data to the rest of the channel (omit requester to avoid duplicates).
	redacted := redactToolCalls(toolCalls)
	redactedJSON, err := json.Marshal(redacted)
	if err != nil {
		p.mmClient.LogError("Failed to marshal redacted tool calls", "error", err)
		return
	}
	p.sendPostStreamingEvent(post, &model.WebsocketBroadcast{
		ChannelId:           post.ChannelId,
		OmitUsers:           map[string]bool{requesterUserID: true},
		ReliableClusterSend: true,
	}, map[string]any{"control": "tool_call", "tool_call": string(redactedJSON)})
}

// redactToolCalls returns a copy of the tool calls with Arguments, Result, and
// MCPBareName cleared so non-requesters see tool identity and status but not
// payloads. Must stay in lockstep with conversation.FilterForNonRequester
// (enforced by tool_call_parity_test.go); new llm.ToolCall fields default to
// redacted here by omission.
func redactToolCalls(toolCalls []llm.ToolCall) []llm.ToolCall {
	redacted := make([]llm.ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		redacted[i] = llm.ToolCall{
			ID:               tc.ID,
			Name:             tc.Name,
			Title:            tc.Title,
			Description:      tc.Description,
			ServerOrigin:     tc.ServerOrigin,
			Status:           tc.Status,
			UserInteraction:  tc.UserInteraction,
			WouldAutoExecute: tc.WouldAutoExecute,
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

	// postupdate events stream the full assistant message, tool calls,
	// reasoning and annotations, which routinely exceed the 49077-byte UDP
	// limit and are essential to the streaming UX. ReliableClusterSend routes
	// them over TCP so they are not silently dropped between cluster nodes.
	broadcast := &model.WebsocketBroadcast{ChannelId: post.ChannelId, ReliableClusterSend: true}

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
	p.sendPostStreamingEvent(post, broadcast, map[string]any{"control": controlEvent})

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
		p.sendPostStreamingEvent(post, broadcast, map[string]any{"control": PostStreamingControlEnd})
	}()

	var messageBuilder strings.Builder
	messageBuilder.Grow(4096) // Pre-allocate for typical response size
	var reasoningBuffer strings.Builder
	attachmentCapWarned := false

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
					p.sendPostStreamingEvent(post, broadcast, map[string]any{"next": post.Message})
					if acc != nil {
						acc.sequence.AppendText(textChunk)
					}
				}
			case llm.EventTypeFiles:
				// File IDs created during the turn. Merge into the post so
				// the final UpdatePost attaches them server-side; the server
				// strips any ID that is not attachable. Cap the merged total
				// independently of emitter discipline.
				if ids, ok := event.Value.([]string); ok {
					dropped := 0
					for _, id := range ids {
						if slices.Contains(post.FileIds, id) {
							continue
						}
						if len(post.FileIds) >= maxPostAttachments {
							dropped++
							continue
						}
						post.FileIds = append(post.FileIds, id)
					}
					if dropped > 0 && !attachmentCapWarned {
						attachmentCapWarned = true
						p.mmClient.LogWarn("Streaming truncated attachments over the per-post limit",
							"post_id", post.Id, "dropped", dropped, "limit", maxPostAttachments)
					}
				}
			case llm.EventTypeEnd:
				// Stream has closed cleanly. The "empty" fallback message only
				// applies when the LLM truly produced nothing; a stream that
				// stopped after emitting tool_use blocks (e.g. awaiting user
				// approval) or attached files is a valid response.
				hasToolCalls := acc != nil && len(acc.toolCalls) > 0
				if strings.TrimSpace(post.Message) == "" && !hasToolCalls && len(post.FileIds) == 0 {
					p.mmClient.LogError("LLM closed stream with no result")
					T := i18n.LocalizerFunc(p.i18n, userLocale)
					emptyText := T("agents.stream_to_post_llm_not_return", "Sorry! The LLM did not return a result.")
					post.Message = emptyText
					// Mirror into the accumulator so the turn carries the fallback.
					if acc != nil {
						acc.sequence.AppendText(emptyText)
					}
					p.sendPostStreamingEvent(post, broadcast, map[string]any{"next": post.Message})
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
						acc.sequence.AppendText(separator)
					}
					acc.sequence.AppendText(errorText)
				}

				if err := p.mmClient.UpdatePost(post); err != nil {
					p.mmClient.LogError("Error recovering from streaming error", "error", err)
					return
				}
				p.sendPostStreamingEvent(post, broadcast, map[string]any{"next": post.Message})
				return
			case llm.EventTypeReasoning:
				// Handle reasoning summary chunk - accumulate and stream
				if reasoningChunk, ok := event.Value.(string); ok {
					reasoningBuffer.WriteString(reasoningChunk)
					// Send reasoning event with accumulated text so far
					p.sendPostStreamingEvent(post, broadcast, map[string]any{"control": "reasoning_summary", "reasoning": reasoningBuffer.String()})
					if acc != nil {
						acc.sequence.AppendReasoning(reasoningChunk)
					}
				}
			case llm.EventTypeReasoningEnd:
				// Reasoning summary completed - stream final event and accumulate for turn persistence
				if reasoningData, ok := event.Value.(llm.ReasoningData); ok {
					p.sendPostStreamingEvent(post, broadcast, map[string]any{"control": "reasoning_summary_done", "reasoning": reasoningData.Text})
					reasoningBuffer.Reset()
					if acc != nil {
						acc.sequence.FinishReasoning(reasoningData)
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
						if llm.IsResolvedToolCallBatch(toolCalls) {
							acc.sequence.Reset()
							acc.annotations = nil
							acc.toolCalls = nil
							acc.serverTools = nil
							messageBuilder.Reset()
							post.Message = ""
						} else {
							acc.toolCalls = toolCalls
						}
					}
					p.broadcastToolCalls(post, toolCalls, requesterUserID)
				}
			case llm.EventTypeAnnotations:
				if annotationMap, ok := event.Value.(map[string]any); ok {
					if annotations, hasAnnotations := annotationMap["annotations"].([]llm.Annotation); hasAnnotations {
						if cleanedMsg, hasCleaned := annotationMap["cleanedMessage"].(string); hasCleaned {
							messageBuilder.Reset()
							messageBuilder.WriteString(cleanedMsg)
							post.Message = cleanedMsg
							p.sendPostStreamingEvent(post, broadcast, map[string]any{"next": post.Message})
							if acc != nil {
								originalMsg, hasOriginal := annotationMap["originalMessage"].(string)
								removedRanges, hasRanges := annotationMap["removedTextRanges"].([]llm.TextRange)
								if !hasOriginal || !hasRanges || !acc.sequence.RemoveTextRanges(originalMsg, removedRanges) || acc.sequence.Text() != cleanedMsg {
									p.mmClient.LogWarn("Unable to preserve turn text segments during citation cleanup", "post_id", post.Id)
								}
							}
						}

						annotationsJSON, err := json.Marshal(annotations)
						if err != nil {
							p.mmClient.LogError("Failed to marshal annotations", "error", err)
						} else {
							p.sendPostStreamingEvent(post, broadcast, map[string]any{"control": "annotations", "annotations": string(annotationsJSON)})
						}
						if acc != nil {
							acc.annotations = annotations
						}
					}
				} else if annotations, ok := event.Value.([]llm.Annotation); ok {
					annotationsJSON, err := json.Marshal(annotations)
					if err != nil {
						p.mmClient.LogError("Failed to marshal annotations", "error", err)
					} else {
						p.sendPostStreamingEvent(post, broadcast, map[string]any{"control": "annotations", "annotations": string(annotationsJSON)})
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
			case llm.EventTypeServerToolUse:
				// Provider-executed tool activity (web search / web fetch /
				// code execution). The event carries the cumulative snapshot
				// for the round; sanitize, persist, and broadcast it. Like
				// annotations, server tool activity shares the post text's
				// visibility, so it goes to the whole channel unredacted.
				if rawServerTools, ok := event.Value.([]llm.ServerToolUse); ok {
					// Clone before sanitizing: this event can share FileIDs backing storage with ToolRunner's replay snapshot.
					serverTools := llm.CloneServerToolUses(rawServerTools)
					for i := range serverTools {
						serverTools[i].ProviderRoute = ""
						serverTools[i].Sanitize()
					}
					if acc != nil {
						acc.serverTools = serverTools
						acc.sequence.RecordServerTools(serverTools)
					}
					serverToolsJSON, err := json.Marshal(serverTools)
					if err != nil {
						p.mmClient.LogError("Failed to marshal server tool activity", "error", err)
					} else {
						p.sendPostStreamingEvent(post, broadcast, map[string]any{"control": "server_tool", "server_tool": string(serverToolsJSON)})
					}
				}
			}
		case <-ctx.Done():
			if err := p.mmClient.UpdatePost(post); err != nil {
				p.mmClient.LogError("Error updating post on stop signaled", "error", err)
				return
			}
			p.sendPostStreamingEvent(post, broadcast, map[string]any{"control": PostStreamingControlCancel})
			return
		}
	}
}
