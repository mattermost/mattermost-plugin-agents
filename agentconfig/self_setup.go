// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentconfig

import (
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// SelfSetupFields are the BotConfig JSON keys a self_setup write may change.
// They are a subset of what any agent manager may change while service
// account auth is on (ClearManagerEditableFields), so an agent configuring
// itself never reaches access lists, service account grants, its service,
// or its model. Tool fields may only be narrowed.
var SelfSetupFields = []string{
	"customInstructions",
	"disableTools",
	"displayName",
	"enabledMCPTools",
	"enabledNativeTools",
}

// checkSelfSetupChange enforces SelfSetupFields on a self_setup write. It is
// enforced by the Service, not the Authorizer, so no authorizer can widen it.
func checkSelfSetupChange(stored, proposed *llm.BotConfig) error {
	for _, key := range audit.ChangedJSONKeys(stored, proposed) {
		if !slices.Contains(SelfSetupFields, key) {
			return ErrSelfSetupFieldNotAllowed
		}
	}
	if stored.DisableTools && !proposed.DisableTools {
		return ErrSelfSetupFieldNotAllowed
	}
	if !isSubset(proposed.EnabledNativeTools, stored.EnabledNativeTools) {
		return ErrSelfSetupFieldNotAllowed
	}
	if !slices.Equal(stored.EnabledMCPTools, proposed.EnabledMCPTools) {
		// With AutoEnableNewMCPTools the list is ignored, so editing it could
		// only ever mean switching to an explicit grant.
		if stored.AutoEnableNewMCPTools || !isSubset(proposed.EnabledMCPTools, stored.EnabledMCPTools) {
			return ErrSelfSetupFieldNotAllowed
		}
	}
	return nil
}

func isSubset[T comparable](sub, set []T) bool {
	for _, v := range sub {
		if !slices.Contains(set, v) {
			return false
		}
	}
	return true
}
