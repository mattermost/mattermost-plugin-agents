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
		wantOutcome model.AccessDecisionOutcome
		wantErr     bool
	}{
		{name: "allow", decision: &model.PluginAccessControlDecision{Outcome: model.AccessDecisionOutcomeAllow}, wantOutcome: model.AccessDecisionOutcomeAllow},
		{name: "deny", decision: &model.PluginAccessControlDecision{Outcome: model.AccessDecisionOutcomeDeny}, wantOutcome: model.AccessDecisionOutcomeDeny},
		{name: "no_policy", decision: &model.PluginAccessControlDecision{Outcome: model.AccessDecisionOutcomeNoPolicy}, wantOutcome: model.AccessDecisionOutcomeNoPolicy},
		{name: "unavailable", decision: &model.PluginAccessControlDecision{Outcome: model.AccessDecisionOutcomeUnavailable}, wantOutcome: model.AccessDecisionOutcomeUnavailable},
		// Unknown values pass through verbatim; the checkers' default switch
		// row denies them (see TestCanUseAgentDecisionTable).
		{name: "unknown outcome passes through", decision: &model.PluginAccessControlDecision{Outcome: model.AccessDecisionOutcome("future_value")}, wantOutcome: model.AccessDecisionOutcome("future_value")},
		{name: "app error", appErr: model.NewAppError("EvaluateAccessControl", "boom", nil, "", http.StatusInternalServerError), wantErr: true},
		// The generated RPC client returns (nil, nil) on transport failure
		// (it logs and swallows the error); that must surface as an error so
		// the checkers deny, never as a dereference or a silent outcome.
		{name: "transport failure (nil, nil) returns error", wantErr: true},
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
