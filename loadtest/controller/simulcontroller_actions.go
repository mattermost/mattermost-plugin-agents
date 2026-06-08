// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
	"fmt"
	"time"

	ltcontrol "github.com/mattermost/mattermost-load-test-ng/loadtest/control"
	"github.com/mattermost/mattermost-load-test-ng/loadtest/store"
	ltuser "github.com/mattermost/mattermost-load-test-ng/loadtest/user"
	"github.com/mattermost/mattermost/server/public/model"
)

// AskAgentChannelMention posts a public/private channel message that @-mentions the Agents bot.
func (c *SimulController) AskAgentChannelMention(u ltuser.User) ltcontrol.UserActionResponse {
	return c.askAgentChannelMention(u)
}

func (c *SimulController) askAgentChannelMention(u simulAPI) ltcontrol.UserActionResponse {
	state := c.store.Get(u.Store().Id())
	ch, ok, err := resolveChannelForMention(u.Store(), state)
	if err != nil {
		return ltcontrol.UserActionResponse{Err: ltcontrol.NewUserError(err)}
	}
	if !ok {
		return ltcontrol.UserActionResponse{Info: "skip channel mention: no suitable channel"}
	}

	target, err := c.resolveAgentTarget(u)
	if err != nil {
		return ltcontrol.UserActionResponse{Err: ltcontrol.NewUserError(err)}
	}
	if target.Username == "" {
		return ltcontrol.UserActionResponse{Err: ltcontrol.NewUserError(fmt.Errorf("agent username unresolved for channel mention"))}
	}

	n := c.store.NextPromptCounter(u.Store().Id())
	prompt := GeneratePrompt(c.config.PromptProfile, c.config.TriggerMode, n)
	message := fmt.Sprintf("@%s %s", target.Username, prompt)

	postID, err := createMattermostPost(u, ch.Id, message)
	if err != nil {
		return ltcontrol.UserActionResponse{Err: ltcontrol.NewUserError(err)}
	}
	return ltcontrol.UserActionResponse{Info: fmt.Sprintf("channel mention post id=%s channel id=%s", postID, ch.Id)}
}

// AskAgentDM sends a direct message to the Agents bot user (no @mention in the body).
func (c *SimulController) AskAgentDM(u ltuser.User) ltcontrol.UserActionResponse {
	return c.askAgentDM(u)
}

func (c *SimulController) askAgentDM(u simulAPI) ltcontrol.UserActionResponse {
	target, err := c.resolveAgentTarget(u)
	if err != nil {
		return ltcontrol.UserActionResponse{Err: ltcontrol.NewUserError(err)}
	}
	selfID := u.Store().Id()
	if target.UserID == "" {
		return ltcontrol.UserActionResponse{Err: ltcontrol.NewUserError(fmt.Errorf("agent user id unresolved for DM"))}
	}
	if target.UserID == selfID {
		return ltcontrol.UserActionResponse{Info: "skip DM: agent user id matches load-test user"}
	}

	dmChannelID, err := u.CreateDirectChannel(target.UserID)
	if err != nil {
		return ltcontrol.UserActionResponse{Err: ltcontrol.NewUserError(err)}
	}

	n := c.store.NextPromptCounter(u.Store().Id())
	prompt := GeneratePrompt(c.config.PromptProfile, c.config.TriggerMode, n)

	postID, err := createMattermostPost(u, dmChannelID, prompt)
	if err != nil {
		return ltcontrol.UserActionResponse{Err: ltcontrol.NewUserError(err)}
	}
	return ltcontrol.UserActionResponse{Info: fmt.Sprintf("DM post id=%s channel id=%s", postID, dmChannelID)}
}

func createMattermostPost(u simulAPI, channelID, message string) (string, error) {
	return u.CreatePost(&model.Post{
		ChannelId: channelID,
		Message:   message,
		CreateAt:  time.Now().UnixMilli(),
	})
}

func isEligibleMentionChannel(ch *model.Channel) bool {
	if ch == nil {
		return false
	}
	switch ch.Type {
	case model.ChannelTypeOpen, model.ChannelTypePrivate:
		return true
	default:
		return false
	}
}

// resolveChannelForMention picks a non-DM channel for @mentions. ok is false when the
// environment has no suitable channel (skip without error).
func resolveChannelForMention(st store.UserStore, state UserState) (model.Channel, bool, error) {
	if state.CurrentChannelID != "" {
		ch, err := st.Channel(state.CurrentChannelID)
		if err == nil && isEligibleMentionChannel(ch) {
			return *ch, true, nil
		}
	}

	cur, err := st.CurrentChannel()
	if err == nil && isEligibleMentionChannel(cur) {
		return *cur, true, nil
	}

	if state.CurrentTeamID != "" {
		teamChannel, randErr := st.RandomChannel(state.CurrentTeamID, store.SelectMemberOf)
		if randErr == nil && isEligibleMentionChannel(&teamChannel) {
			return teamChannel, true, nil
		}
	}

	team, err := st.RandomTeam(store.SelectMemberOf)
	if err != nil {
		return model.Channel{}, false, nil
	}
	ch, err := st.RandomChannel(team.Id, store.SelectMemberOf)
	if err != nil {
		return model.Channel{}, false, nil
	}
	if !isEligibleMentionChannel(&ch) {
		return model.Channel{}, false, nil
	}
	return ch, true, nil
}
