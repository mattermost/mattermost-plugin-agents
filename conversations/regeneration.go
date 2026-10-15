// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	stdcontext "context"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/channels"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/trace"
)

// HandleRegenerate handles post regeneration requests
func (c *Conversations) HandleRegenerate(ctx stdcontext.Context, userID string, post *model.Post, channel *model.Channel) error {
	// Resume into the originating run's trace so the regenerated work shows
	// up alongside the original invocation.
	ctx = c.rehydrateRunTrace(ctx, post)
	ctx, span := telemetry.Tracer().Start(ctx, "handle regenerate",
		trace.WithNewRoot(),
		trace.WithAttributes(telemetry.PostID.String(post.Id)),
	)
	defer span.End()

	bot := c.bots.GetBotByID(post.UserId)
	if bot == nil {
		return fmt.Errorf("unable to get bot")
	}

	// Fail closed: all regeneration ownership checks must pass.
	if c.convService == nil {
		return errors.New("conversation service not available for regeneration ownership check")
	}
	convIDProp, _ := post.GetProp(streaming.ConversationIDProp).(string)
	if convIDProp == "" {
		return errors.New("post missing conversation_id for ownership check")
	}
	conv, err := c.convService.GetConversation(convIDProp)
	if err != nil {
		return fmt.Errorf("failed to get conversation for ownership check: %w", err)
	}
	if conv.UserID != userID {
		return errors.New("only the original poster can regenerate")
	}

	if post.GetProp(streaming.NoRegen) != nil {
		return errors.New("tagged no regen")
	}

	// Follow-up replies share the analysis/meeting conversation but are
	// ordinary conversation responses; only the operation's initial response
	// is regenerated through the operation-specific path.
	operation := conv.Operation
	if operation != "" {
		turns, turnsErr := c.convService.GetTurns(conv.ID)
		if turnsErr != nil {
			return fmt.Errorf("failed to get turns for regeneration: %w", turnsErr)
		}
		if !isInitialResponse(turns, post.Id) {
			operation = ""
		}
	}

	if err = c.checkRegenerateLicense(operation); err != nil {
		return err
	}

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return fmt.Errorf("unable to get user to regen post: %w", err)
	}

	ctx, err = c.streamingService.GetStreamingContext(ctx, post.Id)
	if err != nil {
		return fmt.Errorf("unable to get post streaming context: %w", err)
	}
	defer c.streamingService.FinishStreaming(post.Id)

	var result *llm.TextStreamResult
	switch operation {
	case llm.OperationThreadAnalysis:
		// The stored user turn holds the thread content; re-check the
		// requester can still read the analyzed thread.
		if threadID, _ := post.GetProp(ThreadIDProp).(string); threadID != "" {
			threadPost, getPostErr := c.mmClient.GetPost(threadID)
			if getPostErr != nil {
				return fmt.Errorf("could not get thread post on regen: %w", getPostErr)
			}
			if !c.mmClient.HasPermissionToChannel(userID, threadPost.ChannelId, model.PermissionReadChannel) {
				return errors.New("user doesn't have permission to read the channel of the original thread")
			}
		}
		result, err = c.regenerateSingleShot(ctx, bot, user, channel, conv, post)
		if err != nil {
			return fmt.Errorf("could not regenerate thread analysis: %w", err)
		}

	case llm.OperationMeetingSummary:
		// The stored user turn is derived from a recording or captions file;
		// the requester must still be allowed to download that source.
		if accessErr := CheckMeetingSourceAccess(ctx, c.mmClient, userID, post); accessErr != nil {
			return accessErr
		}
		post.Message = ""
		result, err = c.regenerateSingleShot(ctx, bot, user, channel, conv, post)
		if err != nil {
			return fmt.Errorf("could not regenerate %s: %w", conv.Operation, err)
		}

	case llm.OperationChannelInterval:
		// The stored user turn already holds the formatted source (meeting
		// transcription or channel posts), so no source files are re-read.
		post.Message = ""
		result, err = c.regenerateSingleShot(ctx, bot, user, channel, conv, post)
		if err != nil {
			return fmt.Errorf("could not regenerate %s: %w", conv.Operation, err)
		}

	case llm.OperationChannelSummary:
		post.Message = ""
		result, err = c.regenerateChannelAnalysis(ctx, bot, user, conv, post)
		if err != nil {
			return fmt.Errorf("could not regenerate channel summary: %w", err)
		}

	default:
		post.Message = ""

		respondingToPostID, ok := post.GetProp(streaming.RespondingToProp).(string)
		if !ok {
			return errors.New("post missing responding to prop")
		}

		result, err = c.regenerateViaConversation(ctx, bot, user, channel, post, respondingToPostID)
		if err != nil {
			return fmt.Errorf("could not regenerate via conversation: %w", err)
		}
	}

	if mmapi.IsDMWith(bot.GetMMBot().UserId, channel) {
		if channel.Name == bot.GetMMBot().UserId+"__"+user.Id || channel.Name == user.Id+"__"+bot.GetMMBot().UserId {
			c.streamingService.StreamToPost(ctx, result, post, user.Locale, user.Id)
			return nil
		}
	}

	config := c.mmClient.GetConfig()
	c.streamingService.StreamToPost(ctx, result, post, *config.LocalizationSettings.DefaultServerLocale, user.Id)

	return nil
}

