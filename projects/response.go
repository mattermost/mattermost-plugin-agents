// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package projects

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
)

// Created-from values in API responses.
const (
	ResponseCreatedFromModal = "channel_modal"
	ResponseCreatedFromDM    = "dm"
	ResponseCreatedFromAPI   = "api"
)

// Response is a project as the webapp sees it.
type Response struct {
	store.Project
	AgentDisplayName string `json:"agent_display_name"`
	AgentUsername    string `json:"agent_username"`
	// SetupCompleted reports whether the agent finished its setup thread.
	SetupCompleted bool `json:"setup_completed"`
}

// ProjectResponse returns project with its agent's display fields.
func (s *Service) ProjectResponse(project *store.Project) Response {
	r := Response{Project: *project}
	switch project.CreatedFrom {
	case store.ProjectCreatedFromModal:
		r.CreatedFrom = ResponseCreatedFromModal
	case store.ProjectCreatedFromDM:
		r.CreatedFrom = ResponseCreatedFromDM
	default:
		r.CreatedFrom = ResponseCreatedFromAPI
	}
	if bot := s.deps.Bots.GetBotByID(project.AgentBotUserID); bot != nil {
		cfg := bot.GetConfig()
		r.AgentDisplayName = cfg.DisplayName
		r.AgentUsername = cfg.Name
		r.SetupCompleted = cfg.SetupCompletedAt != 0
	}
	return r
}

// Available reports whether project channels can be used: the setting is
// on and a default LLM service is chosen.
func (s *Service) Available() bool {
	return s.enabled() && s.deps.Config.ProjectDefaultServiceID() != ""
}

// LiveProject returns the running project bot leads, failing closed exactly
// like a project turn does.
func (s *Service) LiveProject(bot *bots.Bot) (*store.Project, error) {
	return s.liveProjectFor(bot)
}
