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
	noPolicy := model.NewNoPolicyAccessDecision()

	tests := []struct {
		name     string
		decision *model.AccessDecision
		appErr   *model.AppError

		wantErr error
		// wantAttribute is the value reported on the ABACOutcome span
		// attribute, which keeps allow/deny/no_policy apart even though the
		// AuthZEN decision folds them into one boolean.
		wantAttribute string
		wantNoPolicy  bool
	}{
		{
			name:          "allow",
			decision:      &model.AccessDecision{Decision: true},
			wantAttribute: "allow",
		},
		{
			name:          "deny",
			decision:      &model.AccessDecision{Decision: false},
			wantAttribute: "deny",
		},
		{
			name:          "no_policy",
			decision:      &noPolicy,
			wantAttribute: "no_policy",
			wantNoPolicy:  true,
		},
		{
			// A reason this plugin does not recognize does not weaken the
			// allow it annotates; only the no_policy reason makes one vacuous.
			name: "allow carrying an unrecognized reason is still an allow",
			decision: &model.AccessDecision{
				Decision: true,
				Context:  map[string]any{model.AccessDecisionContextKeyReason: "future_reason"},
			},
			wantAttribute: "allow",
		},
		{
			// A deny labelled no_policy is self-contradictory; it must not be
			// mistaken for the unregulated case, which fails open.
			name: "deny contradicting the no_policy reason stays a deny",
			decision: &model.AccessDecision{
				Decision: false,
				Context:  map[string]any{model.AccessDecisionContextKeyReason: string(model.AccessDecisionReasonNoPolicy)},
			},
			wantAttribute: "deny",
		},
		{
			name:    "evaluation failure",
			appErr:  model.NewAppError("EvaluateAccessControl", "boom", nil, "", http.StatusInternalServerError),
			wantErr: model.NewAppError("EvaluateAccessControl", "boom", nil, "", http.StatusInternalServerError),
		},
		{
			// Unavailability is no longer a decision value: an open-core,
			// unlicensed, or disabled server reports it as an error.
			name:    "unavailable PDP arrives as an error",
			appErr:  model.NewAppError("EvaluateAccessControl", "unavailable", nil, "", http.StatusServiceUnavailable),
			wantErr: model.NewAppError("EvaluateAccessControl", "unavailable", nil, "", http.StatusServiceUnavailable),
		},
		{
			// (nil, nil) is the RPC client's silent transport failure; it must
			// surface as an error so the checkers deny.
			name:    "transport failure (nil, nil) returns an error",
			wantErr: errRPCTransportFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &plugintest.API{}
			defer api.AssertExpectations(t)
			api.On("EvaluateAccessControl", "userid", ResourceTypeAgent, "resourceid", ActionUse).
				Return(tt.decision, tt.appErr).Once()

			client := NewPluginAPIClient(api)
			decision, err := client.EvaluateAccessRequest(context.Background(), "userid", ResourceTypeAgent, "resourceid", ActionUse)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err)
				assert.Nil(t, decision)
				return
			}
			require.NoError(t, err)
			// The server's decision is returned verbatim.
			require.Equal(t, tt.decision, decision)
			assert.Equal(t, tt.wantNoPolicy, decision.IsNoPolicy())
			assert.Equal(t, tt.wantAttribute, outcomeAttribute(*decision))
		})
	}
}
