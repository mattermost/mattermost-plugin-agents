// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"context"

	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
)

// RebuildIndex reconciles the fail-closed policy index against server truth
// for every provided resource ID: GetAccessControlPolicy per resource, marker
// ensured when a policy exists and dropped when the server reports 404. Run
// on plugin activation (best-effort, non-fatal) so index divergence caused by
// a lost mutex lease (see Checker.reconcileIndex) never survives a restart.
//
// resourceIDsByType maps ResourceTypeAgent/Service/MCP to all resource IDs
// known to the plugin at the time of the call. IDs that are not
// policy-addressable (non-26-char config bot IDs) are skipped. Markers for
// resource IDs no longer enumerated (deleted resources) are left alone: they
// can only fail closed for resources that no longer resolve anyway.
func (c *Checker) RebuildIndex(ctx context.Context, resourceIDsByType map[string][]string) {
	_, span := telemetry.Tracer().Start(ctx, "abac rebuild_index")
	defer span.End()

	if c.papi == nil {
		return
	}

	c.mutationMu.Lock()
	defer c.mutationMu.Unlock()

	var healed, failed int
	for _, resourceType := range []string{ResourceTypeAgent, ResourceTypeService, ResourceTypeMCP} {
		for _, resourceID := range resourceIDsByType[resourceType] {
			if !model.IsValidId(resourceID) {
				continue
			}

			_, appErr := c.papi.GetAccessControlPolicy(resourceID)
			var wantMarker bool
			switch {
			case appErr == nil:
				wantMarker = true
			case isNotFoundAppErr(appErr):
				wantMarker = false
			default:
				// Debug, not Warn: on servers without ABAC every Get can fail
				// and per-resource warnings would spam each activation.
				failed++
				logDebug(c.log, "ABAC index rebuild could not read the policy for a resource; leaving its marker unchanged",
					"resource_type", resourceType, "resource_id", resourceID, "error", appErr.Error())
				continue
			}

			has, err := c.index.Has(resourceType, resourceID)
			if err != nil {
				failed++
				logWarn(c.log, "ABAC index rebuild could not read the policy index",
					"resource_type", resourceType, "resource_id", resourceID, "error", err.Error())
				continue
			}
			if has == wantMarker {
				continue
			}

			if wantMarker {
				err = c.index.Add(resourceType, resourceID)
			} else {
				err = c.index.Remove(resourceType, resourceID)
			}
			if err != nil {
				failed++
				logWarn(c.log, "ABAC index rebuild failed to update a marker",
					"resource_type", resourceType, "resource_id", resourceID, "want_marker", wantMarker, "error", err.Error())
				continue
			}
			healed++
			logWarn(c.log, "ABAC index rebuild healed a divergent marker",
				"resource_type", resourceType, "resource_id", resourceID, "want_marker", wantMarker)
		}
	}

	if healed > 0 {
		logWarn(c.log, "ABAC policy index rebuild healed divergent markers", "healed", healed, "unreadable", failed)
		return
	}
	logDebug(c.log, "ABAC policy index rebuild found no divergences", "unreadable", failed)
}
