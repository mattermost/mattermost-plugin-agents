// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mattermost/mattermost-load-test-ng/defaults"
)

const (
	configEnvVar      = "MM_AGENTS_LOADTEST_CONFIG"
	defaultConfigPath = "./config/mattermost-ai-loadtest.json"
)

// TriggerMode selects which simulated actions are logically enabled by configuration.
// Action weights (TriggerFrequency*) are separate; zero frequency omits an action from Actions().
type TriggerMode string

const (
	TriggerModeBoth           TriggerMode = "both"
	TriggerModeChannelMention TriggerMode = "channel_mention"
	TriggerModeDM             TriggerMode = "dm"
)

// Config is narrow Agents-only load-test configuration (not a generic plugin framework).
//
// TriggerFrequencyChannelMention and TriggerFrequencyDM are load-test-ng relative weights,
// not global probabilities: 0.001 means one-thousandth the weight of an action with
// frequency 1.0 (e.g. core CreatePost). The simulator scales by the smallest non-zero
// frequency, so very small values remain valid.
type Config struct {
	TriggerFrequencyChannelMention float64         `json:"triggerFrequencyChannelMention" default:"0.001" validate:"range:[0,]"`
	TriggerFrequencyDM             float64         `json:"triggerFrequencyDM" default:"0.001" validate:"range:[0,]"`
	AgentUsername                  string          `json:"agentUsername" default:"ai"`
	AgentUserID                    string          `json:"agentUserID,omitempty"`
	TriggerMode                    TriggerMode     `json:"triggerMode" default:"both" validate:"oneof:{both,channel_mention,dm}"`
	PromptProfile                  string          `json:"promptProfile" default:"mixed"`
	MockProfile                    json.RawMessage `json:"mockProfile,omitempty"`
}

// DefaultConfig returns defaults after applying `default` struct tags.
func DefaultConfig() (Config, error) {
	var c Config
	if err := defaults.Set(&c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

// ReadConfig reads and validates configuration from path (required non-empty path).
func ReadConfig(path string) (Config, error) {
	var c Config
	if err := defaults.ReadFrom(path, "", &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

// ReadConfigFromEnv loads config from MM_AGENTS_LOADTEST_CONFIG when set, otherwise
// merges ./config/mattermost-ai-loadtest.json when that file exists, then validates.
func ReadConfigFromEnv() (Config, error) {
	var c Config
	path := os.Getenv(configEnvVar)
	if err := defaults.ReadFrom(path, defaultConfigPath, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

// Validate checks struct tags and Agents-specific rules.
func (c Config) Validate() error {
	if err := defaults.Validate(&c); err != nil {
		return err
	}

	needChannel := (c.TriggerMode == TriggerModeBoth || c.TriggerMode == TriggerModeChannelMention) &&
		c.TriggerFrequencyChannelMention > 0
	needDM := (c.TriggerMode == TriggerModeBoth || c.TriggerMode == TriggerModeDM) &&
		c.TriggerFrequencyDM > 0

	if needChannel && c.AgentUsername == "" && c.AgentUserID == "" {
		return fmt.Errorf("channel mention enabled but both agentUsername and agentUserID are empty")
	}
	if needDM && c.AgentUsername == "" && c.AgentUserID == "" {
		return fmt.Errorf("DM trigger enabled but both agentUserID and agentUsername are empty")
	}
	return nil
}
