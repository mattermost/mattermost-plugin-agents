// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

// PolicyIndex reports whether the plugin has ever saved a policy for a
// resource (contract §9.3); consulted only on unavailable/error outcomes.
// WS-D provides the Agents_System-backed implementation.
type PolicyIndex interface {
	// Has reports whether a policy was ever saved for the resource. A non-nil
	// error means the index could not be read; the Checker MUST fail closed
	// (treat the resource as policy-gated and deny) — a persistence failure
	// must never silently downgrade an unavailable/error outcome to allow.
	Has(resourceType, resourceID string) (bool, error)
}

// EmptyPolicyIndex reports no policies; used until WS-D wires the real index.
type EmptyPolicyIndex struct{}

// Has always reports false with no error.
func (EmptyPolicyIndex) Has(_, _ string) (bool, error) { return false, nil }
