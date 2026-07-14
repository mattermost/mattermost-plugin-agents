// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

// PolicyIndex records which resources the plugin has saved policies for
// (contract §9.3). Has is consulted only on unavailable/error outcomes;
// Add/Remove are called by the authoring routes on successful PUT/DELETE.
type PolicyIndex interface {
	// Has reports whether a policy was ever saved for the resource. A non-nil
	// error means the index could not be read; the Checker MUST fail closed
	// (treat the resource as policy-gated and deny) — a persistence failure
	// must never silently downgrade an unavailable/error outcome to allow.
	Has(resourceType, resourceID string) (bool, error)
	// Add records the resource as policy-gated. Idempotent.
	Add(resourceType, resourceID string) error
	// Remove clears the resource's policy-gated marker. Idempotent.
	Remove(resourceType, resourceID string) error
}

// EmptyPolicyIndex reports no policies; used in tests and passthrough wiring.
type EmptyPolicyIndex struct{}

// Has always reports false with no error.
func (EmptyPolicyIndex) Has(_, _ string) (bool, error) { return false, nil }

// Add is a no-op.
func (EmptyPolicyIndex) Add(_, _ string) error { return nil }

// Remove is a no-op.
func (EmptyPolicyIndex) Remove(_, _ string) error { return nil }
