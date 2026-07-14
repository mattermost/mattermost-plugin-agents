// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import "context"

// Plugin-local resource type / action names (contract §1.1); replaced by
// model.AccessControlPolicyTypePlugin* constants after the server/public bump.
const (
	ResourceTypeAgent   = "mattermost-ai.agent"
	ResourceTypeService = "mattermost-ai.service"
	ResourceTypeMCP     = "mattermost-ai.mcp"
	ActionUse           = "use"
)

// DecisionClient abstracts the PDP call (plugin.API.EvaluateAccessControl in
// WS-D) so Checker logic is testable against a stub.
type DecisionClient interface {
	EvaluateAccessRequest(ctx context.Context, userID, resourceType, resourceID, action string) (Outcome, error)
}
