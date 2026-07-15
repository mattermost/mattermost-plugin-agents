// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSavePolicyOverwritesIdentityFieldsAndUpdatesIndex(t *testing.T) {
	tests := []struct {
		name        string
		bodyName    string
		defaultName string
		wantName    string
	}{
		{name: "empty name defaults", bodyName: "", defaultName: "Agent policy", wantName: "Agent policy"},
		{name: "explicit name kept", bodyName: "My policy", defaultName: "Agent policy", wantName: "My policy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceID := model.NewId()
			actingUserID := model.NewId()

			api := &plugintest.API{}
			defer api.AssertExpectations(t)
			var savedPolicy *model.AccessControlPolicy
			api.On("SaveAccessControlPolicy", actingUserID, mock.AnythingOfType("*model.AccessControlPolicy")).
				Run(func(args mock.Arguments) {
					savedPolicy = args.Get(1).(*model.AccessControlPolicy)
				}).
				Return(&model.AccessControlPolicy{ID: resourceID}, nil).Once()

			index := &stubPolicyIndex{has: map[string]bool{}}
			c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)

			// Spoofed identity fields must be overwritten from the route
			// (contract §7.2): ID, Type, Version, Active never come from the body.
			body := &model.AccessControlPolicy{
				ID:      "spoofed-id",
				Type:    model.AccessControlPolicyTypeChannel,
				Version: "v99",
				Active:  false,
				Name:    tt.bodyName,
			}

			_, err := c.SavePolicy(context.Background(), actingUserID, ResourceTypeAgent, resourceID, tt.defaultName, body)
			require.NoError(t, err)

			require.NotNil(t, savedPolicy)
			assert.Equal(t, resourceID, savedPolicy.ID)
			assert.Equal(t, ResourceTypeAgent, savedPolicy.Type)
			assert.Equal(t, model.AccessControlPolicyVersionV0_5, savedPolicy.Version)
			assert.True(t, savedPolicy.Active)
			assert.Equal(t, tt.wantName, savedPolicy.Name)

			assert.Equal(t, []string{indexKey(ResourceTypeAgent, resourceID)}, index.added)
		})
	}
}

func TestDeletePolicyUpdatesIndex(t *testing.T) {
	resourceID := model.NewId()
	actingUserID := model.NewId()

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("DeleteAccessControlPolicy", actingUserID, ResourceTypeService, resourceID).
		Return(nil).Once()

	index := &stubPolicyIndex{has: map[string]bool{}}
	c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)

	require.NoError(t, c.DeletePolicy(context.Background(), actingUserID, ResourceTypeService, resourceID))
	assert.Equal(t, []string{indexKey(ResourceTypeService, resourceID)}, index.removed)
}

func TestGetAndDeletePolicyNotFound(t *testing.T) {
	resourceID := model.NewId()
	actingUserID := model.NewId()
	notFound := model.NewAppError("GetAccessControlPolicy", "not found", nil, "", http.StatusNotFound)

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("GetAccessControlPolicy", resourceID).Return(nil, notFound).Once()
	api.On("DeleteAccessControlPolicy", actingUserID, ResourceTypeMCP, resourceID).Return(notFound).Once()

	index := &stubPolicyIndex{has: map[string]bool{}}
	c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)

	_, err := c.GetPolicy(context.Background(), resourceID)
	assert.ErrorIs(t, err, ErrPolicyNotFound)

	err = c.DeletePolicy(context.Background(), actingUserID, ResourceTypeMCP, resourceID)
	assert.ErrorIs(t, err, ErrPolicyNotFound)
	assert.Empty(t, index.removed, "not-found delete must not touch the index")
}

// --- F1: conservative marker-first ordering ---

func TestSavePolicyIndexWriteFailureBlocksSave(t *testing.T) {
	resourceID := model.NewId()

	// No SaveAccessControlPolicy expectation: an index write failure must
	// abort the mutation before the policy is ever persisted.
	api := &plugintest.API{}
	defer api.AssertExpectations(t)

	index := &stubPolicyIndex{addErr: errors.New("kv write failed")}
	c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)

	_, err := c.SavePolicy(context.Background(), model.NewId(), ResourceTypeAgent, resourceID, "policy", &model.AccessControlPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the policy was not saved")
}

