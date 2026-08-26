// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package loadtest

import (
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/v2/loadtest/profile"
)

// The mock profile implementation lives in loadtest/profile so that llm can
// validate raw profile JSON without importing this package (which imports
// llm). The aliases below keep the profile types usable under their loadtest
// names alongside MockLLM.

// MockProfile configures load-test LLM behavior.
type MockProfile = profile.MockProfile

// LatencyProfile describes one named latency mix for mock streaming.
type LatencyProfile = profile.LatencyProfile

// ToolArgumentProfile holds optional discrete values for argument generation per tool.
type ToolArgumentProfile = profile.ToolArgumentProfile

// ParseProfile merges operator JSON on top of the default profile. Nil, empty, or whitespace-only raw returns the default.
func ParseProfile(raw json.RawMessage) (MockProfile, error) {
	return profile.Parse(raw)
}

// DefaultReadSearchHeavyProfile returns the documented empirical defaults for read/search-heavy load tests.
func DefaultReadSearchHeavyProfile() MockProfile {
	return profile.DefaultReadSearchHeavyProfile()
}
