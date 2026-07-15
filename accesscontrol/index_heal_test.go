// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the self-healing that closes the lease-loss hole: a lost
// cluster-mutex lease plus a concurrent opposing mutation can leave the
// policy index divergent from the stored policies. Every decision with a
// definitive outcome schedules a background reconciliation of the marker for
// the evaluated resource (the hot path never blocks on the index for
// definitive outcomes), and activation rebuilds the whole index from server
// truth.

func notFoundGetAppErr() *model.AppError {
	return model.NewAppError("GetAccessControlPolicy", "not found", nil, "", http.StatusNotFound)
}

// drainReconciler waits until the background worker has processed every
// request that made it into the queue, then returns. enqueued == processed
// with a quiescent worker gives deterministic assertions.
func drainReconciler(t *testing.T, c *Checker) {
	t.Helper()
	require.Eventually(t, func() bool {
		return c.reconciler.processed.Load() == c.reconciler.enqueued.Load()
	}, 5*time.Second, time.Millisecond)
}

// TestDecisionHealsMissingMarker: allow/deny outcomes prove a stored policy
// exists, so a missing marker is restored (via the background worker) —
// across every decision entry point.
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
			t.Cleanup(c.Close)

			err := tt.invoke(c, model.NewId(), resourceID)
			if tt.denied {
				assert.ErrorIs(t, err, ErrAccessDenied)
			} else {
				assert.NoError(t, err)
			}

			drainReconciler(t, c)
			assert.Equal(t, []string{indexKey(tt.resType, resourceID)}, index.addedKeys(),
				"a definitive policy outcome must restore the missing marker")
			assert.True(t, index.marker(tt.resType, resourceID))
		})
	}
}

// TestDecisionDoesNotRewritePresentMarker: no index write when the marker
// already matches the outcome (only write when divergent).
func TestDecisionDoesNotRewritePresentMarker(t *testing.T) {
	resourceID := model.NewId()
	client := &stubDecisionClient{outcome: OutcomeAllow}
	index := &stubPolicyIndex{has: map[string]bool{indexKey(ResourceTypeService, resourceID): true}}
	c := New(client, nil, index, NoMCPServerIDs, nil, nil)
	t.Cleanup(c.Close)

	require.NoError(t, c.CanUseService(context.Background(), model.NewId(), resourceID))
	drainReconciler(t, c)
	assert.Empty(t, index.addedKeys())
	assert.Empty(t, index.removedKeys())
}

// TestDecisionHotPathNeverReadsIndexOnDefinitiveOutcomes pins the M1 remedy:
// allow/deny/no_policy outcomes must not read (or write) the KV-backed index
// on the calling goroutine — all index work happens on the background worker.
// The worker is stopped before the calls, so any read the counting stub sees
// can only have come from the hot path.
func TestDecisionHotPathNeverReadsIndexOnDefinitiveOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		outcome Outcome
	}{
		{name: "allow", outcome: OutcomeAllow},
		{name: "deny", outcome: OutcomeDeny},
		{name: "no_policy", outcome: OutcomeNoPolicy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceID := model.NewId()
			client := &stubDecisionClient{outcome: tt.outcome}
			index := &stubPolicyIndex{has: map[string]bool{}}
			c := New(client, nil, index, NoMCPServerIDs, nil, nil)
			c.Close() // stop the worker: only hot-path reads can reach the stub now

			err := c.CanUseService(context.Background(), model.NewId(), resourceID)
			if tt.outcome == OutcomeDeny {
				require.ErrorIs(t, err, ErrAccessDenied)
			} else {
				require.NoError(t, err)
			}

			assert.Zero(t, index.reads(), "definitive outcomes must not read the index on the hot path")
			assert.Empty(t, index.addedKeys())
			assert.Empty(t, index.removedKeys())
		})
	}
}

