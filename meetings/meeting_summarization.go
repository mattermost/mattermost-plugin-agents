// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	sq "github.com/Masterminds/squirrel"

	"github.com/mattermost/mattermost-plugin-ai/bots"
	"github.com/mattermost/mattermost-plugin-ai/chunking"
	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mmapi"
	"github.com/mattermost/mattermost-plugin-ai/prompts"
	"github.com/mattermost/mattermost-plugin-ai/streaming"
	"github.com/mattermost/mattermost-plugin-ai/subtitles"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	ContextTokenMargin = 1000
	WhisperAPILimit    = 25 * 1000 * 1000 // 25 MB

)

func GetCaptionsFileIDFromProps(post *model.Post) (fileID string, err error) {
	if post == nil {
		return "", errors.New("post is nil")
	}

	defer func() {
		if r := recover(); r != nil {
			err = errors.New("unable to parse captions on post")
		}
	}()

	captions, ok := post.GetProp("captions").([]interface{})
	if !ok || len(captions) == 0 {
		return "", errors.New("no captions on post")
	}

	// Calls will only ever have one for now.
	return captions[0].(map[string]interface{})["file_id"].(string), nil
}

// GetCaptionsFileIDFromProps is a wrapper method to make the function available via the Service
func (s *Service) GetCaptionsFileIDFromProps(post *model.Post) (fileID string, err error) {
	return GetCaptionsFileIDFromProps(post)
}

func (s *Service) createTranscription(recordingFileID string) (*subtitles.Subtitles, error) {
	if s.ffmpegPath == "" {
		return nil, errors.New("ffmpeg not installed")
	}

	recordingFileInfo, err := s.pluginAPI.File.GetInfo(recordingFileID)
	if err != nil {
		return nil, fmt.Errorf("unable to get calls file info: %w", err)
	}

	fileReader, err := s.pluginAPI.File.Get(recordingFileID)
	if err != nil {
		return nil, fmt.Errorf("unable to read calls file: %w", err)
	}

	var cmd *exec.Cmd
	if recordingFileInfo.Size > WhisperAPILimit {
		cmd = exec.Command(s.ffmpegPath, "-i", "pipe:0", "-ac", "1", "-map", "0:a:0", "-b:a", "32k", "-ar", "16000", "-f", "mp3", "pipe:1") //nolint:gosec
	} else {
		cmd = exec.Command(s.ffmpegPath, "-i", "pipe:0", "-f", "mp3", "pipe:1") //nolint:gosec
	}

	cmd.Stdin = fileReader

	audioReader, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("couldn't create stdout pipe: %w", err)
	}

	errorReader, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("couldn't create stderr pipe: %w", err)
	}

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("couldn't run ffmpeg: %w", err)
	}

	transcriber := s.bots.GetTranscribe()
	// Limit reader should probably error out instead of just silently failing
	transcription, err := transcriber.Transcribe(io.LimitReader(audioReader, WhisperAPILimit))
	if err != nil {
		return nil, fmt.Errorf("unable to transcribe: %w", err)
	}

	errout, err := io.ReadAll(errorReader)
	if err != nil {
		return nil, fmt.Errorf("unable to read stderr from ffmpeg: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		s.pluginAPI.Log.Debug("ffmpeg stderr: " + string(errout))
		return nil, fmt.Errorf("error while waiting for ffmpeg: %w", err)
	}

	return transcription, nil
}

func (s *Service) newCallRecordingThread(bot *bots.Bot, requestingUser *model.User, recordingPost *model.Post, channel *model.Channel, fileID string) (*model.Post, error) {
	siteURL := s.pluginAPI.Configuration.GetConfig().ServiceSettings.SiteURL
	T := i18n.LocalizerFunc(s.i18n, requestingUser.Locale)
	surePost := &model.Post{
		Message: T("agents.summarize_recording", "Sure, I will summarize this recording: %s/_redirect/pl/%s\n", *siteURL, recordingPost.Id),
	}
	surePost.AddProp(streaming.NoRegen, "true")
	if err := s.botDMNonResponse(bot.GetMMBot().UserId, requestingUser.Id, surePost); err != nil {
		return nil, err
	}

	if err := s.summarizeCallRecording(bot, surePost.Id, requestingUser, fileID, channel); err != nil {
		return nil, err
	}

	return surePost, nil
}

