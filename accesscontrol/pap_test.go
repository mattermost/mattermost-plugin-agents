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

func TestSavePolicyOverwritesIdentityFields(t *testing.T) {
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

			c := New(PassthroughClient{}, api, NoMCPServerIDs, nil)

			// Spoofed identity fields (ID, Type, Version, Active) must be
			// overwritten from the route, never taken from the body.
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
		})
	}
}

func TestSavePolicySurfacesSaveFailure(t *testing.T) {
	saveErr := model.NewAppError("SaveAccessControlPolicy", "boom", nil, "", http.StatusInternalServerError)

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("SaveAccessControlPolicy", mock.Anything, mock.Anything).Return(nil, saveErr).Once()

	c := New(PassthroughClient{}, api, NoMCPServerIDs, nil)

	_, err := c.SavePolicy(context.Background(), model.NewId(), ResourceTypeAgent, model.NewId(), "policy", &model.AccessControlPolicy{})
	require.Error(t, err)
}

func TestDeletePolicyHappyPath(t *testing.T) {
	resourceID := model.NewId()
	actingUserID := model.NewId()

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("DeleteAccessControlPolicy", actingUserID, ResourceTypeService, resourceID).
		Return(nil).Once()

	c := New(PassthroughClient{}, api, NoMCPServerIDs, nil)

	require.NoError(t, c.DeletePolicy(context.Background(), actingUserID, ResourceTypeService, resourceID))
}

func TestGetAndDeletePolicyNotFound(t *testing.T) {
	resourceID := model.NewId()
	actingUserID := model.NewId()
	notFound := model.NewAppError("GetAccessControlPolicy", "not found", nil, "", http.StatusNotFound)

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("GetAccessControlPolicy", resourceID).Return(nil, notFound).Once()
	api.On("DeleteAccessControlPolicy", actingUserID, ResourceTypeMCP, resourceID).Return(notFound).Once()

	c := New(PassthroughClient{}, api, NoMCPServerIDs, nil)

	_, err := c.GetPolicy(context.Background(), resourceID)
	assert.ErrorIs(t, err, ErrPolicyNotFound)

	err = c.DeletePolicy(context.Background(), actingUserID, ResourceTypeMCP, resourceID)
	assert.ErrorIs(t, err, ErrPolicyNotFound)
}

func TestPAPWithoutPluginAPI(t *testing.T) {
	c := New(PassthroughClient{}, nil, NoMCPServerIDs, nil)
	ctx := context.Background()
	userID := model.NewId()
	resourceID := model.NewId()

	tests := []struct {
		name    string
		call    func() error
		wantErr error
	}{
		{name: "GetPolicy reports not found", wantErr: ErrPolicyNotFound, call: func() error {
			_, err := c.GetPolicy(ctx, resourceID)
			return err
		}},
		{name: "DeletePolicy reports not found", wantErr: ErrPolicyNotFound, call: func() error {
			return c.DeletePolicy(ctx, userID, ResourceTypeAgent, resourceID)
		}},
		{name: "SavePolicy reports no plugin API", wantErr: errNoPluginAPI, call: func() error {
			_, err := c.SavePolicy(ctx, userID, ResourceTypeAgent, resourceID, "policy", &model.AccessControlPolicy{})
			return err
		}},
		{name: "CheckExpression reports no plugin API", wantErr: errNoPluginAPI, call: func() error {
			_, err := c.CheckExpression(ctx, userID, ResourceTypeAgent, "true")
			return err
		}},
		{name: "TestExpression reports no plugin API", wantErr: errNoPluginAPI, call: func() error {
			_, err := c.TestExpression(ctx, userID, ResourceTypeAgent, "true", "", "", 10)
			return err
		}},
		{name: "RequesterMatchesExpression reports no plugin API", wantErr: errNoPluginAPI, call: func() error {
			_, err := c.RequesterMatchesExpression(ctx, userID, ResourceTypeAgent, "true")
			return err
		}},
		{name: "FieldsAutocomplete reports no plugin API", wantErr: errNoPluginAPI, call: func() error {
			_, err := c.FieldsAutocomplete(ctx, userID, "", 10)
			return err
		}},
		{name: "VisualAST reports no plugin API", wantErr: errNoPluginAPI, call: func() error {
			_, err := c.VisualAST(ctx, userID, ResourceTypeAgent, "true")
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorIs(t, tt.call(), tt.wantErr)
		})
	}
}

func TestRequesterMatchesExpression(t *testing.T) {
	actingUserID := model.NewId()
	username := "cel-tester"
	expression := `user.attributes.department == "eng"`
	lookupErr := model.NewAppError("GetUser", "not found", nil, "", http.StatusNotFound)
	queryErr := model.NewAppError("QueryUsersForAccessControlExpression", "boom", nil, "", http.StatusInternalServerError)

	tests := []struct {
		name      string
		setup     func(api *plugintest.API)
		wantMatch bool
		wantErr   bool
	}{
		{
			name: "GetUser failure is an error",
			setup: func(api *plugintest.API) {
				api.On("GetUser", actingUserID).Return(nil, lookupErr).Once()
			},
			wantErr: true,
		},
		{
			name: "empty username fails closed without querying",
			setup: func(api *plugintest.API) {
				api.On("GetUser", actingUserID).Return(&model.User{Id: actingUserID}, nil).Once()
			},
		},
		{
			name: "other users in the username probe do not count as a self match",
			setup: func(api *plugintest.API) {
				api.On("GetUser", actingUserID).Return(&model.User{Id: actingUserID, Username: username}, nil).Once()
				api.On("QueryUsersForAccessControlExpression", actingUserID, ResourceTypeAgent, expression, username, "", requesterMatchQueryLimit).
					Return(&model.AccessControlPolicyTestResponse{
						Users: []*model.User{{Id: model.NewId(), Username: username + "2"}},
						Total: 1,
					}, nil).Once()
			},
		},
		{
			name: "acting user in the username probe matches",
			setup: func(api *plugintest.API) {
				api.On("GetUser", actingUserID).Return(&model.User{Id: actingUserID, Username: username}, nil).Once()
				api.On("QueryUsersForAccessControlExpression", actingUserID, ResourceTypeAgent, expression, username, "", requesterMatchQueryLimit).
					Return(&model.AccessControlPolicyTestResponse{
						Users: []*model.User{{Id: actingUserID, Username: username}},
						Total: 1,
					}, nil).Once()
			},
			wantMatch: true,
		},
		{
			name: "query failure is an error",
			setup: func(api *plugintest.API) {
				api.On("GetUser", actingUserID).Return(&model.User{Id: actingUserID, Username: username}, nil).Once()
				api.On("QueryUsersForAccessControlExpression", actingUserID, ResourceTypeAgent, expression, username, "", requesterMatchQueryLimit).
					Return(nil, queryErr).Once()
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &plugintest.API{}
			defer api.AssertExpectations(t)
			tt.setup(api)

			c := New(PassthroughClient{}, api, NoMCPServerIDs, nil)
			matched, err := c.RequesterMatchesExpression(context.Background(), actingUserID, ResourceTypeAgent, expression)
			if tt.wantErr {
				require.Error(t, err)
				assert.False(t, matched)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMatch, matched)
		})
	}
}
