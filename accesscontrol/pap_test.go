// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"
	"net/http"
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
			c := New(PassthroughClient{}, api, index, nil)

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
	c := New(PassthroughClient{}, api, index, nil)

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
	c := New(PassthroughClient{}, api, index, nil)

	_, err := c.GetPolicy(context.Background(), resourceID)
	assert.ErrorIs(t, err, ErrPolicyNotFound)

	err = c.DeletePolicy(context.Background(), actingUserID, ResourceTypeMCP, resourceID)
	assert.ErrorIs(t, err, ErrPolicyNotFound)
	assert.Empty(t, index.removed, "not-found delete must not touch the index")
}