func (s *Service) newCallTranscriptionSummaryThread(bot *bots.Bot, requestingUser *model.User, transcriptionPost *model.Post, channel *model.Channel) (*model.Post, error) {
	if len(transcriptionPost.FileIds) != 1 {
		return nil, errors.New("unexpected number of files in calls post")
	}

	siteURL := s.pluginAPI.Configuration.GetConfig().ServiceSettings.SiteURL
	T := i18n.LocalizerFunc(s.i18n, requestingUser.Locale)
	surePost := &model.Post{
		Message: T("agents.summarize_transcription", "Sure, I will summarize this transcription: %s/_redirect/pl/%s\n", *siteURL, transcriptionPost.Id),
	}
	surePost.AddProp(streaming.NoRegen, "true")
	surePost.AddProp(ReferencedTranscriptPostID, transcriptionPost.Id)
	if err := s.botDMNonResponse(bot.GetMMBot().UserId, requestingUser.Id, surePost); err != nil {
		return nil, err
	}

	go func() (reterr error) {
		// Update to an error if we return one.
		defer func() {
			if reterr != nil {
				surePost.Message = T("agents.summairize_subscription_error", "Sorry! Something went wrong. Check the server logs for details.")
				if err := s.pluginAPI.Post.UpdatePost(surePost); err != nil {
					s.pluginAPI.Log.Error("Failed to update post in error handling newCallTranscriptionSummaryThread", "error", err)
				}
				s.pluginAPI.Log.Error("Error in call recording post", "error", reterr)
			}
		}()

		transcriptionFileID, err := GetCaptionsFileIDFromProps(transcriptionPost)
		if err != nil {
			return fmt.Errorf("unable to get transcription file id: %w", err)
		}
		transcriptionFileInfo, err := s.pluginAPI.File.GetInfo(transcriptionFileID)
		if err != nil {
			return fmt.Errorf("unable to get transcription file info: %w", err)
		}
		transcriptionFilePost, err := s.pluginAPI.Post.GetPost(transcriptionFileInfo.PostId)
		if err != nil {
			return fmt.Errorf("unable to get transcription file post: %w", err)
		}
		if transcriptionFilePost.ChannelId != channel.Id {
			return errors.New("strange configuration of calls transcription file")
		}
		transcriptionFileReader, err := s.pluginAPI.File.Get(transcriptionFileID)
		if err != nil {
			return fmt.Errorf("unable to read calls file: %w", err)
		}

		var text *subtitles.Subtitles
		if transcriptionFilePost.Type == "custom_zoom_chat" {
			text, err = subtitles.NewSubtitlesFromZoomChat(transcriptionFileReader)
			if err != nil {
				return fmt.Errorf("unable to parse transcription file: %w", err)
			}
		} else {
			text, err = subtitles.NewSubtitlesFromVTT(transcriptionFileReader)
			if err != nil {
				return fmt.Errorf("unable to parse transcription file: %w", err)
			}
		}

		requestContext := s.contextBuilder.BuildLLMContextUserRequest(
			bot,
			requestingUser,
			channel,
			s.contextBuilder.WithLLMContextDefaultTools(bot, mmapi.IsDMWith(bot.GetMMBot().UserId, channel)),
		)
		summaryStream, err := s.SummarizeTranscription(bot, text, requestContext)
		if err != nil {
			return fmt.Errorf("unable to summarize transcription: %w", err)
		}

		summaryPost := &model.Post{
			RootId:    surePost.Id,
			ChannelId: surePost.ChannelId,
			Message:   "",
		}
		summaryPost.AddProp(ReferencedTranscriptPostID, transcriptionPost.Id)
		if err := s.streamingService.StreamToNewPost(context.Background(), bot.GetMMBot().UserId, requestingUser.Id, summaryStream, summaryPost, transcriptionPost.Id); err != nil {
			return fmt.Errorf("unable to stream result to post: %w", err)
		}

		return nil
	}() //nolint:errcheck

	return surePost, nil
}

func (s *Service) summarizeCallRecording(bot *bots.Bot, rootID string, requestingUser *model.User, recordingFileID string, channel *model.Channel) error {
	T := i18n.LocalizerFunc(s.i18n, requestingUser.Locale)

	transcriptPost := &model.Post{
		RootId:  rootID,
		Message: T("agents.summarize_call_recording_processing", "Processing audio into transcription. This will take some time..."),
	}
	transcriptPost.AddProp(ReferencedRecordingFileID, recordingFileID)
	if err := s.botDMNonResponse(bot.GetMMBot().UserId, requestingUser.Id, transcriptPost); err != nil {
		return err
	}

	go func() (reterr error) {
		// Update to an error if we return one.
		defer func() {
			if reterr != nil {
				transcriptPost.Message = T("agents.summarize_call_recording_processing_error", "Sorry! Something went wrong. Check the server logs for details.")
				if err := s.pluginAPI.Post.UpdatePost(transcriptPost); err != nil {
					s.pluginAPI.Log.Error("Failed to update post in error handling handleCallRecordingPost", "error", err)
				}
				s.pluginAPI.Log.Error("Error in call recording post", "error", reterr)
			}
		}()

		transcription, err := s.createTranscription(recordingFileID)
		if err != nil {
			return fmt.Errorf("failed to create transcription: %w", err)
		}

		transcriptFileInfo, err := s.pluginAPI.File.Upload(strings.NewReader(transcription.FormatVTT()), "transcript.txt", channel.Id)
		if err != nil {
			return fmt.Errorf("unable to upload transcript: %w", err)
		}

		llmContext := s.contextBuilder.BuildLLMContextUserRequest(
			bot,
			requestingUser,
			channel,
			s.contextBuilder.WithLLMContextDefaultTools(bot, channel.Type == model.ChannelTypeDirect),
		)
		summaryStream, err := s.SummarizeTranscription(bot, transcription, llmContext)
		if err != nil {
			return fmt.Errorf("unable to summarize transcription: %w", err)
		}

		if err = s.updatePostWithFile(transcriptPost, transcriptFileInfo); err != nil {
			return fmt.Errorf("unable to update transcript post: %w", err)
		}

		ctx, err := s.streamingService.GetStreamingContext(context.Background(), transcriptPost.Id)
		if err != nil {
			return fmt.Errorf("unable to get post streaming context: %w", err)
		}
		defer s.streamingService.FinishStreaming(transcriptPost.Id)

		s.streamingService.StreamToPost(ctx, summaryStream, transcriptPost, requestingUser.Locale)

		return nil
	}() //nolint:errcheck

	return nil
}

