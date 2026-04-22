// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/conversation"
	"github.com/mattermost/mattermost-plugin-agents/llm"
	"github.com/mattermost/mattermost-plugin-agents/mcp"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost-plugin-agents/subtitles"
	"github.com/mattermost/mattermost-plugin-agents/threads"
	"github.com/mattermost/mattermost-plugin-agents/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	ReferencedRecordingFileID  = "referenced_recording_file_id"
	ReferencedTranscriptPostID = "referenced_transcript_post_id"
)

// HandleRegenerate handles post regeneration requests
func (c *Conversations) HandleRegenerate(userID string, post *model.Post, channel *model.Channel) error {
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
		// Compatibility bridge for meeting summarization posts produced
		// without a conversation entity. Remove once meeting flows migrate.
		requester, _ := post.GetProp(streaming.LLMRequesterUserIDProp).(string)
		if requester == "" {
			return errors.New("post missing conversation_id for ownership check")
		}
		if requester != userID {
			return errors.New("only the original poster can regenerate")
		}
	} else {
		conv, err := c.convService.GetConversation(convIDProp)
		if err != nil {
			return fmt.Errorf("failed to get conversation for ownership check: %w", err)
		}
		if conv.UserID != userID {
			return errors.New("only the original poster can regenerate")
		}
	}

	if post.GetProp(streaming.NoRegen) != nil {
		return errors.New("tagged no regen")
	}

	user, err := c.mmClient.GetUser(userID)
	if err != nil {
		return fmt.Errorf("unable to get user to regen post: %w", err)
	}

	ctx, err := c.streamingService.GetStreamingContext(context.Background(), post.Id)
	if err != nil {
		return fmt.Errorf("unable to get post streaming context: %w", err)
	}
	defer c.streamingService.FinishStreaming(post.Id)

	threadIDProp := post.GetProp(ThreadIDProp)
	analysisTypeProp := post.GetProp(AnalysisTypeProp)
	referenceRecordingFileIDProp := post.GetProp(ReferencedRecordingFileID)
	referencedTranscriptPostProp := post.GetProp(ReferencedTranscriptPostID)
	var result *llm.TextStreamResult
	switch {
	case threadIDProp != nil:
		threadID := threadIDProp.(string)
		analysisType := analysisTypeProp.(string)
		threadPost, getPostErr := c.mmClient.GetPost(threadID)
		if getPostErr != nil {
			return fmt.Errorf("could not get thread post on regen: %w", getPostErr)
		}

		if !c.mmClient.HasPermissionToChannel(userID, threadPost.ChannelId, model.PermissionReadChannel) {
			return errors.New("user doesn't have permission to read channel original thread in in")
		}

		llmContext := c.contextBuilder.BuildLLMContextUserRequest(
			bot,
			user,
			channel,
			c.contextBuilder.WithLLMContextNoTools(),
		)

		analyzer := threads.New(bot.LLM(), c.prompts, c.mmClient, c.convService)
		var analyzeResult *threads.AnalyzeResult
		switch analysisType {
		case "summarize_thread":
			analyzeResult, err = analyzer.Summarize(threadID, llmContext, bot.GetMMBot().UserId, userID)
		case "action_items":
			analyzeResult, err = analyzer.FindActionItems(threadID, llmContext, bot.GetMMBot().UserId, userID)
		case "open_questions":
			analyzeResult, err = analyzer.FindOpenQuestions(threadID, llmContext, bot.GetMMBot().UserId, userID)
		default:
			return fmt.Errorf("invalid analysis type: %s", analysisType)
		}
		if err != nil {
			return fmt.Errorf("could not analyze thread on regen: %w", err)
		}
		result = analyzeResult.Stream

	case referenceRecordingFileIDProp != nil:
		post.Message = ""
		referencedRecordingFileID := referenceRecordingFileIDProp.(string)

		fileInfo, getErr := c.mmClient.GetFileInfo(referencedRecordingFileID)
		if getErr != nil {
			return fmt.Errorf("could not get transcription file on regen: %w", getErr)
		}

		reader, getErr := c.mmClient.GetFile(post.FileIds[0])
		if getErr != nil {
			return fmt.Errorf("could not get transcription file on regen: %w", getErr)
		}
		transcription, parseErr := subtitles.NewSubtitlesFromVTT(reader)
		if parseErr != nil {
			return fmt.Errorf("could not parse transcription file on regen: %w", parseErr)
		}

		if transcription.IsEmpty() {
			return errors.New("transcription is empty on regen")
		}

		originalFileChannel, channelErr := c.mmClient.GetChannel(fileInfo.ChannelId)
		if channelErr != nil {
			return fmt.Errorf("could not get channel of original recording on regen: %w", channelErr)
		}

		context := c.contextBuilder.BuildLLMContextUserRequest(
			bot,
			user,
			originalFileChannel,
			c.contextBuilder.WithLLMContextNoTools(),
		)
		var summaryErr error
		result, summaryErr = c.meetingsService.SummarizeTranscription(bot, transcription, context)
		if summaryErr != nil {
			return fmt.Errorf("could not summarize transcription on regen: %w", summaryErr)
		}
	case referencedTranscriptPostProp != nil:
		post.Message = ""
		referencedTranscriptionPostID := referencedTranscriptPostProp.(string)
		referencedTranscriptionPost, postErr := c.mmClient.GetPost(referencedTranscriptionPostID)
		if postErr != nil {
			return fmt.Errorf("could not get transcription post on regen: %w", postErr)
		}

		transcriptionFileID, fileIDErr := c.meetingsService.GetCaptionsFileIDFromProps(referencedTranscriptionPost)
		if fileIDErr != nil {
			return fmt.Errorf("unable to get transcription file id: %w", fileIDErr)
		}
		transcriptionFileReader, fileErr := c.mmClient.GetFile(transcriptionFileID)
		if fileErr != nil {
			return fmt.Errorf("unable to read calls file: %w", fileErr)
		}

		transcription, parseErr := subtitles.NewSubtitlesFromVTT(transcriptionFileReader)
		if parseErr != nil {
			return fmt.Errorf("unable to parse transcription file: %w", parseErr)
		}

		context := c.contextBuilder.BuildLLMContextUserRequest(
			bot,
			user,
			channel,
			c.contextBuilder.WithLLMContextNoTools(),
		)
		var summaryErr error
		result, summaryErr = c.meetingsService.SummarizeTranscription(bot, transcription, context)
		if summaryErr != nil {
			return fmt.Errorf("unable to summarize transcription: %w", summaryErr)
		}

	default:
		post.Message = ""

		respondingToPostID, ok := post.GetProp(streaming.RespondingToProp).(string)
		if !ok {
			return errors.New("post missing responding to prop")
		}

		// Use the conversation entity path for regeneration.
		if c.convService != nil {
			regenResult, regenErr := c.regenerateViaConversation(bot, user, channel, post, respondingToPostID)
			if regenErr != nil {
				return fmt.Errorf("could not regenerate via conversation: %w", regenErr)
			}
			result = regenResult
		} else {
			return errors.New("conversation service not configured for regeneration")
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

// regenerateViaConversation rebuilds the completion request from the conversation entity
// and runs the ToolRunner to produce a new response stream.
func (c *Conversations) regenerateViaConversation(
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

	contextOpts := []llm.ContextOption{
		c.contextBuilder.WithLLMContextDefaultTools(bot),
	}
	llmContext := c.contextBuilder.BuildLLMContextUserRequest(bot, user, channel, contextOpts...)

	// Apply user-disabled-provider filtering for DM/group channels only.
	if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
		prefs, prefsErr := mcp.LoadUserPreferences(c.mmClient, user.Id)
		if prefsErr != nil {
			c.mmClient.LogWarn("Failed to load user tool preferences on regen, proceeding without filtering", "error", prefsErr.Error(), "userID", user.Id)
		} else if len(prefs.DisabledServers) > 0 && llmContext.Tools != nil {
			llmContext.Tools.RemoveToolsByServerOrigin(prefs.DisabledServers)
		}
	}

	isDM := mmapi.IsDMWith(bot.GetMMBot().UserId, channel)
	toolsDisabled := !isDM
	if !isDM && c.configProvider != nil && c.configProvider.EnableChannelMentionToolCalling() {
		toolsDisabled = false
	}
	if llmContext != nil {
		if toolsDisabled && llmContext.Tools != nil {
			llmContext.DisabledToolsInfo = llmContext.Tools.GetToolsInfo()
		} else {
			llmContext.DisabledToolsInfo = nil
		}
	}

	// BuildCompletionRequest redacts unshared tool output by default.
	// DMs opt in to the full content because their follow-up stream is
	// scoped to the requester; DM tool_results are always shared=true so
	// nothing would actually be redacted either way, this just documents
	// intent.
	completionReq, buildErr := c.convService.BuildCompletionRequest(conv, llmContext, conversation.BuildOptions{
		ExcludeAfterPostID:       post.Id,
		AllowUnsharedToolContent: isDM,
	})
	if buildErr != nil {
		return nil, fmt.Errorf("failed to build completion request for regen: %w", buildErr)
	}

	var opts []llm.LanguageModelOption
	if toolsDisabled {
		opts = append(opts, llm.WithToolsDisabled())
		if c.configProvider != nil && c.configProvider.AllowNativeWebSearchInChannels() && bot.HasNativeWebSearchEnabled() {
			opts = append(opts, llm.WithNativeWebSearchAllowed())
		}
	}

	runner := toolrunner.New(bot.LLM())
	runResult, runErr := runner.Run(*completionReq, c.shouldAutoExecuteTool(llmContext, isDM), func(turns []toolrunner.ToolTurn) {
		shared := isDM || c.allToolsAutoRunEverywhere(turns, llmContext)
		if writeErr := c.convService.WriteToolTurns(conv.ID, turns, shared); writeErr != nil {
			c.mmClient.LogError("Failed to write tool turns on regen", "error", writeErr)
		}
	}, opts...)

	if runErr != nil {
		return nil, fmt.Errorf("tool runner failed on regen: %w", runErr)
	}

	return runResult.Stream, nil
}