// TestDecisionFailClosedPathStillReadsIndexSynchronously: the authoritative
// synchronous index read must remain on the unavailable/error fail-closed
// paths — that gate cannot be deferred to a background worker.
func TestDecisionFailClosedPathStillReadsIndexSynchronously(t *testing.T) {
	resourceID := model.NewId()
	client := &stubDecisionClient{outcome: OutcomeUnavailable}
	index := &stubPolicyIndex{has: map[string]bool{indexKey(ResourceTypeService, resourceID): true}}
	c := New(client, nil, index, NoMCPServerIDs, nil, nil)
	c.Close()

	assert.ErrorIs(t, c.CanUseService(context.Background(), model.NewId(), resourceID), ErrAccessDenied)
	assert.Equal(t, 1, index.reads(), "the fail-closed gate must consult the index synchronously")
}

// TestReconcilerDedupsBurstsToOneHeal: a burst of decisions against one
// divergent resource costs exactly one background reconciliation — later
// requests are dropped while one is pending or inside the cooldown window.
func TestReconcilerDedupsBurstsToOneHeal(t *testing.T) {
	resourceID := model.NewId()
	client := &stubDecisionClient{outcome: OutcomeAllow}
	index := &stubPolicyIndex{has: map[string]bool{}}
	c := New(client, nil, index, NoMCPServerIDs, nil, nil)
	t.Cleanup(c.Close)
	c.reconciler.cooldown = time.Hour

	const burst = 5
	for range burst {
		require.NoError(t, c.CanUseService(context.Background(), model.NewId(), resourceID))
	}
	drainReconciler(t, c)

	assert.Equal(t, []string{indexKey(ResourceTypeService, resourceID)}, index.addedKeys(),
		"the burst must converge to exactly one heal")
	assert.EqualValues(t, 1, c.reconciler.enqueued.Load())
	assert.EqualValues(t, 1, c.reconciler.processed.Load())
	assert.EqualValues(t, burst-1, c.reconciler.dropped.Load(),
		"every duplicate in the burst must be dropped, not queued")
}

