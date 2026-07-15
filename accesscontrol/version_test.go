// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
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

// TestPassthroughWiringReportsUnavailable pins the pre-11.10 wiring that
// server/main.go selects when ServerSupportsABAC is false: passthrough
// decisions with no PAP handle. The status endpoint must report unavailable
// (hiding all ABAC UI) and attribute-based agent saves must be rejected,
// while legacy access checks keep working.
func TestPassthroughWiringReportsUnavailable(t *testing.T) {
	c := New(PassthroughClient{}, nil, NoMCPServerIDs, nil)

	assert.False(t, c.IsAvailable(context.Background()), "passthrough wiring must report ABAC unavailable")

	err := c.ValidateAgentWrite(context.Background(), model.NewId(), &llm.BotConfig{
		ID:              model.NewId(),
		ServiceID:       model.NewId(),
		UserAccessLevel: llm.UserAccessLevelAttributeBased,
	}, nil)
	assert.ErrorIs(t, err, ErrABACUnavailable, "attribute-based saves must be rejected under passthrough wiring")

	legacyRan := false
	err = c.CanUseAgent(context.Background(), model.NewId(), &llm.BotConfig{ID: model.NewId()}, func() error {
		legacyRan = true
		return nil
	})
	assert.NoError(t, err)
	assert.True(t, legacyRan, "legacy checks must keep running under passthrough wiring")
}
