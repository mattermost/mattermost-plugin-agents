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
	"github.com/mattermost/mattermost-plugin-agents/v2/meetings"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/trace"
)

// MeetingSummaryRegenerator re-runs a meeting summary from the transcript
// file its conversation references.
type MeetingSummaryRegenerator interface {
	RegenerateSummary(ctx stdcontext.Context, bot *bots.Bot, llmContext *llm.Context, conv *store.Conversation, postID string) (*llm.TextStreamResult, error)
}

// SetMeetingSummaryRegenerator sets the service that regenerates meeting
// summaries. Meeting summaries cannot be regenerated while it is unset.
func (c *Conversations) SetMeetingSummaryRegenerator(r MeetingSummaryRegenerator) {
	c.meetingSummaries = r
}

type regeneratePath int

const (
	regenerateConversation regeneratePath = iota
	regenerateThreadAnalysis
	regenerateChannelSummary
	regenerateMeetingSummary
)

// ErrNotRegenerable is returned when regenerating a post tagged no_regen,
// such as an interval summary. The HTTP layer maps it to 400.
var ErrNotRegenerable = errors.New("this post cannot be regenerated")

// selectRegeneratePath picks how post is regenerated from server-stored state
// and props of the bot-authored post only. Follow-up replies in an
// operation's conversation regenerate as ordinary conversation turns.
func selectRegeneratePath(conv *store.Conversation, post *model.Post) (regeneratePath, error) {
	isOperationRoot := conv.RootPostID != nil && *conv.RootPostID == post.Id
	switch conv.Operation {
	case "", llm.OperationConversation, llm.OperationSearch:
		return regenerateConversation, nil
	case llm.OperationThreadAnalysis:
		if isOperationRoot {
			return regenerateThreadAnalysis, nil
		}
		return regenerateConversation, nil
	case llm.OperationChannelSummary:
		if isOperationRoot {
			return regenerateChannelSummary, nil
		}
		return regenerateConversation, nil
	case llm.OperationChannelInterval:
		if isOperationRoot {
			return 0, ErrNotRegenerable
		}
		return regenerateConversation, nil
	case llm.OperationMeetingSummary:
		// Summary posts reply under the request post, so the conversation
		// root never matches them. The prop only selects the path; the
		// transcript comes from the conversation.
		recording, _ := post.GetProp(meetings.ReferencedRecordingFileID).(string)
		transcript, _ := post.GetProp(meetings.ReferencedTranscriptPostID).(string)
		if recording != "" || transcript != "" {
			return regenerateMeetingSummary, nil
		}
		return regenerateConversation, nil
	}
	return 0, fmt.Errorf("regenerate is not supported for %q conversations", conv.Operation)
}

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
		return ErrNotRequester
	}
	// Props select the regenerate path and its sources, so they are only
	// trusted on a post the conversation's own bot authored.
	if post.UserId != conv.BotID {
		return errors.New("post was not authored by the conversation's bot")
	}

	if post.GetProp(streaming.NoRegen) != nil {
		return ErrNotRegenerable
	}

	if err = c.checkRegenerateLicense(conv.Operation); err != nil {
		return err
	}

	path, err := selectRegeneratePath(conv, post)
	if err != nil {
		return err
	}

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return fmt.Errorf("unable to get user to regen post: %w", err)
	}

	policy, err := c.resolveTurnPolicy(ctx, turnRequest{bot: bot, user: user, channel: channel, conv: conv, post: post})
	if err != nil {
		return err
	}

	ctx, err = c.streamingService.GetStreamingContext(ctx, post.Id)
	if err != nil {
		return fmt.Errorf("unable to get post streaming context: %w", err)
	}
	defer c.streamingService.FinishStreaming(post.Id)

	var result *llm.TextStreamResult
	switch path {
	case regenerateThreadAnalysis:
		result, err = c.regenerateSingleShot(ctx, bot, user, channel, conv, post)

	case regenerateMeetingSummary:
		if c.meetingSummaries == nil {
			return errors.New("meeting summary regeneration not available")
		}
		llmContext := c.contextBuilder.BuildLLMContextUserRequest(bot, user, channel, c.contextBuilder.WithLLMContextNoTools())
		result, err = c.meetingSummaries.RegenerateSummary(ctx, bot, llmContext, conv, post.Id)

	case regenerateChannelSummary:
		result, err = c.regenerateChannelAnalysis(ctx, bot, user, conv, post)

	default:
		if _, ok := post.GetProp(streaming.RespondingToProp).(string); !ok {
			return errors.New("post missing responding to prop")
		}
		result, err = c.regenerateViaConversation(ctx, policy, post)
	}
	if err != nil {
		return fmt.Errorf("could not regenerate %s post: %w", conv.Operation, err)
	}

	post.Message = ""

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

// checkRegenerateLicense gates regenerating any post of a conversation by the
// capability of the operation that started it. Thread and channel summaries
// are available at Professional and above, meeting summaries at Enterprise
// and above. Other conversations are not gated.
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

// regenerateSingleShot re-runs a tool-less thread analysis from its stored
// system prompt and user turn, scrubbing the prior response anchored to post.
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

	completionReq, err := c.convService.BuildRegenerateRequest(conv, llmContext, post.Id, conversation.BuildOptions{
		SessionID: auth.SessionIDFromContext(ctx),
	})
	if err != nil {
		return nil, err
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
	if err = c.bots.CheckUsageRestrictionsForChannel(bot, analyzedChannel); err != nil {
		return nil, err
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
func (c *Conversations) regenerateViaConversation(ctx stdcontext.Context, policy *turnPolicy, post *model.Post) (*llm.TextStreamResult, error) {
	conv := policy.conv
	if conv == nil {
		return nil, errors.New("post missing conversation_id for regeneration")
	}

	// Regen truncates the request at the originating user message, so a
	// steering note would never reach the model: cancel without one.
	c.cancelThreadWake(responsePostMarkerKey(post))

	llmContext := policy.buildContext(ctx, "Failed to load user tool preferences on regen, proceeding without filtering")
	llmContext.ResponsePostID = post.Id

	// AllowUnsharedToolContent on DMs is a no-op (DM tool_results are shared)
	// but documents intent.
	completionReq, err := c.convService.BuildRegenerateRequest(conv, llmContext, post.Id, conversation.BuildOptions{
		AllowUnsharedToolContent: policy.isDM,
		SessionID:                auth.SessionIDFromContext(ctx),
	})
	if err != nil {
		return nil, err
	}

	// Clear prior attachments so the regenerated response starts clean.
	// Safe: conversation-flow bot posts only ever carry CreateFile
	// attachments (meeting summaries regenerate through their own path).
	// An explicit empty list — not nil — tells the server's UpdatePost
	// file-change processing to detach the previous generation's attachments
	// even if the new run creates none; nil could be treated as "no change".
	post.FileIds = []string{}

	runResult, runErr := policy.runToolLoop(ctx, llmContext, conv.ID, *completionReq, "Failed to write tool turns on regen")
	if runErr != nil {
		return nil, fmt.Errorf("tool runner failed on regen: %w", runErr)
	}

	return c.decorateStreamWithCreatedFiles(ctx, policy.bot, runResult.Stream, post, nil, llmContext, llmContext), nil
}
