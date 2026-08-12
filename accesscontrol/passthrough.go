// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// PassthroughClient is a decision client that reports no_policy for every
// resource. NOT safe as a stand-in on servers without ABAC support: no_policy
// lets attribute-based agents fail open, which is only sound when the server
// really resolved policy existence. Pre-11.10 wiring must use NewLegacyOnly;
// this client remains for tests.
type PassthroughClient struct{}

// EvaluateAccessRequest always reports no_policy.
func (PassthroughClient) EvaluateAccessRequest(_ context.Context, _, _, _, _ string) (*model.AccessDecision, error) {
	decision := model.NewNoPolicyAccessDecision()
	return &decision, nil
}