func (s *Service) SummarizeTranscription(bot *bots.Bot, transcription *subtitles.Subtitles, context *llm.Context) (*llm.TextStreamResult, error) {
	llmFormattedTranscription := transcription.FormatForLLM()
	tokens := bot.LLM().CountTokens(llmFormattedTranscription)
	tokenLimitWithMargin := int(float64(bot.LLM().InputTokenLimit())*0.75) - ContextTokenMargin
	if tokenLimitWithMargin < 0 {
		tokenLimitWithMargin = ContextTokenMargin / 2
	}
	isChunked := false
	if tokens > tokenLimitWithMargin {
		s.pluginAPI.Log.Debug("Transcription too long, summarizing in chunks.", "tokens", tokens, "limit", tokenLimitWithMargin)
		chunks := chunking.SplitPlaintextOnSentences(llmFormattedTranscription, tokenLimitWithMargin*4)
		summarizedChunks := make([]string, 0, len(chunks))
		s.pluginAPI.Log.Debug("Split into chunks", "chunks", len(chunks))
		for _, chunk := range chunks {
			systemPrompt, err := s.prompts.Format(prompts.PromptSummarizeChunkSystem, context)
			if err != nil {
				return nil, fmt.Errorf("unable to get summarize chunk prompt: %w", err)
			}
			request := llm.CompletionRequest{
				Posts: []llm.Post{
					{
						Role:    llm.PostRoleSystem,
						Message: systemPrompt,
					},
					{
						Role:    llm.PostRoleUser,
						Message: chunk,
					},
				},
				Context: context,
			}

			summarizedChunk, err := bot.LLM().ChatCompletionNoStream(request)
			if err != nil {
				return nil, fmt.Errorf("unable to get summarized chunk: %w", err)
			}

			summarizedChunks = append(summarizedChunks, summarizedChunk)
		}

		llmFormattedTranscription = strings.Join(summarizedChunks, "\n\n")
		isChunked = true
		s.pluginAPI.Log.Debug("Completed chunk summarization", "chunks", len(summarizedChunks), "tokens", bot.LLM().CountTokens(llmFormattedTranscription))
	}

	context.Parameters = map[string]any{"IsChunked": fmt.Sprintf("%t", isChunked)}
	systemPrompt, err := s.prompts.Format(prompts.PromptMeetingSummarySystem, context)
	if err != nil {
		return nil, fmt.Errorf("unable to get meeting summary prompt: %w", err)
	}

	completionRequest := llm.CompletionRequest{
		Posts: []llm.Post{
			{
				Role:    llm.PostRoleSystem,
				Message: systemPrompt,
			},
			{
				Role:    llm.PostRoleUser,
				Message: llmFormattedTranscription,
			},
		},
		Context: context,
	}

	summaryStream, err := bot.LLM().ChatCompletion(completionRequest, llm.WithToolsDisabled())
	if err != nil {
		return nil, fmt.Errorf("unable to get meeting summary: %w", err)
	}

	return summaryStream, nil
}

func (s *Service) updatePostWithFile(post *model.Post, fileinfo *model.FileInfo) error {
	if _, err := s.db.ExecBuilder(s.db.Builder().
		Update("FileInfo").
		Set("PostId", post.Id).
		Set("ChannelId", post.ChannelId).
		Where(sq.And{
			sq.Eq{"Id": fileinfo.Id},
			sq.Eq{"PostId": ""},
		})); err != nil {
		return fmt.Errorf("unable to update file info: %w", err)
	}

	post.FileIds = []string{fileinfo.Id}
	post.Message = ""
	if err := s.pluginAPI.Post.UpdatePost(post); err != nil {
		return fmt.Errorf("unable to update post: %w", err)
	}

	return nil
}

func (s *Service) botDMNonResponse(botid string, userID string, post *model.Post) error {
	streaming.ModifyPostForBot(botid, userID, post, "")

	if err := s.pluginAPI.Post.DM(botid, userID, post); err != nil {
		return fmt.Errorf("failed to post DM: %w", err)
	}

	return nil
}
