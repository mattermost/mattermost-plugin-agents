// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the self-healing that closes the lease-loss hole: a lost
// cluster-mutex lease plus a concurrent opposing mutation can leave the
// policy index divergent from the stored policies; every decision with a
// definitive outcome must reconcile the marker for the evaluated resource,
// and activation rebuilds the whole index from server truth.

func notFoundGetAppErr() *model.AppError {
	return model.NewAppError("GetAccessControlPolicy", "not found", nil, "", http.StatusNotFound)
}

// TestDecisionHealsMissingMarker: allow/deny outcomes prove a stored policy
// exists, so a missing marker is restored — across every decision entry point.
func TestDecisionHealsMissingMarker(t *testing.T) {
	tests := []struct {
		name    string
		outcome Outcome
		invoke  func(c *Checker, userID, resourceID string) error
		resType string
		denied  bool
	}{
		{name: "agent allow", outcome: OutcomeAllow, resType: ResourceTypeAgent, invoke: func(c *Checker, userID, resourceID string) error {
			return c.CanUseAgent(context.Background(), userID, &llm.BotConfig{ID: resourceID}, nil)
		}},
		{name: "agent deny", outcome: OutcomeDeny, resType: ResourceTypeAgent, denied: true, invoke: func(c *Checker, userID, resourceID string) error {
			return c.CanUseAgent(context.Background(), userID, &llm.BotConfig{ID: resourceID}, nil)
		}},
		{name: "service allow", outcome: OutcomeAllow, resType: ResourceTypeService, invoke: func(c *Checker, userID, resourceID string) error {
			return c.CanUseService(context.Background(), userID, resourceID)
		}},
		{name: "service deny", outcome: OutcomeDeny, resType: ResourceTypeService, denied: true, invoke: func(c *Checker, userID, resourceID string) error {
			return c.CanUseService(context.Background(), userID, resourceID)
		}},
		{name: "mcp allow", outcome: OutcomeAllow, resType: ResourceTypeMCP, invoke: func(c *Checker, userID, resourceID string) error {
			return c.CanUseMCPServer(context.Background(), userID, resourceID)
		}},
		{name: "mcp deny", outcome: OutcomeDeny, resType: ResourceTypeMCP, denied: true, invoke: func(c *Checker, userID, resourceID string) error {
			return c.CanUseMCPServer(context.Background(), userID, resourceID)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceID := model.NewId()
			client := &stubDecisionClient{outcome: tt.outcome}
			index := &stubPolicyIndex{has: map[string]bool{}}
			c := New(client, nil, index, NoMCPServerIDs, nil, nil)

			err := tt.invoke(c, model.NewId(), resourceID)
			if tt.denied {
				assert.ErrorIs(t, err, ErrAccessDenied)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, []string{indexKey(tt.resType, resourceID)}, index.added,
				"a definitive policy outcome must restore the missing marker")
			assert.True(t, index.has[indexKey(tt.resType, resourceID)])
		})
	}
}

// TestDecisionDoesNotRewritePresentMarker: no index write when the marker
// already matches the outcome (the throttle: only write when divergent).
func TestDecisionDoesNotRewritePresentMarker(t *testing.T) {
	resourceID := model.NewId()
	client := &stubDecisionClient{outcome: OutcomeAllow}
	index := &stubPolicyIndex{has: map[string]bool{indexKey(ResourceTypeService, resourceID): true}}
	c := New(client, nil, index, NoMCPServerIDs, nil, nil)

	require.NoError(t, c.CanUseService(context.Background(), model.NewId(), resourceID))
	assert.Empty(t, index.added)
	assert.Empty(t, index.removed)
}

// TestDecisionHealsStaleMarker: a no_policy outcome with a marker present
// drops the marker — but only after Get confirms no stored policy (404).
func TestDecisionHealsStaleMarker(t *testing.T) {
	tests := []struct {
		name       string
		getPolicy  *model.AccessControlPolicy
		getErr     *model.AppError
		wantRemove bool
	}{
		{name: "confirmed 404 removes the stale marker", getErr: notFoundGetAppErr(), wantRemove: true},
		{name: "policy exists keeps the marker", getPolicy: &model.AccessControlPolicy{}},
		{name: "unreadable truth keeps the marker", getErr: model.NewAppError("GetAccessControlPolicy", "boom", nil, "", http.StatusInternalServerError)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceID := model.NewId()
			if tt.getPolicy != nil {
				tt.getPolicy.ID = resourceID
			}

			api := &plugintest.API{}
			defer api.AssertExpectations(t)
			api.On("GetAccessControlPolicy", resourceID).Return(tt.getPolicy, tt.getErr).Once()

			client := &stubDecisionClient{outcome: OutcomeNoPolicy}
			index := &stubPolicyIndex{has: map[string]bool{indexKey(ResourceTypeService, resourceID): true}}
			c := New(client, api, index, NoMCPServerIDs, nil, nil)

			require.NoError(t, c.CanUseService(context.Background(), model.NewId(), resourceID))

			if tt.wantRemove {
				assert.Equal(t, []string{indexKey(ResourceTypeService, resourceID)}, index.removed)
				assert.False(t, index.has[indexKey(ResourceTypeService, resourceID)])
			} else {
				assert.Empty(t, index.removed)
				assert.True(t, index.has[indexKey(ResourceTypeService, resourceID)])
			}
		})
	}
}

