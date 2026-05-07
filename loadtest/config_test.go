// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package loadtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	c, err := DefaultConfig()
	require.NoError(t, err)
	assert.Equal(t, 0.001, c.TriggerFrequencyChannelMention)
	assert.Equal(t, 0.001, c.TriggerFrequencyDM)
	assert.Equal(t, "ai", c.AgentUsername)
	assert.Equal(t, TriggerModeBoth, c.TriggerMode)
	assert.Equal(t, "mixed", c.PromptProfile)
}

func TestReadConfig_JSONOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.json")
	err := os.WriteFile(p, []byte(`{
  "triggerFrequencyChannelMention": 0.01,
  "triggerFrequencyDM": 0.001,
  "agentUsername": "helper_bot",
  "triggerMode": "dm",
  "promptProfile": "short"
}`), 0o600)
	require.NoError(t, err)

	c, err := ReadConfig(p)
	require.NoError(t, err)
	assert.Equal(t, 0.01, c.TriggerFrequencyChannelMention)
	assert.Equal(t, 0.001, c.TriggerFrequencyDM)
	assert.Equal(t, "helper_bot", c.AgentUsername)
	assert.Equal(t, TriggerModeDM, c.TriggerMode)
}

func TestReadConfig_UnknownFields(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.json")
	err := os.WriteFile(p, []byte(`{"agentUsername":"x","extraField":true}`), 0o600)
	require.NoError(t, err)

	_, err = ReadConfig(p)
	require.Error(t, err)
}

func TestConfigValidate_InvalidTriggerMode(t *testing.T) {
	c := Config{
		TriggerFrequencyChannelMention: 0.001,
		TriggerFrequencyDM:             0.001,
		AgentUsername:                  "bot",
		TriggerMode:                    TriggerMode("nope"),
	}
	assert.Error(t, c.Validate())
}

func TestConfigValidate_NegativeFrequency(t *testing.T) {
	c := Config{
		TriggerFrequencyChannelMention: -0.1,
		TriggerFrequencyDM:             0.001,
		AgentUsername:                  "bot",
		TriggerMode:                    TriggerModeBoth,
	}
	assert.Error(t, c.Validate())
}

func TestConfigValidate_InvalidMockProfile(t *testing.T) {
	raw := json.RawMessage(`{"not_a_profile":true}`)
	c := Config{
		TriggerFrequencyChannelMention: 0.001,
		TriggerFrequencyDM:             0.001,
		AgentUsername:                  "bot",
		TriggerMode:                    TriggerModeBoth,
		MockProfile:                    raw,
	}
	assert.Error(t, c.Validate())
}

func TestConfigValidate_MissingAgentWhenMentionNeeded(t *testing.T) {
	c := Config{
		TriggerFrequencyChannelMention: 0.001,
		TriggerFrequencyDM:             0,
		AgentUsername:                  "",
		AgentUserID:                    "",
		TriggerMode:                    TriggerModeChannelMention,
	}
	assert.Error(t, c.Validate())
}

func TestConfigValidate_MissingAgentWhenDMNeeded(t *testing.T) {
	c := Config{
		TriggerFrequencyChannelMention: 0,
		TriggerFrequencyDM:             0.001,
		AgentUsername:                  "",
		AgentUserID:                    "",
		TriggerMode:                    TriggerModeDM,
	}
	assert.Error(t, c.Validate())
}

func TestConfigValidate_ZeroFrequenciesSkipAgentRequirement(t *testing.T) {
	c := Config{
		TriggerFrequencyChannelMention: 0,
		TriggerFrequencyDM:             0,
		AgentUsername:                  "",
		AgentUserID:                    "",
		TriggerMode:                    TriggerModeBoth,
	}
	assert.NoError(t, c.Validate())
}
