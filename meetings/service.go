// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/llmcontext"
	"github.com/mattermost/mattermost-plugin-agents/v2/metrics"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const CallsRecordingPostType = "custom_calls_recording"

// MeetingBotUsernames is the set of bot usernames whose posts can be summarized as meeting transcriptions.
var MeetingBotUsernames = []string{"calls", "zoom", "google-meet"}

// Service handles meeting summarization and transcription functionality
type Service struct {
	pluginAPI        *pluginapi.Client
	streamingService streaming.Service
	prompts          *llm.Prompts
	bots             *bots.MMBots
	i18n             *i18n.Bundle
	metricsService   metrics.Metrics
	contextBuilder   *llmcontext.Builder
	conversations    *conversations.Conversations

	ffmpegPath string
}

// NewService creates a new meetings service
func NewService(
	pluginAPI *pluginapi.Client,
	streamingService streaming.Service,
	prompts *llm.Prompts,
	bots *bots.MMBots,
	i18n *i18n.Bundle,
	metricsService metrics.Metrics,
	contextBuilder *llmcontext.Builder,
	conversations *conversations.Conversations,
) *Service {
	service := &Service{
		pluginAPI:        pluginAPI,
		streamingService: streamingService,
		prompts:          prompts,
		bots:             bots,
		i18n:             i18n,
		metricsService:   metricsService,
		contextBuilder:   contextBuilder,
		conversations:    conversations,
	}

	service.ffmpegPath = resolveFFMPEGPath()
	if service.ffmpegPath == "" {
		service.pluginAPI.Log.Error("ffmpeg not installed, transcriptions will be disabled.")
	}

	return service
}
