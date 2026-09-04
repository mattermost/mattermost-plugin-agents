// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// PassthroughClient reports no_policy for every resource. Tests that need an
// explicit grant must stub DecisionClient; attribute-based agents deny on
// no_policy. Services and MCP servers stay unrestricted, matching production
// when no policy exists.
type PassthroughClient struct{}

func (PassthroughClient) EvaluateAccessRequest(_ context.Context, _, _, _, _ string) (*model.AccessDecision, error) {
	decision := model.NewNoPolicyAccessDecision()
	return &decision, nil
}
