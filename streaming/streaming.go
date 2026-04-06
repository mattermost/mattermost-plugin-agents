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
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
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

const ToolCallProp = "pending_tool_call"
const ToolCallRedactedProp = "pending_tool_call_redacted"
const ToolCallPrivateKeyPrefix = "tool_call_private"
const ToolResultPrivateKeyPrefix = "tool_result_private"
const PendingToolResultProp = "pending_tool_result"
const AutoApprovedToolCallProp = "auto_approved_tool_call"
const AutoShareToolResultProp = "auto_share_tool_result"
const ReasoningSummaryProp = "reasoning_summary"
const AnnotationsProp = "annotations"
const WebSearchContextProp = "web_search_context"
const ReasoningSignatureProp = "reasoning_signature"

func ToolCallPrivateKVKey(postID, requesterID string) string {
	return fmt.Sprintf("%s:%s:%s", ToolCallPrivateKeyPrefix, postID, requesterID)
}

func ToolResultPrivateKVKey(postID, requesterID string) string {
	return fmt.Sprintf("%s:%s:%s", ToolResultPrivateKeyPrefix, postID, requesterID)
}

func RedactToolCalls(toolCalls []llm.ToolCall) []llm.ToolCall {
	redacted := make([]llm.ToolCall, len(toolCalls))
	for i, toolCall := range toolCalls {
		redacted[i] = toolCall
		redacted[i].Arguments = json.RawMessage("{}")
		redacted[i].Result = ""
	}
	return redacted
}

// ToolPolicyChecker looks up the per-tool policy for a given MCP server/tool.
type ToolPolicyChecker interface {
	GetToolPolicy(serverBaseURL string, toolName string) (policy string, enabled bool)
}

// AutoExecuteCallback is called when all tool calls in a batch are auto-approvable.
// It triggers tool execution without user approval.
// Parameters: postID, requesterID, approvedToolIDs (the exact IDs approved in this batch)
type AutoExecuteCallback func(postID string, requesterID string, approvedToolIDs []string)

// ToolPolicyFunc is a function adapter that implements ToolPolicyChecker.
type ToolPolicyFunc func(serverBaseURL string, toolName string) (string, bool)

func (f ToolPolicyFunc) GetToolPolicy(serverBaseURL string, toolName string) (string, bool) {
	return f(serverBaseURL, toolName)
}

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
	contexts            map[string]postStreamContext
	contextsMutex       sync.Mutex
	mmClient            Client
	i18n                *i18n.Bundle
	toolPolicyChecker   ToolPolicyChecker
	autoExecuteCallback AutoExecuteCallback
}

func NewMMPostStreamService(mmClient Client, i18n *i18n.Bundle) *MMPostStreamService {
	return &MMPostStreamService{
		contexts: make(map[string]postStreamContext),
		mmClient: mmClient,
		i18n:     i18n,
	}
}

// SetToolPolicyChecker sets the tool policy checker for the streaming service.
func (p *MMPostStreamService) SetToolPolicyChecker(checker ToolPolicyChecker) {
	p.toolPolicyChecker = checker
}

// SetAutoExecuteCallback sets the callback that will be invoked when all tool calls
// in a batch are auto-approvable.
func (p *MMPostStreamService) SetAutoExecuteCallback(callback AutoExecuteCallback) {
	p.autoExecuteCallback = callback
}

// areAllToolCallsAutoApprovable checks if all tool calls in the batch
// can be auto-approved. Returns false if any tool is not auto_run + enabled,
// or if the policy checker is not configured.
func toolPolicyAllowsAutoRun(policy string, enabled bool) bool {
	return mcp.IsToolPolicyAutoRun(policy) && enabled
}

func toolPolicyRequiresResultReview(policy string) bool {
	return !mcp.IsToolPolicyAutoRunEverywhere(policy)
}

func (p *MMPostStreamService) shouldAutoApproveToolCall(toolCall llm.ToolCall) bool {
	if p.toolPolicyChecker == nil {
		return false
	}

	policy, enabled := p.toolPolicyChecker.GetToolPolicy(toolCall.ServerOrigin, toolCall.Name)
	autoRun := toolPolicyAllowsAutoRun(policy, enabled)
	if p.mmClient != nil {
		p.mmClient.LogDebug("Auto-approval check",
			"tool_name", toolCall.Name,
			"server_origin", toolCall.ServerOrigin,
			"approved", fmt.Sprintf("%t", autoRun),
			"requires_result_review", fmt.Sprintf("%t", toolPolicyRequiresResultReview(policy)),
		)
	}

	return autoRun
}

func (p *MMPostStreamService) areAllToolCallsAutoApprovable(toolCalls []llm.ToolCall) bool {
	if p.toolPolicyChecker == nil {
		return false
	}
	if len(toolCalls) == 0 {
		return false
	}
	for _, tc := range toolCalls {
		if !p.shouldAutoApproveToolCall(tc) {
			return false
		}
	}
	return true
}

