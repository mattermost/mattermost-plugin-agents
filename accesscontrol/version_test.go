// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

func TestServerSupportsABAC(t *testing.T) {
	tests := []struct {
		name          string
		serverVersion string
		want          bool
	}{
		{name: "well below minimum", serverVersion: "10.5.0", want: false},
		{name: "one minor below minimum", serverVersion: "11.9.0", want: false},
		{name: "high patch below minimum", serverVersion: "11.9.14", want: false},
		{name: "exactly the minimum", serverVersion: "11.10.0", want: true},
		{name: "patch above minimum", serverVersion: "11.10.1", want: true},
		{name: "minor above minimum", serverVersion: "11.11.0", want: true},
		{name: "next major", serverVersion: "12.0.0", want: true},
		{name: "prerelease of the minimum predates it", serverVersion: "11.10.0-rc1", want: false},
		// Unparseable versions err toward supported so enforcement stays
		// fail-closed (the real client denies on transport failure) instead
		// of silently selecting passthrough.
		{name: "empty version errs toward supported", serverVersion: "", want: true},
		{name: "garbage version errs toward supported", serverVersion: "not-a-version", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ServerSupportsABAC(tt.serverVersion))
		})
	}
}

// TestLegacyOnlyModeDecisionTable pins the pre-11.10 wiring that
// server/main.go selects when ServerSupportsABAC is false (NewLegacyOnly):
// agents in the legacy access modes run their legacy checks unchanged;
// persisted attribute-based agents DENY — the plugin cannot resolve policy
// existence, so the no_policy fail-open must not apply; services and MCP
// servers are unrestricted.
func TestLegacyOnlyModeDecisionTable(t *testing.T) {
	legacyErr := errors.New("legacy restriction")
	c := NewLegacyOnly(NoMCPServerIDs, nil)

	agentCheck := func(attributeBased bool, legacy error) func() (error, bool) {
		return func() (error, bool) {
			cfg := &llm.BotConfig{ID: model.NewId()}
			if attributeBased {
				cfg.UserAccessLevel = llm.UserAccessLevelAttributeBased
			}
			legacyRan := false
			err := c.CanUseAgent(context.Background(), model.NewId(), cfg, func() error {
				legacyRan = true
				return legacy
			})
			return err, legacyRan
		}
	}

	tests := []struct {
		name          string
		check         func() (error, bool)
		wantErr       error
		wantLegacyRan bool
	}{
		{
			name:          "legacy-mode agent passes its legacy check",
			check:         agentCheck(false, nil),
			wantLegacyRan: true,
		},
		{
			name:          "legacy-mode agent fails its legacy check",
			check:         agentCheck(false, legacyErr),
			wantErr:       legacyErr,
			wantLegacyRan: true,
		},
		{
			name:    "attribute-based agent denies without running legacy checks",
			check:   agentCheck(true, nil),
			wantErr: ErrAccessDenied,
		},
		{
			name: "service unrestricted",
			check: func() (error, bool) {
				return c.CanUseService(context.Background(), model.NewId(), model.NewId()), false
			},
		},
		{
			name: "mcp server unrestricted",
			check: func() (error, bool) {
				return c.CanUseMCPServer(context.Background(), model.NewId(), model.NewId()), false
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err, legacyRan := tt.check()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantLegacyRan, legacyRan, "legacy-check participation")
		})
	}
}

// TestLegacyOnlyModeReportsUnavailable: the status endpoint must report
// unavailable (hiding all ABAC UI) and attribute-based agent saves must be
// rejected under legacy-only wiring.
func TestLegacyOnlyModeReportsUnavailable(t *testing.T) {
	c := NewLegacyOnly(NoMCPServerIDs, nil)

	assert.False(t, c.IsAvailable(context.Background()), "legacy-only wiring must report ABAC unavailable")

	err := c.ValidateAgentWrite(context.Background(), model.NewId(), &llm.BotConfig{
		ID:              model.NewId(),
		ServiceID:       model.NewId(),
		UserAccessLevel: llm.UserAccessLevelAttributeBased,
	}, nil)
	assert.ErrorIs(t, err, ErrABACUnavailable, "attribute-based saves must be rejected under legacy-only wiring")
}
