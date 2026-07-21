// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// ErrServiceIDConflict is the base error for every identity problem
// ReconcileServiceIDs can detect; callers map it to a client error (stale
// or corrupt admin console payload).
var ErrServiceIDConflict = errors.New("LLM service identity conflict")

// ReconcileServiceIDs carries stable IDs forward from prev onto ID-less
// entries in next (e.g. from webapp bundles or API automation predating the
// ID field). Service IDs are ABAC policy identities, so minting a fresh ID
// for an existing service silently detaches its policy (which fails open).
// Matching runs in global phases over the whole payload so the outcome cannot
// depend on payload order; anything suspicious errors:
//
//  1. Explicit-ID claims: each must reference a distinct existing prev entry.
//  2. Name claims against the unclaimed remainder: an ID-less entry claims
//     the single unclaimed prev entry with its Name; any ambiguity or
//     double-claim is an error.
//  3. Entries matching nothing stay ID-less; the caller mints a fresh ID.
//
// Each prev entry is claimed at most once across both phases.
func ReconcileServiceIDs(next []llm.ServiceConfig, prev []llm.ServiceConfig) ([]llm.ServiceConfig, error) {
	// Duplicate stored IDs mean the persisted config is already corrupt;
	// keeping the last row would let two services share one policy ID.
	prevByID := make(map[string]int, len(prev))
	for j := range prev {
		id := prev[j].ID
		if id == "" {
			continue
		}
		if _, dup := prevByID[id]; dup {
			return nil, fmt.Errorf("%w: stored configuration contains two services with ID %q", ErrServiceIDConflict, id)
		}
		prevByID[id] = j
	}

	claimed := make([]bool, len(prev))

	// Phase 1: entries arriving with an ID claim the prev entry holding it.
	seenIncoming := make(map[string]bool, len(next))
	for i := range next {
		id := next[i].ID
		if id == "" {
			continue
		}
		if seenIncoming[id] {
			return nil, fmt.Errorf("%w: service %q duplicates the ID of another entry in the payload", ErrServiceIDConflict, next[i].Name)
		}
		seenIncoming[id] = true
		j, ok := prevByID[id]
		if !ok {
			return nil, fmt.Errorf("%w: service %q carries ID %q which does not exist in the stored configuration", ErrServiceIDConflict, next[i].Name, id)
		}
		claimed[j] = true
	}

	// Phase 2: Name claims against the unclaimed remainder; every entry sees
	// the same post-phase-1 snapshot, so this phase is order-independent.
	nameClaimed := make(map[int]bool, len(prev))
	nameMatch := make([]int, len(next))
	for i := range next {
		nameMatch[i] = -1
		if next[i].ID != "" {
			continue
		}

		candidate := -1
		for j := range prev {
			if claimed[j] || prev[j].ID == "" || prev[j].Name != next[i].Name {
				continue
			}
			if candidate >= 0 {
				return nil, fmt.Errorf("%w: service %q ambiguously matches more than one stored service", ErrServiceIDConflict, next[i].Name)
			}
			candidate = j
		}
		if candidate < 0 {
			// Genuinely new: no identity claim to resolve.
			continue
		}
		if nameClaimed[candidate] {
			return nil, fmt.Errorf("%w: service %q claims a stored service another entry in the payload already claims", ErrServiceIDConflict, next[i].Name)
		}
		nameClaimed[candidate] = true
		nameMatch[i] = candidate
	}
	for i, j := range nameMatch {
		if j >= 0 {
			next[i].ID = prev[j].ID
		}
	}

	return next, nil
}
