// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/bifrost"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// ServiceCanServeCompletions reports whether completions can be run against
// this service directly, without an agent in front of it.
//
// The service path has no agent to supply a model override, so a service
// without a default model cannot serve a completion at all. `scale` passes
// llm.IsValidService but has no Bifrost provider mapping, so it is excluded
// here even though it remains a valid stored configuration.
func ServiceCanServeCompletions(svc llm.ServiceConfig) bool {
	if !llm.IsValidService(svc) {
		return false
	}
	if svc.DefaultModel == "" {
		return false
	}
	return svc.Type == llm.ServiceTypeLoadTestMock || bifrost.IsSupported(svc.Type)
}

// ResolveBridgeFallbacks resolves the complete fallback chain of primary from
// the given service snapshot, and verifies that every fallback is itself able
// to serve a completion. It is kept separate from ServiceCanServeCompletions so
// a broken chain surfaces as a descriptive error instead of silently making the
// primary ineligible. llm.ResolveFallbackChain already rejects a member that
// fails llm.IsValidService, so the checks below only have to cover what
// ServiceCanServeCompletions adds on top of it.
//
// primary is expected to come from services, so the whole chain resolves from
// one consistent view. Duplicate service IDs in the snapshot resolve to the
// first entry, matching how the configuration itself is read.
func ResolveBridgeFallbacks(services []llm.ServiceConfig, primary llm.ServiceConfig) ([]llm.ServiceConfig, error) {
	lookup := llm.ServiceLookup(services)

	chain, err := llm.ResolveFallbackChain(primary.ID, lookup)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve fallback chain for service %q: %w", primary.ID, err)
	}

	for _, fallback := range chain {
		switch {
		case fallback.Type != llm.ServiceTypeLoadTestMock && !bifrost.IsSupported(fallback.Type):
			return nil, fmt.Errorf("fallback service %q in the chain of service %q has unsupported type %q", fallback.ID, primary.ID, fallback.Type)
		case fallback.DefaultModel == "":
			return nil, fmt.Errorf("fallback service %q in the chain of service %q has no default model", fallback.ID, primary.ID)
		}
	}

	return chain, nil
}