// Meeting post props, mirrored from the meetings package (which imports this
// package, so it cannot be imported here).
const (
	meetingRecordingFileIDProp  = "referenced_recording_file_id"
	meetingTranscriptPostIDProp = "referenced_transcript_post_id"
)

// HasMeetingSourceReference reports whether post carries the reference to the
// recording or captions a meeting summary was built from.
func HasMeetingSourceReference(post *model.Post) bool {
	recording, _ := post.GetProp(meetingRecordingFileIDProp).(string)
	transcript, _ := post.GetProp(meetingTranscriptPostIDProp).(string)
	return recording != "" || transcript != ""
}

// CheckMeetingSourceAccess fails closed unless userID can read the channel of
// the recording or captions file the meeting summary post was built from and
// the session in ctx may download it.
func CheckMeetingSourceAccess(ctx stdcontext.Context, mmClient mmapi.Client, userID string, post *model.Post) error {
	fileID, _ := post.GetProp(meetingRecordingFileIDProp).(string)
	if fileID == "" {
		transcriptPostID, _ := post.GetProp(meetingTranscriptPostIDProp).(string)
		if transcriptPostID == "" {
			return errors.New("meeting summary post missing its source reference")
		}
		transcriptPost, err := mmClient.GetPost(transcriptPostID)
		if err != nil {
			return fmt.Errorf("could not get transcription post: %w", err)
		}
		fileID = captionsFileID(transcriptPost)
		if fileID == "" {
			return errors.New("transcription post has no captions file")
		}
	}

	mm := mmapi.WithFilePolicy(mmClient, auth.SessionIDFromContext(ctx))
	fileInfo, err := mm.GetFileInfo(fileID)
	if err != nil {
		return fmt.Errorf("could not access meeting source file: %w", err)
	}
	if !mmClient.HasPermissionToChannel(userID, fileInfo.ChannelId, model.PermissionReadChannel) {
		return errors.New("user can't read the meeting source file's channel")
	}
	return nil
}

// captionsFileID returns the first captions file id on a calls transcription
// post, or "" when absent or malformed.
func captionsFileID(post *model.Post) string {
	captions, _ := post.GetProp("captions").([]any)
	if len(captions) == 0 {
		return ""
	}
	caption, _ := captions[0].(map[string]any)
	fileID, _ := caption["file_id"].(string)
	return fileID
}

// isInitialResponse reports whether the assistant turn anchored to postID
// answers the conversation's first user turn. A post with no anchored turn is
// treated as initial only while the conversation has a single user turn, so an
// unpersisted follow-up still uses ordinary conversation regeneration.
func isInitialResponse(turns []store.Turn, postID string) bool {
	userTurns := 0
	for _, turn := range turns {
		if turn.Role == "assistant" && turn.PostID != nil && *turn.PostID == postID {
			return userTurns <= 1
		}
		if turn.Role == "user" {
			userTurns++
		}
	}
	return userTurns <= 1
}

// checkRegenerateLicense gates regenerating a post by the capability of the
// operation that produced it. Thread summaries are available at Professional
// and above, channel summaries at Professional and above, and transcription
// summaries at Enterprise and above. Other conversation posts are not gated.
func (c *Conversations) checkRegenerateLicense(operation string) error {
	switch operation {
	case llm.OperationMeetingSummary:
		return c.licenseChecker.Check(enterprise.CapMeetings)
	case llm.OperationThreadAnalysis:
		return c.licenseChecker.Check(enterprise.CapThreadSummarization)
	case llm.OperationChannelSummary, llm.OperationChannelInterval:
		return c.licenseChecker.Check(enterprise.CapChannelSummarization)
	}
	return nil
}

