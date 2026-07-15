// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// PassthroughClient is the pre-ABAC decision client: it reports that no
// policy exists for every resource, which reproduces legacy behavior exactly
// under the contract §9.2 decision tables.
type PassthroughClient struct{}

// EvaluateAccessRequest always reports no_policy.
func (PassthroughClient) EvaluateAccessRequest(_ context.Context, _, _, _, _ string) (model.AccessDecisionOutcome, error) {
	return model.AccessDecisionOutcomeNoPolicy, nil
}
