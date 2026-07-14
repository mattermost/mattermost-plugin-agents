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
	"github.com/stretchr/testify/require"
)

func TestPluginAPIClientEvaluateAccessRequest(t *testing.T) {
	tests := []struct {
		name        string
		decision    *model.PluginAccessControlDecision
		appErr      *model.AppError
		wantOutcome Outcome
		wantErr     bool
	}{
		{name: "allow", decision: &model.PluginAccessControlDecision{Outcome: model.AccessDecisionOutcomeAllow}, wantOutcome: OutcomeAllow},
		{name: "deny", decision: &model.PluginAccessControlDecision{Outcome: model.AccessDecisionOutcomeDeny}, wantOutcome: OutcomeDeny},
		{name: "no_policy", decision: &model.PluginAccessControlDecision{Outcome: model.AccessDecisionOutcomeNoPolicy}, wantOutcome: OutcomeNoPolicy},
		{name: "unavailable", decision: &model.PluginAccessControlDecision{Outcome: model.AccessDecisionOutcomeUnavailable}, wantOutcome: OutcomeUnavailable},
		{name: "unknown outcome maps to unavailable", decision: &model.PluginAccessControlDecision{Outcome: model.AccessDecisionOutcome("future_value")}, wantOutcome: OutcomeUnavailable},
		{name: "app error", appErr: model.NewAppError("EvaluateAccessControl", "boom", nil, "", http.StatusInternalServerError), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &plugintest.API{}
			defer api.AssertExpectations(t)
			api.On("EvaluateAccessControl", "userid", ResourceTypeAgent, "resourceid", ActionUse).
				Return(tt.decision, tt.appErr).Once()

			client := NewPluginAPIClient(api)
			outcome, err := client.EvaluateAccessRequest(context.Background(), "userid", ResourceTypeAgent, "resourceid", ActionUse)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOutcome, outcome)
		})
	}
}