// TestReconcilerThrottlesPerResourceCooldown: after a reconciliation, further
// requests for the same resource are dropped until the cooldown elapses.
func TestReconcilerThrottlesPerResourceCooldown(t *testing.T) {
	resourceID := model.NewId()
	client := &stubDecisionClient{outcome: OutcomeAllow}
	index := &stubPolicyIndex{has: map[string]bool{}}
	c := New(client, nil, index, NoMCPServerIDs, nil, nil)
	t.Cleanup(c.Close)
	c.reconciler.cooldown = time.Hour

	require.NoError(t, c.CanUseService(context.Background(), model.NewId(), resourceID))
	drainReconciler(t, c)
	require.Len(t, index.addedKeys(), 1)

	// Re-create the divergence; the cooldown must suppress a second heal.
	index.setMarker(ResourceTypeService, resourceID, false)
	require.NoError(t, c.CanUseService(context.Background(), model.NewId(), resourceID))

	assert.EqualValues(t, 1, c.reconciler.enqueued.Load(), "within the cooldown nothing new is queued")
	assert.EqualValues(t, 1, c.reconciler.dropped.Load())
	drainReconciler(t, c)
	assert.Len(t, index.addedKeys(), 1, "no second heal inside the cooldown window")

	// An expired cooldown lets the next decision re-enqueue and heal.
	c.reconciler.mu.Lock()
	c.reconciler.lastRun[ResourceTypeService+"/"+resourceID] = time.Now().Add(-2 * time.Hour)
	c.reconciler.mu.Unlock()
	require.NoError(t, c.CanUseService(context.Background(), model.NewId(), resourceID))
	drainReconciler(t, c)
	assert.Len(t, index.addedKeys(), 2, "after the cooldown the divergence heals again")
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
			t.Cleanup(c.Close)

			require.NoError(t, c.CanUseService(context.Background(), model.NewId(), resourceID))
			drainReconciler(t, c)

			if tt.wantRemove {
				assert.Equal(t, []string{indexKey(ResourceTypeService, resourceID)}, index.removedKeys())
				assert.False(t, index.marker(ResourceTypeService, resourceID))
			} else {
				assert.Empty(t, index.removedKeys())
				assert.True(t, index.marker(ResourceTypeService, resourceID))
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
	t.Cleanup(c.Close)
	assert.ErrorIs(t, c.CanUseService(context.Background(), model.NewId(), resourceID), ErrAccessDenied)
	drainReconciler(t, c)
	assert.Empty(t, index.addedKeys())
	assert.Empty(t, index.removedKeys())
	assert.Zero(t, c.reconciler.enqueued.Load(), "unavailable outcomes are never scheduled")
}

// TestDecisionSkipsHealForNonAddressableIDs: short-circuited evaluations
// (config bots with UUID IDs) never touch the index.
func TestDecisionSkipsHealForNonAddressableIDs(t *testing.T) {
	client := &stubDecisionClient{outcome: OutcomeNoPolicy}
	index := &stubPolicyIndex{has: map[string]bool{}}
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	c := New(client, api, index, NoMCPServerIDs, nil, nil)
	t.Cleanup(c.Close)

	require.NoError(t, c.CanUseService(context.Background(), model.NewId(), "550e8400-e29b-41d4-a716-446655440000"))
	drainReconciler(t, c)
	assert.Empty(t, index.addedKeys())
	assert.Empty(t, index.removedKeys())
	assert.Zero(t, c.reconciler.enqueued.Load())
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
	// The stale marker is fetched once in the truth-gathering phase and
	// re-confirmed under the mutation lock before the fail-open removal.
	api.On("GetAccessControlPolicy", serviceStale).Return(nil, notFoundGetAppErr()).Twice()
	api.On("GetAccessControlPolicy", mcpUnreadable).
		Return(nil, model.NewAppError("GetAccessControlPolicy", "boom", nil, "", http.StatusInternalServerError)).Once()
	api.On("GetAccessControlPolicy", agentInSync).Return(&model.AccessControlPolicy{ID: agentInSync}, nil).Once()

	kv := newFakeSystemKV()
	index := NewKVPolicyIndex(kv, nil)
	require.NoError(t, index.Add(ResourceTypeService, serviceStale))
	require.NoError(t, index.Add(ResourceTypeMCP, mcpUnreadable))
	require.NoError(t, index.Add(ResourceTypeAgent, agentInSync))

	c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)
	t.Cleanup(c.Close)
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

// TestRebuildIndexRemovalRegatedUnderLock: the pre-lock 404 may predate a
// save that committed in the meantime; the under-lock re-confirmation must
// keep the marker when the policy exists again.
func TestRebuildIndexRemovalRegatedUnderLock(t *testing.T) {
	resourceID := model.NewId()

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	// First Get (truth phase): 404. Second Get (under lock): policy exists.
	api.On("GetAccessControlPolicy", resourceID).Return(nil, notFoundGetAppErr()).Once()
	api.On("GetAccessControlPolicy", resourceID).Return(&model.AccessControlPolicy{ID: resourceID}, nil).Once()

	index := &stubPolicyIndex{has: map[string]bool{indexKey(ResourceTypeService, resourceID): true}}
	c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)
	t.Cleanup(c.Close)

	c.RebuildIndex(context.Background(), map[string][]string{ResourceTypeService: {resourceID}})

	assert.Empty(t, index.removedKeys(), "a marker whose policy re-appeared under the lock must survive")
	assert.True(t, index.marker(ResourceTypeService, resourceID))
}

// TestRebuildIndexAbandonsOnCancelledContext: a cancelled lifecycle context
// (plugin deactivation) abandons the sweep without touching the index.
func TestRebuildIndexAbandonsOnCancelledContext(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)

	index := &stubPolicyIndex{has: map[string]bool{}}
	c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)
	t.Cleanup(c.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.RebuildIndex(ctx, map[string][]string{ResourceTypeAgent: {model.NewId()}})

	assert.Zero(t, index.reads())
	assert.Empty(t, index.addedKeys())
	assert.Empty(t, index.removedKeys())
}

func TestRebuildIndexNoopsWithoutPluginAPI(t *testing.T) {
	index := &stubPolicyIndex{has: map[string]bool{indexKey(ResourceTypeAgent, model.NewId()): true}}
	c := New(PassthroughClient{}, nil, index, NoMCPServerIDs, nil, nil)
	t.Cleanup(c.Close)
	c.RebuildIndex(context.Background(), map[string][]string{ResourceTypeAgent: {model.NewId()}})
	assert.Empty(t, index.addedKeys())
	assert.Empty(t, index.removedKeys())
}
