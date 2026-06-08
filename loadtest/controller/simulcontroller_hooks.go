// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
	"fmt"
	"strings"

	ltplugins "github.com/mattermost/mattermost-load-test-ng/loadtest/plugins"
	ltuser "github.com/mattermost/mattermost-load-test-ng/loadtest/user"
)

// HookLogin resolves and caches the configured Agents bot user for the simulated user.
func (c *SimulController) HookLogin(u simulAPI) error {
	if c.configErr != nil {
		return nil
	}

	target, err := resolveAgentTargetFromConfig(u, c.config)
	if err != nil {
		return nil
	}
	c.store.SetAgentTarget(u.Store().Id(), target)
	return nil
}

// HookSwitchTeam records the active team for channel selection heuristics.
func (c *SimulController) HookSwitchTeam(u simulAPI, teamID string) error {
	c.store.SetCurrentTeam(u.Store().Id(), teamID)
	return nil
}

// HookSwitchChannel records the active channel for mention actions.
func (c *SimulController) HookSwitchChannel(u simulAPI, channelID string) error {
	c.store.SetCurrentChannel(u.Store().Id(), channelID)
	return nil
}

// RunHook dispatches load-test-ng lifecycle hooks.
func (c *SimulController) RunHook(hookType ltplugins.HookType, u ltuser.User, payload any) error {
	switch hookType {
	case ltplugins.HookLogin:
		return c.HookLogin(u)
	case ltplugins.HookSwitchTeam:
		switch p := payload.(type) {
		case ltplugins.HookPayloadSwitchTeam:
			return c.HookSwitchTeam(u, p.TeamId)
		case *ltplugins.HookPayloadSwitchTeam:
			if p == nil {
				return fmt.Errorf("hookSwitchTeam: expected plugins.HookPayloadSwitchTeam, got %T", payload)
			}
			return c.HookSwitchTeam(u, p.TeamId)
		default:
			return fmt.Errorf("hookSwitchTeam: expected plugins.HookPayloadSwitchTeam, got %T", payload)
		}
	case ltplugins.HookSwitchChannel:
		switch p := payload.(type) {
		case ltplugins.HookPayloadSwitchChannel:
			return c.HookSwitchChannel(u, p.ChannelId)
		case *ltplugins.HookPayloadSwitchChannel:
			if p == nil {
				return fmt.Errorf("hookSwitchChannel: expected plugins.HookPayloadSwitchChannel, got %T", payload)
			}
			return c.HookSwitchChannel(u, p.ChannelId)
		default:
			return fmt.Errorf("hookSwitchChannel: expected plugins.HookPayloadSwitchChannel, got %T", payload)
		}
	default:
		return nil
	}
}

func resolveAgentTargetFromConfig(u simulAPI, cfg Config) (AgentTarget, error) {
	uid := strings.TrimSpace(cfg.AgentUserID)
	uname := strings.TrimSpace(cfg.AgentUsername)

	if uid != "" && uname != "" {
		ids, err := u.GetUsersByUsernames([]string{uname})
		if err != nil {
			return AgentTarget{}, err
		}
		if len(ids) == 0 {
			return AgentTarget{}, fmt.Errorf("no user for username %q", uname)
		}
		if ids[0] != uid {
			return AgentTarget{}, fmt.Errorf("agentUsername %q resolves to user id %s, which does not match agentUserID %s", uname, ids[0], uid)
		}
		userObj, err := u.Store().GetUser(uid)
		if err != nil {
			return AgentTarget{}, err
		}
		return AgentTarget{UserID: userObj.Id, Username: userObj.Username}, nil
	}

	if uname != "" {
		ids, err := u.GetUsersByUsernames([]string{uname})
		if err != nil {
			return AgentTarget{}, err
		}
		if len(ids) == 0 {
			return AgentTarget{}, fmt.Errorf("no user for username %q", uname)
		}
		userObj, err := u.Store().GetUser(ids[0])
		if err != nil {
			return AgentTarget{}, err
		}
		return AgentTarget{UserID: userObj.Id, Username: userObj.Username}, nil
	}

	if uid != "" {
		if _, err := u.GetUsersByIds([]string{uid}, 0); err != nil {
			return AgentTarget{}, err
		}
		userObj, err := u.Store().GetUser(uid)
		if err != nil {
			return AgentTarget{}, err
		}
		return AgentTarget{UserID: userObj.Id, Username: userObj.Username}, nil
	}

	return AgentTarget{}, fmt.Errorf("agent username and user id are empty")
}

func (c *SimulController) resolveAgentTarget(u simulAPI) (AgentTarget, error) {
	state := c.store.Get(u.Store().Id())
	if state.AgentUserID != "" && state.AgentUsername != "" {
		return AgentTarget{UserID: state.AgentUserID, Username: state.AgentUsername}, nil
	}
	target, err := resolveAgentTargetFromConfig(u, c.config)
	if err != nil {
		return AgentTarget{}, err
	}
	c.store.SetAgentTarget(u.Store().Id(), target)
	return target, nil
}
