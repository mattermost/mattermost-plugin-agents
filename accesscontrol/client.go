// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// PluginID is the owner segment of every policy type below. Must equal
// plugin.json's id — TestAccessControlPluginIDMatchesManifest pins that.
const PluginID = "mattermost-ai"

// Resource types compose against the platform's separator. There is no
// server-side registry; the server treats both segments opaquely.
const (
	ResourceTypeAgent   = PluginID + model.PluginAccessControlPolicyTypeSeparator + "agent"
	ResourceTypeService = PluginID + model.PluginAccessControlPolicyTypeSeparator + "service"
	ResourceTypeMCP     = PluginID + model.PluginAccessControlPolicyTypeSeparator + "mcp"

	// ActionUse is plugin-defined; the server validates an action's format,
	// not its meaning.
	ActionUse = "use"
)

// DecisionClient abstracts plugin.API.EvaluateAccessControl so Checker logic is testable.
type DecisionClient interface {
	EvaluateAccessRequest(ctx context.Context, userID, resourceType, resourceID, action string) (*model.AccessDecision, error)
}
