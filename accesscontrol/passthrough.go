// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// PassthroughClient reports no_policy for every resource. Not a production
// stand-in: no_policy lets attribute-based agents fail open, which is only
// sound when the server really resolved policy existence.
type PassthroughClient struct{}

func (PassthroughClient) EvaluateAccessRequest(_ context.Context, _, _, _, _ string) (*model.AccessDecision, error) {
	decision := model.NewNoPolicyAccessDecision()
	return &decision, nil
}
