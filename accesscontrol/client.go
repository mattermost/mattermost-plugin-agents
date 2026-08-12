// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// PluginID is the owner segment of every policy type below. The server keys
// plugin policies as "<pluginID>:<resourceType>" and enforces that the prefix
// matches the calling plugin, so this must equal plugin.json's id —
// TestAccessControlPluginIDMatchesManifest pins that.
const PluginID = "mattermost-ai"

// Resource types and the action these policies govern. There is no server-side
// registry of plugin resource types: the server treats both segments opaquely
// (format-checked only), so this package defines the plugin's policy
// namespace. The types compose against the platform's separator constant to
// keep the key shape compile-checked.
const (
	ResourceTypeAgent   = PluginID + model.PluginAccessControlPolicyTypeSeparator + "agent"
	ResourceTypeService = PluginID + model.PluginAccessControlPolicyTypeSeparator + "service"
	ResourceTypeMCP     = PluginID + model.PluginAccessControlPolicyTypeSeparator + "mcp"

	// ActionUse is plugin-defined; the server validates an action's format,
	// not its meaning.
	ActionUse = "use"
)

// DecisionClient abstracts the PDP call (plugin.API.EvaluateAccessControl)
// so Checker logic is testable against a stub.
type DecisionClient interface {
	EvaluateAccessRequest(ctx context.Context, userID, resourceType, resourceID, action string) (*model.AccessDecision, error)
}
