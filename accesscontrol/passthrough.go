// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// PassthroughClient is a decision client that reports no_policy for every
// resource — as if an ABAC-capable server resolved that no policy exists
// anywhere. Note this is NOT safe as a stand-in on servers without ABAC
// support: under the §9.2 tables no_policy lets attribute-based agents fail
// open, which is only sound when the server really resolved policy
// existence. Production wiring for pre-11.10 servers must use NewLegacyOnly
// (which denies attribute-based agents) instead; this client remains for
// tests that want plain no-policies-anywhere decision behavior.
type PassthroughClient struct{}

// EvaluateAccessRequest always reports no_policy.
func (PassthroughClient) EvaluateAccessRequest(_ context.Context, _, _, _, _ string) (model.AccessDecisionOutcome, error) {
	return model.AccessDecisionOutcomeNoPolicy, nil
}