// markAutoApprovedStatusesAndCheck combines status marking and
// areAllToolCallsAutoApprovable in a single pass over the tool calls.
// It upgrades successful tools to AutoApproved when the policy allows,
// and returns true only if ALL tools are auto_run + enabled.
func (p *MMPostStreamService) markAutoApprovedStatusesAndCheck(toolCalls []llm.ToolCall) bool {
	if p.toolPolicyChecker == nil || len(toolCalls) == 0 {
		return false
	}

	allAutoApprovable := true
	for i := range toolCalls {
		isAutoRun := p.shouldAutoApproveToolCall(toolCalls[i])

		if toolCalls[i].Status == llm.ToolCallStatusSuccess && isAutoRun {
			toolCalls[i].Status = llm.ToolCallStatusAutoApproved
		}

		if !isAutoRun {
			allAutoApprovable = false
		}
	}

	return allAutoApprovable
}

func (p *MMPostStreamService) anyToolResultRequiresReview(toolCalls []llm.ToolCall) bool {
	if p.toolPolicyChecker == nil || len(toolCalls) == 0 {
		return true
	}

	for _, tc := range toolCalls {
		policy, enabled := p.toolPolicyChecker.GetToolPolicy(tc.ServerOrigin, tc.Name)
		if !toolPolicyAllowsAutoRun(policy, enabled) || toolPolicyRequiresResultReview(policy) {
			return true
		}
	}

	return false
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

// handleAutoApprovedToolCalls handles tool calls that were pre-executed by the
// MCP auto-approval wrapper. It skips the call-approval UI and either enters the
// result-sharing stage or auto-shares immediately depending on the tool policies.
func (p *MMPostStreamService) handleAutoApprovedToolCalls(post *model.Post, toolCalls []llm.ToolCall, broadcast *model.WebsocketBroadcast) {
	requesterID, ok := post.GetProp(LLMRequesterUserID).(string)
	if !ok || requesterID == "" {
		p.mmClient.LogError("Missing requester ID for auto-approved tool call", "post_id", post.Id)
		return
	}

	// Convert AutoApproved status to Success for downstream processing
	for i := range toolCalls {
		if toolCalls[i].Status == llm.ToolCallStatusAutoApproved {
			toolCalls[i].Status = llm.ToolCallStatusSuccess
		}
	}

	// Store full results in the result KV key (consumed by HandleToolResult)
	resultKVKey := ToolResultPrivateKVKey(post.Id, requesterID)
	if kvErr := p.mmClient.KVSet(resultKVKey, toolCalls); kvErr != nil {
		p.mmClient.LogError("Failed to store auto-approved tool results", "error", kvErr, "post_id", post.Id)
		return
	}

	// Store full tool calls in the call KV key (for cleanup in HandleToolResult)
	callKVKey := ToolCallPrivateKVKey(post.Id, requesterID)
	if kvErr := p.mmClient.KVSet(callKVKey, toolCalls); kvErr != nil {
		p.mmClient.LogError("Failed to store auto-approved tool call data", "error", kvErr, "post_id", post.Id)
		return
	}

	// Redact for post display
	redactedTools := RedactToolCalls(toolCalls)
	toolCallJSON, err := json.Marshal(redactedTools)
	if err != nil {
		p.mmClient.LogError("Failed to marshal auto-approved tool call", "error", err)
		return
	}

	post.AddProp(ToolCallProp, string(toolCallJSON))
	post.AddProp(ToolCallRedactedProp, "true")
	post.AddProp(AutoApprovedToolCallProp, "true")
	requiresResultReview := p.anyToolResultRequiresReview(toolCalls)
	if requiresResultReview {
		// Set up result-sharing stage: the post shows redacted tools with
		// PendingToolResultProp so the frontend presents the result-approval UI
		// instead of the call-approval UI.
		post.AddProp(PendingToolResultProp, "true")
		post.DelProp(AutoShareToolResultProp)
	} else {
		// The new everywhere policy runs to completion in channels too.
		post.DelProp(PendingToolResultProp)
		post.AddProp(AutoShareToolResultProp, "true")
	}

	if err := p.mmClient.UpdatePost(post); err != nil {
		p.mmClient.LogError("Failed to update post with auto-approved tool call", "error", err)
		return
	}

	// Send websocket event for the result-sharing UI
	p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
		"post_id":   post.Id,
		"control":   "tool_call",
		"tool_call": string(toolCallJSON),
	}, broadcast)

	p.mmClient.LogDebug("Auto-approved MCP tool calls executed", "post_id", post.Id, "tool_count", len(toolCalls))

	if !requiresResultReview && p.autoExecuteCallback != nil {
		approvedIDs := make([]string, len(toolCalls))
		for idx := range toolCalls {
			approvedIDs[idx] = toolCalls[idx].ID
		}
		go p.autoExecuteCallback(post.Id, requesterID, approvedIDs)
	}
}