// TestDecisionNeverHealsOnUnavailable: unavailable outcomes carry no server
// truth; the index must not change even when it looks divergent.
func TestDecisionNeverHealsOnUnavailable(t *testing.T) {
	resourceID := model.NewId()
	client := &stubDecisionClient{outcome: OutcomeUnavailable}
	index := &stubPolicyIndex{has: map[string]bool{indexKey(ResourceTypeService, resourceID): true}}

	// No GetAccessControlPolicy expectation: it must never be consulted.
	api := &plugintest.API{}
	defer api.AssertExpectations(t)

	c := New(client, api, index, NoMCPServerIDs, nil, nil)
	assert.ErrorIs(t, c.CanUseService(context.Background(), model.NewId(), resourceID), ErrAccessDenied)
	assert.Empty(t, index.added)
	assert.Empty(t, index.removed)
}

// TestDecisionSkipsHealForNonAddressableIDs: short-circuited evaluations
// (config bots with UUID IDs) never touch the index.
func TestDecisionSkipsHealForNonAddressableIDs(t *testing.T) {
	client := &stubDecisionClient{outcome: OutcomeNoPolicy}
	index := &stubPolicyIndex{has: map[string]bool{}}
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	c := New(client, api, index, NoMCPServerIDs, nil, nil)

	require.NoError(t, c.CanUseService(context.Background(), model.NewId(), "550e8400-e29b-41d4-a716-446655440000"))
	assert.Empty(t, index.added)
	assert.Empty(t, index.removed)
}

// --- activation rebuild ---

func TestRebuildIndexHealsFromServerTruth(t *testing.T) {
	agentWithPolicy := model.NewId()   // policy exists, marker missing → add
	serviceStale := model.NewId()      // no policy, marker present → remove
	mcpUnreadable := model.NewId()     // Get fails, marker present → keep
	agentInSync := model.NewId()       // policy exists, marker present → untouched
	configBotID := "not-a-valid-26-id" // never policy-addressable → skipped

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("GetAccessControlPolicy", agentWithPolicy).Return(&model.AccessControlPolicy{ID: agentWithPolicy}, nil).Once()
	api.On("GetAccessControlPolicy", serviceStale).Return(nil, notFoundGetAppErr()).Once()
	api.On("GetAccessControlPolicy", mcpUnreadable).
		Return(nil, model.NewAppError("GetAccessControlPolicy", "boom", nil, "", http.StatusInternalServerError)).Once()
	api.On("GetAccessControlPolicy", agentInSync).Return(&model.AccessControlPolicy{ID: agentInSync}, nil).Once()

	kv := newFakeSystemKV()
	index := NewKVPolicyIndex(kv, nil)
	require.NoError(t, index.Add(ResourceTypeService, serviceStale))
	require.NoError(t, index.Add(ResourceTypeMCP, mcpUnreadable))
	require.NoError(t, index.Add(ResourceTypeAgent, agentInSync))

	c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)
	c.RebuildIndex(context.Background(), map[string][]string{
		ResourceTypeAgent:   {agentWithPolicy, agentInSync, configBotID},
		ResourceTypeService: {serviceStale},
		ResourceTypeMCP:     {mcpUnreadable},
	})

	assertHas := func(resourceType, resourceID string, want bool, msg string) {
		has, err := index.Has(resourceType, resourceID)
		require.NoError(t, err)
		assert.Equal(t, want, has, msg)
	}
	assertHas(ResourceTypeAgent, agentWithPolicy, true, "missing marker for an enforced policy must be restored")
	assertHas(ResourceTypeService, serviceStale, false, "stale marker without a policy must be dropped")
	assertHas(ResourceTypeMCP, mcpUnreadable, true, "unreadable truth must leave the marker unchanged")
	assertHas(ResourceTypeAgent, agentInSync, true, "in-sync marker must survive")
}

func TestRebuildIndexNoopsWithoutPluginAPI(t *testing.T) {
	index := &stubPolicyIndex{has: map[string]bool{indexKey(ResourceTypeAgent, model.NewId()): true}}
	c := New(PassthroughClient{}, nil, index, NoMCPServerIDs, nil, nil)
	c.RebuildIndex(context.Background(), map[string][]string{ResourceTypeAgent: {model.NewId()}})
	assert.Empty(t, index.added)
	assert.Empty(t, index.removed)
}
