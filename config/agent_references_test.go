// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	agentAID = "agentabotuserid12345678901"
	agentBID = "agentbbotuserid12345678901"
)

func TestAgentReferenceFieldsRoundTrip(t *testing.T) {
	cfg := &Config{
		DefaultBotUserID:    agentAID,
		TranscriptBotUserID: agentBID,
		DefaultBotName:      "legacy-default",
		TranscriptGenerator: "legacy-transcript",
	}

	clone := cfg.Clone()
	assert.Equal(t, cfg.DefaultBotUserID, clone.DefaultBotUserID)
	assert.Equal(t, cfg.TranscriptBotUserID, clone.TranscriptBotUserID)
	assert.Equal(t, cfg.DefaultBotName, clone.DefaultBotName)
	assert.Equal(t, cfg.TranscriptGenerator, clone.TranscriptGenerator)

	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	assert.Equal(t, agentAID, fields["defaultBotUserID"])
	assert.Equal(t, agentBID, fields["transcriptBotUserID"])
	assert.Equal(t, "legacy-default", fields["defaultBotName"])
	assert.Equal(t, "legacy-transcript", fields["transcriptBackend"])

	container := &Container{}
	container.Update(cfg)
	assert.Equal(t, agentAID, container.GetDefaultBotUserID())
	assert.Equal(t, agentBID, container.GetTranscriptBotUserID())
	assert.Equal(t, "legacy-default", container.GetDefaultBotName())
	assert.Equal(t, "legacy-transcript", container.GetTranscriptGenerator())

	empty := &Container{}
	assert.Empty(t, empty.GetDefaultBotUserID())
	assert.Empty(t, empty.GetTranscriptBotUserID())
}

func TestValidateAgentReferences(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "no references", cfg: Config{}},
		{name: "valid IDs", cfg: Config{DefaultBotUserID: agentAID, TranscriptBotUserID: agentBID}},
		{name: "legacy names are not IDs", cfg: Config{DefaultBotName: "ai", TranscriptGenerator: "ai"}},
		{name: "default is a username", cfg: Config{DefaultBotUserID: "ai"}, wantErr: true},
		{name: "transcript is malformed", cfg: Config{TranscriptBotUserID: agentBID + "x"}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.ValidateAgentReferences()
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.True(t, errors.Is(err, ErrInvalidAgentReference))
		})
	}
}

func TestReconcileAgentReferences(t *testing.T) {
	tests := []struct {
		name       string
		prev       *Config
		next       Config
		wantID     string
		wantLegacy string
	}{
		{
			name:   "an ID drops the legacy name",
			prev:   &Config{DefaultBotName: "ai"},
			next:   Config{DefaultBotUserID: agentAID, DefaultBotName: "ai"},
			wantID: agentAID,
		},
		{
			name:       "a legacy-only request selects by name",
			next:       Config{DefaultBotName: "ai"},
			wantLegacy: "ai",
		},
		{
			name:       "a client that only edits the legacy name replaces the ID it round-tripped",
			prev:       &Config{DefaultBotUserID: agentAID},
			next:       Config{DefaultBotUserID: agentAID, DefaultBotName: "other"},
			wantLegacy: "other",
		},
		{
			name:   "a changed ID wins over a changed legacy name",
			prev:   &Config{DefaultBotUserID: agentAID},
			next:   Config{DefaultBotUserID: agentBID, DefaultBotName: "other"},
			wantID: agentBID,
		},
		{
			name: "clearing both clears the setting",
			prev: &Config{DefaultBotUserID: agentAID},
			next: Config{},
		},
		{
			name:   "an unchanged ID is kept",
			prev:   &Config{DefaultBotUserID: agentAID},
			next:   Config{DefaultBotUserID: agentAID},
			wantID: agentAID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ReconcileAgentReferences(tc.prev, tc.next)
			assert.Equal(t, tc.wantID, got.DefaultBotUserID)
			assert.Equal(t, tc.wantLegacy, got.DefaultBotName)

			// The transcript setting follows the same rule.
			toTranscript := func(c Config) Config {
				return Config{TranscriptBotUserID: c.DefaultBotUserID, TranscriptGenerator: c.DefaultBotName}
			}
			var prev *Config
			if tc.prev != nil {
				p := toTranscript(*tc.prev)
				prev = &p
			}
			got = ReconcileAgentReferences(prev, toTranscript(tc.next))
			assert.Equal(t, tc.wantID, got.TranscriptBotUserID)
			assert.Equal(t, tc.wantLegacy, got.TranscriptGenerator)
		})
	}
}

