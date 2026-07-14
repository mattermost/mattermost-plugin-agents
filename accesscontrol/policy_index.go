// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

// PolicyIndex reports whether the plugin has ever saved a policy for a
// resource (contract §9.3); consulted only on unavailable/error outcomes.
// WS-D provides the Agents_System-backed implementation.
type PolicyIndex interface {
	Has(resourceType, resourceID string) bool
}

// EmptyPolicyIndex reports no policies; used until WS-D wires the real index.
type EmptyPolicyIndex struct{}

// Has always reports false.
func (EmptyPolicyIndex) Has(_, _ string) bool { return false }
