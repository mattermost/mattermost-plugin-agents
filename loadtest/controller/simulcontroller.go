// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
	"github.com/blang/semver"
	ltcontrol "github.com/mattermost/mattermost-load-test-ng/loadtest/control"
	ltplugins "github.com/mattermost/mattermost-load-test-ng/loadtest/plugins"
	ltuser "github.com/mattermost/mattermost-load-test-ng/loadtest/user"
)

// PluginID is the Mattermost plugin manifest ID for Agents (load-test-ng EnabledPlugins key).
const PluginID = "mattermost-ai"

var _ ltplugins.SimulController = (*SimulController)(nil)

// SimulController implements load-test-ng's SimulController for the Agents plugin.
type SimulController struct {
	store     *PluginStore
	config    Config
	configErr error
}

// NewSimulController builds a controller, reading config from the environment/file path.
// It never panics on config errors; they are surfaced via action responses.
func NewSimulController() *SimulController {
	cfg, err := ReadConfigFromEnv()
	return &SimulController{
		store:     NewPluginStore(),
		config:    cfg,
		configErr: err,
	}
}

// PluginId returns the manifest ID expected by mattermost-load-test-ng.
//
//revive:disable-next-line:var-naming - PluginId matches the load-test-ng SimulController interface.
func (c *SimulController) PluginId() string {
	return PluginID
}

// MinServerVersion matches mattermost-load-test-ng's simulated controller gate (control.MinSupportedVersion).
func (c *SimulController) MinServerVersion() semver.Version {
	return semver.MustParse("7.8.0")
}

// ClearUserData resets mutex-protected plugin state for all simulated users.
func (c *SimulController) ClearUserData() {
	c.store.Clear()
}

// Actions returns low-frequency mention and DM triggers that create real posts.
func (c *SimulController) Actions() []ltplugins.PluginAction {
	cfg := c.config
	if c.configErr != nil {
		defaultCfg, err := DefaultConfig()
		if err == nil {
			cfg = defaultCfg
		} else {
			cfg = Config{
				TriggerFrequencyChannelMention: 0.001,
				TriggerFrequencyDM:             0.001,
				TriggerMode:                    TriggerModeBoth,
			}
		}
	}

	wrap := func(run func(ltuser.User) ltcontrol.UserActionResponse) ltcontrol.UserAction {
		return func(u ltuser.User) ltcontrol.UserActionResponse {
			if c.configErr != nil {
				return ltcontrol.UserActionResponse{Err: ltcontrol.NewUserError(c.configErr)}
			}
			return run(u)
		}
	}

	var out []ltplugins.PluginAction
	mode := cfg.TriggerMode

	if (mode == TriggerModeBoth || mode == TriggerModeChannelMention) && cfg.TriggerFrequencyChannelMention > 0 {
		f := cfg.TriggerFrequencyChannelMention
		out = append(out, ltplugins.PluginAction{
			Name:      "AskAgentChannelMention",
			Run:       wrap(c.AskAgentChannelMention),
			Frequency: f,
		})
	}
	if (mode == TriggerModeBoth || mode == TriggerModeDM) && cfg.TriggerFrequencyDM > 0 {
		f := cfg.TriggerFrequencyDM
		out = append(out, ltplugins.PluginAction{
			Name:      "AskAgentDM",
			Run:       wrap(c.AskAgentDM),
			Frequency: f,
		})
	}
	return out
}
