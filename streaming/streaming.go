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
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
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

// WebSearchContextProp is still read by conversations/web_search_context.go when
// extracting web search state from legacy thread posts.
const WebSearchContextProp = "web_search_context"

type Service interface {
	StreamToNewPost(ctx context.Context, botID string, requesterUserID string, stream *llm.TextStreamResult, post *model.Post, respondingToPostID string) error
	StreamToNewDM(ctx context.Context, botID string, stream *llm.TextStreamResult, userID string, post *model.Post, respondingToPostID string) error
	StreamToPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, userLocale string)
	StopStreaming(postID string)
	GetStreamingContext(inCtx context.Context, postID string) (context.Context, error)
	FinishStreaming(postID string)
}

type postStreamContext struct {
	cancel context.CancelFunc
}

// TurnStore is the subset of store operations needed by the streaming layer
// for persisting assistant turns during streaming.
type TurnStore interface {
	CreateTurn(turn *store.Turn) error
	UpdateTurnContent(id string, content json.RawMessage) error
	UpdateTurnTokens(id string, tokensIn, tokensOut int64) error
	GetTurnsForConversation(conversationID string) ([]store.Turn, error)
}

// turnAccumulator collects stream state for turn persistence.
// It is created at stream start and finalized at stream end/error/cancel.
type turnAccumulator struct {
	turnID         string
	conversationID string
	postID         string

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
func (a *turnAccumulator) buildContentBlocks() []conversation.ContentBlock {
	var blocks []conversation.ContentBlock

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

	// 4. Tool call blocks (DM tool progress)
	for _, tc := range a.toolCalls {
		blocks = append(blocks, conversation.ContentBlock{
			Type:         conversation.BlockTypeToolUse,
			ID:           tc.ID,
			Name:         tc.Name,
			ServerOrigin: tc.ServerOrigin,
			Input:        tc.Arguments,
			Status:       conversation.StatusToString(tc.Status),
			Shared:       conversation.BoolPtr(true),
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

	// The callback is already set when creating the context

	ctx, err := p.GetStreamingContext(context.Background(), post.Id)
	if err != nil {
		return err
	}

	go func() {
		defer p.FinishStreaming(post.Id)
		user, err := p.mmClient.GetUser(requesterUserID)
		locale := *p.mmClient.GetConfig().LocalizationSettings.DefaultServerLocale
		if err != nil {
			p.StreamToPost(ctx, stream, post, locale)
			return
		}

		channel, err := p.mmClient.GetChannel(post.ChannelId)
		if err != nil {
			p.StreamToPost(ctx, stream, post, locale)
			return
		}

		if channel.Type == model.ChannelTypeDirect {
			if channel.Name == botID+"__"+user.Id || channel.Name == user.Id+"__"+botID {
				p.StreamToPost(ctx, stream, post, user.Locale)
				return
			}
		}
		p.StreamToPost(ctx, stream, post, locale)
	}()

	return nil
}

func (p *MMPostStreamService) StreamToNewDM(ctx context.Context, botID string, stream *llm.TextStreamResult, userID string, post *model.Post, respondingToPostID string) error {
	// We use ModifyPostForBot directly here to add the responding to post ID
	ModifyPostForBot(botID, userID, post, respondingToPostID)

	if err := p.mmClient.DM(botID, userID, post); err != nil {
		return fmt.Errorf("failed to post DM: %w", err)
	}

	// The callback is already set when creating the context

	ctx, err := p.GetStreamingContext(context.Background(), post.Id)
	if err != nil {
		return err
	}

	go func() {
		defer p.FinishStreaming(post.Id)
		user, err := p.mmClient.GetUser(userID)
		locale := *p.mmClient.GetConfig().LocalizationSettings.DefaultServerLocale
		if err != nil {
			p.StreamToPost(ctx, stream, post, locale)
			return
		}

		channel, err := p.mmClient.GetChannel(post.ChannelId)
		if err != nil {
			p.StreamToPost(ctx, stream, post, locale)
			return
		}

		if channel.Type == model.ChannelTypeDirect {
			if channel.Name == botID+"__"+user.Id || channel.Name == user.Id+"__"+botID {
				p.StreamToPost(ctx, stream, post, user.Locale)
				return
			}
		}
		p.StreamToPost(ctx, stream, post, locale)
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

// createPlaceholderTurn creates a placeholder turn row for the streaming assistant response.
// Returns nil if the turn cannot be created (error is logged).
func (p *MMPostStreamService) createPlaceholderTurn(conversationID, postID string) *turnAccumulator {
	turns, err := p.turnStore.GetTurnsForConversation(conversationID)
	if err != nil {
		p.mmClient.LogError("Failed to get turns for sequence number", "error", err, "conversation_id", conversationID)
		return nil
	}

	nextSeq := 0
	if len(turns) > 0 {
		nextSeq = turns[len(turns)-1].Sequence + 1
	}

	turnID := model.NewId()
	postIDPtr := &postID

	turn := &store.Turn{
		ID:             turnID,
		ConversationID: conversationID,
		PostID:         postIDPtr,
		Role:           "assistant",
		Content:        json.RawMessage("[]"),
		Sequence:       nextSeq,
		CreatedAt:      model.GetMillis(),
	}

	if err := p.turnStore.CreateTurn(turn); err != nil {
		p.mmClient.LogError("Failed to create placeholder turn", "error", err, "conversation_id", conversationID)
		return nil
	}

	return &turnAccumulator{
		turnID:         turnID,
		conversationID: conversationID,
		postID:         postID,
	}
}

// finalizeTurn builds content blocks from accumulated state and persists them.
func (p *MMPostStreamService) finalizeTurn(acc *turnAccumulator) {
	blocks := acc.buildContentBlocks()

	contentJSON, err := json.Marshal(blocks)
	if err != nil {
		p.mmClient.LogError("Failed to marshal turn content blocks", "error", err, "turn_id", acc.turnID)
		return
	}

	if err := p.turnStore.UpdateTurnContent(acc.turnID, contentJSON); err != nil {
		p.mmClient.LogError("Failed to update turn content", "error", err, "turn_id", acc.turnID)
	}

	if acc.tokensIn > 0 || acc.tokensOut > 0 {
		if err := p.turnStore.UpdateTurnTokens(acc.turnID, acc.tokensIn, acc.tokensOut); err != nil {
			p.mmClient.LogError("Failed to update turn tokens", "error", err, "turn_id", acc.turnID)
		}
	}
}

// StreamToPost streams the result of a TextStreamResult to a post.
// it will internally handle logging needs and updating the post.
func (p *MMPostStreamService) StreamToPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, userLocale string) {
	broadcast := &model.WebsocketBroadcast{ChannelId: post.ChannelId}
	p.sendPostStreamingControlEventWithBroadcast(post, PostStreamingControlStart, broadcast)

	// Create turn accumulator if turn persistence is enabled and a conversation_id is set
	var acc *turnAccumulator
	if p.turnStore != nil {
		if convID, ok := post.GetProp(ConversationIDProp).(string); ok && convID != "" {
			acc = p.createPlaceholderTurn(convID, post.Id)
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
				// Stream has closed cleanly
				if strings.TrimSpace(post.Message) == "" {
					p.mmClient.LogError("LLM closed stream with no result")
					T := i18n.LocalizerFunc(p.i18n, userLocale)
					post.Message = T("agents.stream_to_post_llm_not_return", "Sorry! The LLM did not return a result.")
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
				if strings.TrimSpace(post.Message) == "" {
					post.Message = ""
				} else {
					post.Message += "\n\n"
				}
				p.mmClient.LogError("Streaming result to post failed partway", "error", err)
				T := i18n.LocalizerFunc(p.i18n, userLocale)
				post.Message += T("agents.stream_to_post_access_llm_error", "Sorry! An error occurred while accessing the LLM. See server logs for details.")

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
						acc.toolCalls = toolCalls
					}
					toolCallJSON, jsonErr := json.Marshal(toolCalls)
					if jsonErr != nil {
						p.mmClient.LogError("Failed to marshal tool calls", "error", jsonErr)
					} else {
						p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
							"post_id":   post.Id,
							"control":   "tool_call",
							"tool_call": string(toolCallJSON),
						}, broadcast)
					}
				}
			case llm.EventTypeAnnotations:
				// Handle annotations - might include cleaned message for web search citations
				if annotationMap, ok := event.Value.(map[string]interface{}); ok {
					// Web search annotations with cleaned message
					if annotations, hasAnnotations := annotationMap["annotations"].([]llm.Annotation); hasAnnotations {
						if cleanedMsg, hasCleaned := annotationMap["cleanedMessage"].(string); hasCleaned {
							// Replace post message with cleaned version (citation markers removed)
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