func TestClearAgentReferences(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		botUserID   string
		username    string
		want        Config
		wantCleared bool
	}{
		{
			name:        "references by ID are cleared",
			cfg:         Config{DefaultBotUserID: agentAID, TranscriptBotUserID: agentAID},
			botUserID:   agentAID,
			username:    "doomed",
			wantCleared: true,
		},
		{
			name:        "legacy names of the deleted agent are cleared",
			cfg:         Config{DefaultBotName: "doomed", TranscriptGenerator: "Doomed"},
			botUserID:   agentAID,
			username:    "doomed",
			wantCleared: true,
		},
		{
			name:      "references to other agents are kept",
			cfg:       Config{DefaultBotUserID: agentBID, TranscriptGenerator: "keeper"},
			botUserID: agentAID,
			username:  "doomed",
			want:      Config{DefaultBotUserID: agentBID, TranscriptGenerator: "keeper"},
		},
		{
			name:     "an agent without a bot user ID clears no empty ID",
			cfg:      Config{DefaultBotName: "keeper"},
			username: "doomed",
			want:     Config{DefaultBotName: "keeper"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, cleared := tc.cfg.ClearAgentReferences(tc.botUserID, tc.username)
			assert.Equal(t, tc.wantCleared, cleared)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMigrateAgentReferences(t *testing.T) {
	tests := []struct {
		name         string
		cfg          Config
		resolved     map[string]string
		want         Config
		wantMigrated bool
	}{
		{
			name:         "both settings resolve",
			cfg:          Config{DefaultBotName: "ai", TranscriptGenerator: "scribe"},
			resolved:     map[string]string{"ai": agentAID, "scribe": agentBID},
			want:         Config{DefaultBotUserID: agentAID, TranscriptBotUserID: agentBID},
			wantMigrated: true,
		},
		{
			name:         "an unresolved name is left in place",
			cfg:          Config{DefaultBotName: "ai", TranscriptGenerator: "gone"},
			resolved:     map[string]string{"ai": agentAID},
			want:         Config{DefaultBotUserID: agentAID, TranscriptGenerator: "gone"},
			wantMigrated: true,
		},
		{
			name:     "nothing resolves",
			cfg:      Config{DefaultBotName: "gone"},
			resolved: map[string]string{},
			want:     Config{DefaultBotName: "gone"},
		},
		{
			name:     "an existing ID is never overwritten",
			cfg:      Config{DefaultBotUserID: agentBID, DefaultBotName: "ai"},
			resolved: map[string]string{"ai": agentAID},
			want:     Config{DefaultBotUserID: agentBID, DefaultBotName: "ai"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, migrated := tc.cfg.MigrateAgentReferences(tc.resolved)
			assert.Equal(t, tc.wantMigrated, migrated)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLegacyAgentReferences(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want []string
	}{
		{name: "nothing configured", cfg: Config{}},
		{name: "already migrated", cfg: Config{DefaultBotUserID: agentAID, TranscriptBotUserID: agentBID}},
		{name: "both legacy", cfg: Config{DefaultBotName: "ai", TranscriptGenerator: "scribe"}, want: []string{"ai", "scribe"}},
		{name: "same agent for both", cfg: Config{DefaultBotName: "ai", TranscriptGenerator: "ai"}, want: []string{"ai"}},
		{name: "only the transcript is legacy", cfg: Config{DefaultBotUserID: agentAID, TranscriptGenerator: "scribe"}, want: []string{"scribe"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.cfg.LegacyAgentReferences())
		})
	}
}
