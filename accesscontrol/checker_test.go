// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the legacy-passthrough behavior of the WS-C scaffolding.
// When WS-D swaps in real evaluation, they become the no_policy rows of its
// decision-table test suite: no_policy must keep behaving exactly like this.

func newTestChecker() *Checker {
	return New(PassthroughClient{}, EmptyPolicyIndex{}, pluginapi.LogService{})
}

func TestPassthroughClientEvaluateAccessRequest(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
	}{
		{name: "agent resource", resourceType: ResourceTypeAgent},
		{name: "service resource", resourceType: ResourceTypeService},
		{name: "mcp resource", resourceType: ResourceTypeMCP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, err := PassthroughClient{}.EvaluateAccessRequest(context.Background(), "userid", tt.resourceType, "resourceid", ActionUse)
			require.NoError(t, err)
			assert.Equal(t, OutcomeNoPolicy, outcome)
		})
	}
}

func TestCheckerCanUseAgentPassthrough(t *testing.T) {
	legacyErr := errors.New("legacy restriction")

	tests := []struct {
		name        string
		legacyCheck func() error
		wantErr     error
	}{
		{name: "nil legacy check allows", legacyCheck: nil, wantErr: nil},
		{name: "passing legacy check allows", legacyCheck: func() error { return nil }, wantErr: nil},
		{name: "failing legacy check returns same error", legacyCheck: func() error { return legacyErr }, wantErr: legacyErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestChecker()
			err := c.CanUseAgent(context.Background(), "userid", &llm.BotConfig{ID: "agentid"}, tt.legacyCheck)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCheckerPassthroughHelpersAllow(t *testing.T) {
	c := newTestChecker()

	tests := []struct {
		name  string
		check func() error
	}{
		{name: "CanUseService", check: func() error { return c.CanUseService(context.Background(), "userid", "serviceid") }},
		{name: "CanUseMCPServer", check: func() error { return c.CanUseMCPServer(context.Background(), "userid", "serverid") }},
		{name: "ValidateAgentWrite", check: func() error {
			return c.ValidateAgentWrite(context.Background(), "userid", &llm.BotConfig{ID: "agentid"})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NoError(t, tt.check())
		})
	}
}

func TestEmptyPolicyIndexHas(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		resourceID   string
	}{
		{name: "agent", resourceType: ResourceTypeAgent, resourceID: "agentid"},
		{name: "service", resourceType: ResourceTypeService, resourceID: "serviceid"},
		{name: "mcp", resourceType: ResourceTypeMCP, resourceID: "serverid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			has, err := EmptyPolicyIndex{}.Has(tt.resourceType, tt.resourceID)
			assert.NoError(t, err)
			assert.False(t, has)
		})
	}
}
