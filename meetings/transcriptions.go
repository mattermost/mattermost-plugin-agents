// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"errors"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/bots"
	"github.com/mattermost/mattermost-plugin-agents/streaming"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	ReferencedRecordingFileID  = "referenced_recording_file_id"
	ReferencedTranscriptPostID = "referenced_transcript_post_id"

	TitleMeetingSummary = "Meeting Summary"
)

// HandleTranscribeFile handles file transcription requests
func (s *Service) HandleTranscribeFile(userID string, bot *bots.Bot, post *model.Post, channel *model.Channel, fileID string) (map[string]string, error) {
	user, err := s.pluginAPI.User.Get(userID)
	if err != nil {
		return nil, err
	}

	recordingFileInfo, err := s.pluginAPI.File.GetInfo(fileID)
	if err != nil {
		return nil, err
	}

	if recordingFileInfo.ChannelId != channel.Id || !slices.Contains(post.FileIds, fileID) {
		return nil, errors.New("file not attached to specified post")
	}

	createdPost, err := s.newCallRecordingThread(bot, user, post, channel, fileID)
	if err != nil {
		return nil, err
	}

	if err := s.conversations.SaveTitle(createdPost.Id, TitleMeetingSummary); err != nil {
		return nil, fmt.Errorf("failed to save title: %w", err)
	}

	return map[string]string{
		"postid":    createdPost.Id,
		"channelid": createdPost.ChannelId,
	}, nil
}

// HandleSummarizeTranscription handles transcription summarization requests
func (s *Service) HandleSummarizeTranscription(userID string, bot *bots.Bot, post *model.Post, channel *model.Channel) (map[string]string, error) {
	user, err := s.pluginAPI.User.Get(userID)
	if err != nil {
		return nil, fmt.Errorf("unable to get user: %w", err)
	}

	targetPostUser, err := s.pluginAPI.User.Get(post.UserId)
	if err != nil {
		return nil, fmt.Errorf("unable to get calls user: %w", err)
	}

	if !targetPostUser.IsBot || (targetPostUser.Username != CallsBotUsername && targetPostUser.Username != ZoomBotUsername) {
		return nil, errors.New("not a calls or zoom bot post")
	}

	createdPost, err := s.newCallTranscriptionSummaryThread(bot, user, post, channel)
	if err != nil {
		return nil, fmt.Errorf("unable to summarize transcription: %w", err)
	}

	s.conversations.SaveTitleAsync(createdPost.Id, TitleMeetingSummary)

	return map[string]string{
		"postid":    createdPost.Id,
		"channelid": createdPost.ChannelId,
	}, nil
}

// HandlePostbackSummary handles posting back a summary to the original channel
func (s *Service) HandlePostbackSummary(userID string, post *model.Post) (map[string]string, error) {
	bot := s.bots.GetBotByID(post.UserId)
	if bot == nil {
		return nil, fmt.Errorf("unable to get bot")
	}

	if post.GetProp(streaming.LLMRequesterUserID) != userID {
		return nil, errors.New("only the original requester can post back")
	}

	transcriptThreadRootPost, err := s.pluginAPI.Post.GetPost(post.RootId)
	if err != nil {
		return nil, fmt.Errorf("unable to get transcript thread root post: %w", err)
	}

	originalTranscriptPostID, ok := transcriptThreadRootPost.GetProp(ReferencedTranscriptPostID).(string)
	if !ok || originalTranscriptPostID == "" {
		return nil, errors.New("post missing reference to transcription post ID")
	}

	transcriptionPost, err := s.pluginAPI.Post.GetPost(originalTranscriptPostID)
	if err != nil {
		return nil, fmt.Errorf("unable to get transcription post: %w", err)
	}

	if !s.pluginAPI.User.HasPermissionToChannel(userID, transcriptionPost.ChannelId, model.PermissionCreatePost) {
		return nil, errors.New("user doesn't have permission to create a post in the transcript channel")
	}

	postedSummary := &model.Post{
		UserId:    bot.GetMMBot().UserId,
		ChannelId: transcriptionPost.ChannelId,
		RootId:    transcriptionPost.RootId,
		Message:   post.Message,
		Type:      "custom_llm_postback",
	}
	postedSummary.AddProp("userid", userID)
	if err := s.pluginAPI.Post.CreatePost(postedSummary); err != nil {
		return nil, fmt.Errorf("unable to post back summary: %w", err)
	}

	return map[string]string{
		"rootid":    postedSummary.RootId,
		"channelid": postedSummary.ChannelId,
	}, nil
}