// regenerateSingleShot re-runs a tool-less, single-turn conversation (thread
// analysis, meeting summary, channel interval) from its stored system prompt
// and user turn, scrubbing the prior response anchored to post.
func (c *Conversations) regenerateSingleShot(
	ctx stdcontext.Context,
	bot *bots.Bot,
	user *model.User,
	channel *model.Channel,
	conv *store.Conversation,
	post *model.Post,
) (*llm.TextStreamResult, error) {
	llmContext := c.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		channel,
		c.contextBuilder.WithLLMContextNoTools(),
	)

	// Build before scrubbing: ExcludeAfterPostID needs the anchor.
	completionReq, err := c.convService.BuildCompletionRequest(conv, llmContext, conversation.BuildOptions{
		ExcludeAfterPostID: post.Id,
		SessionID:          auth.SessionIDFromContext(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build completion request for regen: %w", err)
	}
	completionReq.OperationSubType = llm.SubTypeRegenerate

	if delErr := c.convService.DeleteResponseTurns(conv.ID, post.Id); delErr != nil {
		c.mmClient.LogError("Failed to scrub prior response turns on regen", "error", delErr.Error(), "post_id", post.Id, "conversation_id", conv.ID)
	}

	return bot.LLM().ChatCompletion(ctx, *completionReq, llm.WithToolsDisabled())
}

// regenerateChannelAnalysis re-runs a channel summary in its conversation,
// re-binding the analysis tools to the channel recorded on the post.
func (c *Conversations) regenerateChannelAnalysis(
	ctx stdcontext.Context,
	bot *bots.Bot,
	user *model.User,
	conv *store.Conversation,
	post *model.Post,
) (*llm.TextStreamResult, error) {
	analyzedChannelID, _ := post.GetProp(AnalyzedChannelIDProp).(string)
	if analyzedChannelID == "" {
		return nil, errors.New("post missing analyzed channel")
	}
	if !c.mmClient.HasPermissionToChannel(user.Id, analyzedChannelID, model.PermissionReadChannel) {
		return nil, errors.New("user doesn't have permission to read the analyzed channel")
	}
	analyzedChannel, err := c.mmClient.GetChannel(analyzedChannelID)
	if err != nil {
		return nil, fmt.Errorf("could not get analyzed channel: %w", err)
	}

	toolBot := channels.ToolBot(bot)
	llmContext := c.contextBuilder.BuildLLMContextUserRequest(
		bot,
		user,
		analyzedChannel,
		c.contextBuilder.WithLLMContextPreloadedMCPTools(channels.RequiredMCPTools),
		c.contextBuilder.WithLLMContextTools(ctx, toolBot),
	)

	analyzer := channels.New(bot.LLM(), c.prompts, c.mmClient, c.db, c.convService)
	return analyzer.RegenerateAnalysis(ctx, llmContext, conv, analyzedChannelID, post.Id, auth.SessionIDFromContext(ctx))
}

// regenerateViaConversation rebuilds the completion request from the conversation entity
// and runs the ToolRunner to produce a new response stream.
func (c *Conversations) regenerateViaConversation(
	ctx stdcontext.Context,
	bot *bots.Bot,
	user *model.User,
	channel *model.Channel,
	post *model.Post,
	respondingToPostID string,
) (*llm.TextStreamResult, error) {
	convIDProp, _ := post.GetProp(streaming.ConversationIDProp).(string)
	if convIDProp == "" {
		return nil, errors.New("post missing conversation_id for regeneration")
	}

	conv, err := c.convService.GetConversation(convIDProp)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation for regen: %w", err)
	}

	// Regeneration is triggered by the requester clicking the regen control,
	// so the user is interactively present.
	llmContext := c.buildConversationContextWithTools(
		ctx, bot, user, channel,
		"Failed to load user tool preferences on regen, proceeding without filtering",
		append([]llm.ContextOption{
			c.contextBuilder.WithLLMContextInteractive(),
			c.contextBuilder.WithLLMContextResponseFiles(),
		}, c.conversationToolOptions(bot, conv)...)...,
	)

	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)
	toolsDisabled := applyToolAvailability(llmContext, isDM, c.channelMentionToolCallingEnabled())

	// Build the request BEFORE scrubbing — ExcludeAfterPostID needs the anchor.
	// AllowUnsharedToolContent on DMs is a no-op (DM tool_results are shared)
	// but documents intent.
	completionReq, buildErr := c.convService.BuildCompletionRequest(conv, llmContext, conversation.BuildOptions{
		ExcludeAfterPostID:       post.Id,
		AllowUnsharedToolContent: isDM,
		SessionID:                auth.SessionIDFromContext(ctx),
	})
	if buildErr != nil {
		return nil, fmt.Errorf("failed to build completion request for regen: %w", buildErr)
	}

	// Scrub the prior generation so the stream runs identically to a first.
	if delErr := c.convService.DeleteResponseTurns(conv.ID, post.Id); delErr != nil {
		c.mmClient.LogError("Failed to scrub prior response turns on regen", "error", delErr.Error(), "post_id", post.Id, "conversation_id", conv.ID)
	}

	// Clear prior attachments so the regenerated response starts clean.
	// Safe: conversation-flow bot posts only ever carry CreateFile
	// attachments (meeting-summary regen never reaches this path).
	// An explicit empty list — not nil — tells the server's UpdatePost
	// file-change processing to detach the previous generation's attachments
	// even if the new run creates none; nil could be treated as "no change".
	post.FileIds = []string{}

	runResult, runErr := c.runToolLoop(ctx, bot.LLM(), bot.GetConfig().EffectiveMaxToolTurns(), *completionReq,
		c.shouldAutoExecuteTool(llmContext, isDM),
		conv.ID,
		func(turns []toolrunner.ToolTurn) bool { return isDM || c.allToolsAutoRunEverywhere(turns, llmContext) },
		c.toolsDisabledLLMOptions(bot, toolsDisabled), "Failed to write tool turns on regen")

	if runErr != nil {
		return nil, fmt.Errorf("tool runner failed on regen: %w", runErr)
	}

	return c.decorateStreamWithCreatedFiles(ctx, bot, runResult.Stream, post, nil, llmContext, llmContext), nil
}
