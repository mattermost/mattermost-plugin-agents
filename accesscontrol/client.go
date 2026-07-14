// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// Plugin-local names for the server-registered resource types / action
// (contract §1.1). Sourced from model so a server-side rename is a compile
// error here rather than a silent policy mismatch.
const (
	ResourceTypeAgent   = model.AccessControlPolicyTypePluginAgent
	ResourceTypeService = model.AccessControlPolicyTypePluginService
	ResourceTypeMCP     = model.AccessControlPolicyTypePluginMCP
	ActionUse           = model.AccessControlPolicyActionUse
)

// DecisionClient abstracts the PDP call (plugin.API.EvaluateAccessControl in
// WS-D) so Checker logic is testable against a stub.
type DecisionClient interface {
	EvaluateAccessRequest(ctx context.Context, userID, resourceType, resourceID, action string) (Outcome, error)
}
