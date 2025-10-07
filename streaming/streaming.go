// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const PostStreamingControlCancel = "cancel"
const PostStreamingControlEnd = "end"
const PostStreamingControlStart = "start"

const ToolCallProp = "pending_tool_call"
const ReasoningSummaryProp = "reasoning_summary"

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

var ErrAlreadyStreamingToPost = fmt.Errorf("already streaming to post")

type MMPostStreamService struct {
	contexts      map[string]postStreamContext
	contextsMutex sync.Mutex
	mmClient      mmapi.Client
	i18n          *i18n.Bundle
}

func NewMMPostStreamService(mmClient mmapi.Client, i18n *i18n.Bundle) *MMPostStreamService {
	return &MMPostStreamService{
		contexts: make(map[string]postStreamContext),
		mmClient: mmClient,
		i18n:     i18n,
	}
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

func (p *MMPostStreamService) sendPostStreamingUpdateEvent(post *model.Post, message string) {
	p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
		"post_id": post.Id,
		"next":    message,
	}, &model.WebsocketBroadcast{
		ChannelId: post.ChannelId,
	})
}

func (p *MMPostStreamService) sendPostStreamingControlEvent(post *model.Post, control string) {
	p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
		"post_id": post.Id,
		"control": control,
	}, &model.WebsocketBroadcast{
		ChannelId: post.ChannelId,
	})
}

func (p *MMPostStreamService) sendPostStreamingReasoningEvent(post *model.Post, reasoning string, control string) {
	p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
		"post_id":   post.Id,
		"control":   control,
		"reasoning": reasoning,
	}, &model.WebsocketBroadcast{
		ChannelId: post.ChannelId,
	})
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
	delete(p.contexts, postID)
}

// StreamToPost streams the result of a TextStreamResult to a post.
// it will internally handle logging needs and updating the post.
func (p *MMPostStreamService) StreamToPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, userLocale string) {
	T := i18n.LocalizerFunc(p.i18n, userLocale)
	p.sendPostStreamingControlEvent(post, PostStreamingControlStart)
	defer func() {
		p.sendPostStreamingControlEvent(post, PostStreamingControlEnd)
	}()

	var reasoningBuffer strings.Builder

	for {
		select {
		case event := <-stream.Stream:
			switch event.Type {
			case llm.EventTypeText:
				// Handle text event
				if textChunk, ok := event.Value.(string); ok {
					post.Message += textChunk
					p.sendPostStreamingUpdateEvent(post, post.Message)
				}
			case llm.EventTypeEnd:
				// Stream has closed cleanly
				if strings.TrimSpace(post.Message) == "" {
					p.mmClient.LogError("LLM closed stream with no result")
					post.Message = T("agents.stream_to_post_llm_not_return", "Sorry! The LLM did not return a result.")
					p.sendPostStreamingUpdateEvent(post, post.Message)
				}
				// Update post with all accumulated data
				// This includes the message and any reasoning that was added to props in EventTypeReasoningEnd
				if reasoningProp := post.GetProp(ReasoningSummaryProp); reasoningProp != nil {
					p.mmClient.LogDebug("Persisting post with reasoning summary", "post_id", post.Id)
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
				post.Message = T("agents.stream_to_post_access_llm_error", "Sorry! An error occurred while accessing the LLM. See server logs for details.")

				// Persist any accumulated reasoning before erroring out
				if reasoningBuffer.Len() > 0 {
					post.AddProp(ReasoningSummaryProp, reasoningBuffer.String())
					p.mmClient.LogDebug("Saved partial reasoning summary on error", "post_id", post.Id, "reasoning_length", reasoningBuffer.Len())
				}

				if err := p.mmClient.UpdatePost(post); err != nil {
					p.mmClient.LogError("Error recovering from streaming error", "error", err)
					return
				}
				p.sendPostStreamingUpdateEvent(post, post.Message)
				return
			case llm.EventTypeReasoning:
				// Handle reasoning summary chunk - accumulate and stream
				if reasoningChunk, ok := event.Value.(string); ok {
					reasoningBuffer.WriteString(reasoningChunk)
					// Send reasoning event with accumulated text so far
					p.sendPostStreamingReasoningEvent(post, reasoningBuffer.String(), "reasoning_summary")
				}
			case llm.EventTypeReasoningEnd:
				// Reasoning summary completed - stream final and persist
				if reasoningText, ok := event.Value.(string); ok {
					// Send final reasoning event
					p.sendPostStreamingReasoningEvent(post, reasoningText, "reasoning_summary_done")

					// Persist reasoning summary to post props
					// This will be saved when the post is updated at the end of the stream
					if reasoningText != "" {
						post.AddProp(ReasoningSummaryProp, reasoningText)
						p.mmClient.LogDebug("Added reasoning summary to post props", "post_id", post.Id, "reasoning_length", len(reasoningText))
					}
					reasoningBuffer.Reset()
				}
			case llm.EventTypeToolCalls:
				// Handle tool call event
				if toolCalls, ok := event.Value.([]llm.ToolCall); ok {
					// Ensure all tool calls have Pending status
					for i := range toolCalls {
						toolCalls[i].Status = llm.ToolCallStatusPending
					}

					// Add the tool call as a prop to the post
					toolCallJSON, err := json.Marshal(toolCalls)
					if err != nil {
						p.mmClient.LogError("Failed to marshal tool call", "error", err)
					} else {
						post.AddProp(ToolCallProp, string(toolCallJSON))
					}

					// Update the post with the tool call and any reasoning that was previously added
					if err := p.mmClient.UpdatePost(post); err != nil {
						p.mmClient.LogError("Failed to update post with tool call", "error", err)
					}

					// Send websocket event with tool call data
					p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
						"post_id":   post.Id,
						"control":   "tool_call",
						"tool_call": string(toolCallJSON),
					}, &model.WebsocketBroadcast{
						ChannelId: post.ChannelId,
					})
				}
				return
			}
		case <-ctx.Done():
			// Persist any accumulated reasoning before canceling
			if reasoningBuffer.Len() > 0 {
				post.AddProp(ReasoningSummaryProp, reasoningBuffer.String())
				p.mmClient.LogDebug("Saved partial reasoning summary on cancel", "post_id", post.Id, "reasoning_length", reasoningBuffer.Len())
			}

			if err := p.mmClient.UpdatePost(post); err != nil {
				p.mmClient.LogError("Error updating post on stop signaled", "error", err)
				return
			}
			p.sendPostStreamingControlEvent(post, PostStreamingControlCancel)
			return
		}
	}
}
