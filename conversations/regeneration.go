// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost-plugin-ai/subtitles"
	"github.com/mattermost/mattermost-plugin-ai/threads"
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

	if post.GetProp(streaming.LLMRequesterUserID) != userID {
		return errors.New("only the original poster can regenerate")
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
	post.DelProp(streaming.ToolCallProp)
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
			c.contextBuilder.WithLLMContextDefaultTools(bot, mmapi.IsDMWith(bot.GetMMBot().UserId, channel)),
		)

		analyzer := threads.New(bot.LLM(), c.prompts, c.mmClient)
		switch analysisType {
		case "summarize_thread":
			result, err = analyzer.Summarize(threadID, llmContext)
		case "action_items":
			result, err = analyzer.FindActionItems(threadID, llmContext)
		case "open_questions":
			result, err = analyzer.FindOpenQuestions(threadID, llmContext)
		default:
			return fmt.Errorf("invalid analysis type: %s", analysisType)
		}
		if err != nil {
			return fmt.Errorf("could not analyze thread on regen: %w", err)
		}

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
			c.contextBuilder.WithLLMContextDefaultTools(bot, originalFileChannel.Type == model.ChannelTypeDirect),
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
			c.contextBuilder.WithLLMContextDefaultTools(bot, mmapi.IsDMWith(bot.GetMMBot().UserId, channel)),
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
		respondingToPost, getErr := c.mmClient.GetPost(respondingToPostID)
		if getErr != nil {
			return fmt.Errorf("could not get post being responded to: %w", getErr)
		}

		// Create a context with the tool call callback already set
		contextWithCallback := c.contextBuilder.BuildLLMContextUserRequest(
			bot,
			user,
			channel,
			c.contextBuilder.WithLLMContextDefaultTools(bot, mmapi.IsDMWith(bot.GetMMBot().UserId, channel)),
		)

		// Process the user request with the context that has the callback
		var processErr error
		result, processErr = c.ProcessUserRequestWithContext(bot, user, channel, respondingToPost, contextWithCallback)
		if processErr != nil {
			return fmt.Errorf("could not continue conversation on regen: %w", processErr)
		}
	}

	if mmapi.IsDMWith(bot.GetMMBot().UserId, channel) {
		if channel.Name == bot.GetMMBot().UserId+"__"+user.Id || channel.Name == user.Id+"__"+bot.GetMMBot().UserId {
			c.streamingService.StreamToPost(ctx, result, post, user.Locale)
			return nil
		}
	}

	config := c.mmClient.GetConfig()
	c.streamingService.StreamToPost(ctx, result, post, *config.LocalizationSettings.DefaultServerLocale)

	return nil
}
