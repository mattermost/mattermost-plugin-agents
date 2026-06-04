// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
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

func TestReadConfig(t *testing.T) {
	tests := []struct {
		name      string
		contents  string
		assertion func(t *testing.T, c Config, err error)
	}{
		{
			name: "JSON override",
			contents: `{
  "triggerFrequencyChannelMention": 0.01,
  "triggerFrequencyDM": 0.001,
  "agentUsername": "helper_bot",
  "triggerMode": "dm",
  "promptProfile": "short"
}`,
			assertion: func(t *testing.T, c Config, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.Equal(t, 0.01, c.TriggerFrequencyChannelMention)
				assert.Equal(t, 0.001, c.TriggerFrequencyDM)
				assert.Equal(t, "helper_bot", c.AgentUsername)
				assert.Equal(t, TriggerModeDM, c.TriggerMode)
			},
		},
		{
			name:     "unknown fields",
			contents: `{"agentUsername":"x","extraField":true}`,
			assertion: func(t *testing.T, _ Config, err error) {
				t.Helper()
				require.Error(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "cfg.json")
			err := os.WriteFile(p, []byte(tt.contents), 0o600)
			require.NoError(t, err)

			c, err := ReadConfig(p)
			tt.assertion(t, c, err)
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "invalid trigger mode",
			config: Config{
				TriggerFrequencyChannelMention: 0.001,
				TriggerFrequencyDM:             0.001,
				AgentUsername:                  "bot",
				TriggerMode:                    TriggerMode("nope"),
			},
			wantErr: true,
		},
		{
			name: "negative frequency",
			config: Config{
				TriggerFrequencyChannelMention: -0.1,
				TriggerFrequencyDM:             0.001,
				AgentUsername:                  "bot",
				TriggerMode:                    TriggerModeBoth,
			},
			wantErr: true,
		},
		{
			name: "missing agent when mention needed",
			config: Config{
				TriggerFrequencyChannelMention: 0.001,
				TriggerFrequencyDM:             0,
				AgentUsername:                  "",
				AgentUserID:                    "",
				TriggerMode:                    TriggerModeChannelMention,
			},
			wantErr: true,
		},
		{
			name: "missing agent when DM needed",
			config: Config{
				TriggerFrequencyChannelMention: 0,
				TriggerFrequencyDM:             0.001,
				AgentUsername:                  "",
				AgentUserID:                    "",
				TriggerMode:                    TriggerModeDM,
			},
			wantErr: true,
		},
		{
			name: "zero frequencies skip agent requirement",
			config: Config{
				TriggerFrequencyChannelMention: 0,
				TriggerFrequencyDM:             0,
				AgentUsername:                  "",
				AgentUserID:                    "",
				TriggerMode:                    TriggerModeBoth,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