// StreamToPost streams the result of a TextStreamResult to a post.
// it will internally handle logging needs and updating the post.
func (p *MMPostStreamService) StreamToPost(ctx context.Context, stream *llm.TextStreamResult, post *model.Post, userLocale string) {
	broadcast := &model.WebsocketBroadcast{ChannelId: post.ChannelId}
	p.sendPostStreamingControlEventWithBroadcast(post, PostStreamingControlStart, broadcast)
	defer func() {
		p.sendPostStreamingControlEventWithBroadcast(post, PostStreamingControlEnd, broadcast)
	}()

	var messageBuilder strings.Builder
	messageBuilder.Grow(4096) // Pre-allocate for typical response size
	var reasoningBuffer strings.Builder
	var isDMWithBot bool
	var checkedChannelType bool
	var dmToolCalls []llm.ToolCall // accumulated tool calls for DM progress display

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
				}
			case llm.EventTypeEnd:
				// Stream has closed cleanly
				if strings.TrimSpace(post.Message) == "" {
					p.mmClient.LogError("LLM closed stream with no result")
					T := i18n.LocalizerFunc(p.i18n, userLocale)
					post.Message = T("agents.stream_to_post_llm_not_return", "Sorry! The LLM did not return a result.")
					p.sendPostStreamingUpdateEventWithBroadcast(post, post.Message, broadcast)
				}

				// Inline citations have already been cleaned in EventTypeAnnotations handler
				// (if there were any citations, they were cleaned before annotations were sent)

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
				T := i18n.LocalizerFunc(p.i18n, userLocale)
				post.Message += T("agents.stream_to_post_access_llm_error", "Sorry! An error occurred while accessing the LLM. See server logs for details.")

				// Persist any accumulated reasoning before erroring out
				if reasoningBuffer.Len() > 0 {
					post.AddProp(ReasoningSummaryProp, reasoningBuffer.String())
					p.mmClient.LogDebug("Saved partial reasoning summary on error", "post_id", post.Id, "reasoning_length", reasoningBuffer.Len())
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
				}
			case llm.EventTypeReasoningEnd:
				// Reasoning summary completed - stream final and persist
				if reasoningData, ok := event.Value.(llm.ReasoningData); ok {
					// Send final reasoning event (only text goes to frontend)
					p.sendPostStreamingReasoningEventWithBroadcast(post, reasoningData.Text, "reasoning_summary_done", broadcast)

					// Persist reasoning summary and signature to post props
					// This will be saved when the post is updated at the end of the stream
					if reasoningData.Text != "" {
						post.AddProp(ReasoningSummaryProp, reasoningData.Text)
						p.mmClient.LogDebug("Added reasoning summary to post props", "post_id", post.Id, "reasoning_length", len(reasoningData.Text))
					}
					if reasoningData.Signature != "" {
						post.AddProp(ReasoningSignatureProp, reasoningData.Signature)
						p.mmClient.LogDebug("Added reasoning signature to post props", "post_id", post.Id)
					}
					reasoningBuffer.Reset()
				}
			case llm.EventTypeToolCalls:
				// Handle tool call event
				if toolCalls, ok := event.Value.([]llm.ToolCall); ok {
					// Check if these tool calls were auto-approved by the MCP auto-approval wrapper.
					// Auto-approved tools have already been executed and have results populated.
					preExecuted := llm.HasPreExecutedToolCalls(toolCalls)

					// Preserve non-pending statuses emitted by wrappers (e.g., auto-run
					// success/error) so UI state can transition from spinner to final state.
					// Raw model-emitted tool calls already use zero-value Pending.

					for i := range toolCalls {
						toolCalls[i].SanitizeArguments()
					}

					// Determine channel type once
					if !checkedChannelType {
						channel, chErr := p.mmClient.GetChannel(post.ChannelId)
						if chErr != nil {
							p.mmClient.LogError("Failed to get channel for tool call handling", "error", chErr, "post_id", post.Id, "channel_id", post.ChannelId)
							return
						}
						isDMWithBot = mmapi.IsDMWith(post.UserId, channel)
						checkedChannelType = true
					}

					if preExecuted && !isDMWithBot {
						// Auto-approved in channel (pre-executed by wrapper): skip call-approval, set up result-sharing
						p.handleAutoApprovedToolCalls(post, toolCalls, broadcast)
						return
					}

					if isDMWithBot {
						// DM: show tool call progress without stopping the stream.
						// Merge into accumulated list so all batches stay visible and
						// pending->resolved transitions update in place.
						for _, tc := range toolCalls {
							updated := false
							for j := range dmToolCalls {
								if dmToolCalls[j].ID == tc.ID {
									dmToolCalls[j] = tc
									updated = true
									break
								}
							}
							if !updated {
								dmToolCalls = append(dmToolCalls, tc)
							}
						}
						allAutoApprovable := p.markAutoApprovedStatusesAndCheck(dmToolCalls)

						toolCallJSON, jsonErr := json.Marshal(dmToolCalls)
						if jsonErr != nil {
							p.mmClient.LogError("Failed to marshal DM tool calls", "error", jsonErr)
						} else {
							post.AddProp(ToolCallProp, string(toolCallJSON))
							if allAutoApprovable {
								post.AddProp(AutoApprovedToolCallProp, "true")
							} else {
								post.DelProp(AutoApprovedToolCallProp)
							}
							if updErr := p.mmClient.UpdatePost(post); updErr != nil {
								p.mmClient.LogError("Failed to update post with tool call", "error", updErr)
							}
							p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
								"post_id":   post.Id,
								"control":   "tool_call",
								"tool_call": string(toolCallJSON),
							}, broadcast)
						}
						// Continue processing the stream - don't return
					} else {
						// Channel/GM: standard approval flow with redaction
						requesterID, ok := post.GetProp(LLMRequesterUserID).(string)
						if !ok || requesterID == "" {
							p.mmClient.LogError("Missing requester ID for tool call, cannot persist private data", "post_id", post.Id)
							return
						}
						kvKey := ToolCallPrivateKVKey(post.Id, requesterID)
						if kvErr := p.mmClient.KVSet(kvKey, toolCalls); kvErr != nil {
							p.mmClient.LogError("Failed to store tool calls in KV store, cannot continue", "error", kvErr, "post_id", post.Id, "kv_key", kvKey)
							return
						}
						autoApproved := p.markAutoApprovedStatusesAndCheck(toolCalls)
						requiresResultReview := p.anyToolResultRequiresReview(toolCalls)

						toolCallsForPost := RedactToolCalls(toolCalls)
						post.AddProp(ToolCallRedactedProp, "true")
						if autoApproved {
							post.AddProp(AutoApprovedToolCallProp, "true")
							if !requiresResultReview {
								post.AddProp(AutoShareToolResultProp, "true")
							} else {
								post.DelProp(AutoShareToolResultProp)
							}
						} else {
							post.DelProp(AutoApprovedToolCallProp)
							post.DelProp(AutoShareToolResultProp)
						}

						toolCallJSON, jsonErr := json.Marshal(toolCallsForPost)
						if jsonErr != nil {
							p.mmClient.LogError("Failed to marshal tool call", "error", jsonErr)
						} else {
							post.AddProp(ToolCallProp, string(toolCallJSON))
						}

						if updErr := p.mmClient.UpdatePost(post); updErr != nil {
							p.mmClient.LogError("Failed to update post with tool call", "error", updErr)
							return
						}

						p.mmClient.PublishWebSocketEvent("postupdate", map[string]interface{}{
							"post_id":   post.Id,
							"control":   "tool_call",
							"tool_call": string(toolCallJSON),
						}, broadcast)

						if autoApproved && p.autoExecuteCallback != nil {
							approvedIDs := make([]string, len(toolCalls))
							for idx := range toolCalls {
								approvedIDs[idx] = toolCalls[idx].ID
							}
							go p.autoExecuteCallback(post.Id, requesterID, approvedIDs)
						}
						return
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
							p.mmClient.LogDebug("Replaced post message with cleaned version", "post_id", post.Id, "original_length", len(post.Message), "cleaned_length", len(cleanedMsg))
						}

						annotationsJSON, err := json.Marshal(annotations)
						if err != nil {
							p.mmClient.LogError("Failed to marshal annotations", "error", err)
						} else {
							post.AddProp(AnnotationsProp, string(annotationsJSON))
							p.mmClient.LogDebug("Added annotations to post props", "post_id", post.Id, "count", len(annotations))
							p.sendPostStreamingAnnotationsEventWithBroadcast(post, string(annotationsJSON), broadcast)
						}
					}
				} else if annotations, ok := event.Value.([]llm.Annotation); ok {
					// Regular annotations without cleaned message
					annotationsJSON, err := json.Marshal(annotations)
					if err != nil {
						p.mmClient.LogError("Failed to marshal annotations", "error", err)
					} else {
						post.AddProp(AnnotationsProp, string(annotationsJSON))
						p.mmClient.LogDebug("Added annotations to post props", "post_id", post.Id, "count", len(annotations))
						p.sendPostStreamingAnnotationsEventWithBroadcast(post, string(annotationsJSON), broadcast)
					}
				}
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
			p.sendPostStreamingControlEventWithBroadcast(post, PostStreamingControlCancel, broadcast)
			return
		}
	}
}