func TestSavePolicyRollsBackFreshMarkerOnSaveFailure(t *testing.T) {
	resourceID := model.NewId()
	saveErr := model.NewAppError("SaveAccessControlPolicy", "boom", nil, "", http.StatusInternalServerError)

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("SaveAccessControlPolicy", mock.Anything, mock.Anything).Return(nil, saveErr).Once()

	index := &stubPolicyIndex{}
	c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)

	_, err := c.SavePolicy(context.Background(), model.NewId(), ResourceTypeAgent, resourceID, "policy", &model.AccessControlPolicy{})
	require.Error(t, err)

	// The marker added by this call is rolled back best-effort.
	assert.Equal(t, []string{indexKey(ResourceTypeAgent, resourceID)}, index.removed)
	assert.False(t, index.has[indexKey(ResourceTypeAgent, resourceID)])
}

func TestSavePolicyKeepsPreexistingMarkerOnSaveFailure(t *testing.T) {
	resourceID := model.NewId()
	saveErr := model.NewAppError("SaveAccessControlPolicy", "boom", nil, "", http.StatusInternalServerError)

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("SaveAccessControlPolicy", mock.Anything, mock.Anything).Return(nil, saveErr).Once()

	// The resource already had a policy (and marker) before this failed
	// update; rolling the marker back would fail open for the old policy.
	index := &stubPolicyIndex{has: map[string]bool{indexKey(ResourceTypeAgent, resourceID): true}}
	c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)

	_, err := c.SavePolicy(context.Background(), model.NewId(), ResourceTypeAgent, resourceID, "policy", &model.AccessControlPolicy{})
	require.Error(t, err)

	assert.Empty(t, index.removed, "a pre-existing marker must survive a failed policy update")
	assert.True(t, index.has[indexKey(ResourceTypeAgent, resourceID)])
}

// TestConcurrentSaveDeleteHoldsMarkerInvariant hammers SavePolicy/DeletePolicy
// from concurrent goroutines and asserts the F1 invariant at every save (the
// marker is already present when the policy is persisted — index-first
// ordering under the mutation mutex) and at the end (an existing policy
// always has its marker).
func TestConcurrentSaveDeleteHoldsMarkerInvariant(t *testing.T) {
	resourceID := model.NewId()
	actingUserID := model.NewId()

	kv := newFakeSystemKV()
	index := NewKVPolicyIndex(kv, nil)

	var policyExists atomic.Bool

	api := &plugintest.API{}
	api.On("SaveAccessControlPolicy", mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) {
			// Runs inside the checker's mutation critical section.
			has, hasErr := index.Has(ResourceTypeAgent, resourceID)
			assert.NoError(t, hasErr)
			assert.True(t, has, "policy persisted without its fail-closed marker")
			policyExists.Store(true)
		}).
		Return(&model.AccessControlPolicy{ID: resourceID}, nil)
	api.On("DeleteAccessControlPolicy", actingUserID, ResourceTypeAgent, resourceID).
		Run(func(_ mock.Arguments) {
			policyExists.Store(false)
		}).
		Return(nil)

	c := New(PassthroughClient{}, api, index, NoMCPServerIDs, nil, nil)

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := range 25 {
				if (seed+i)%2 == 0 {
					_, err := c.SavePolicy(context.Background(), actingUserID, ResourceTypeAgent, resourceID, "policy", &model.AccessControlPolicy{})
					assert.NoError(t, err)
				} else {
					err := c.DeletePolicy(context.Background(), actingUserID, ResourceTypeAgent, resourceID)
					assert.NoError(t, err)
				}
			}
		}(g)
	}
	wg.Wait()

	if policyExists.Load() {
		has, err := index.Has(ResourceTypeAgent, resourceID)
		require.NoError(t, err)
		assert.True(t, has, "an enforced policy must always carry its fail-closed marker")
	}
}
