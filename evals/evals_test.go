// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package evals

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadThreadFromJSON(t *testing.T) {
	evalT := &EvalT{T: t}
	path := filepath.Join("..", "threads", "eval_timed_dnd.json")

	threadData := LoadThreadFromJSON(evalT, path)

	// Validate thread data fields
	assert.NotNil(t, threadData.RootPost, "RootPost should not be nil")
	assert.NotNil(t, threadData.PostList, "PostList should not be nil")
	assert.NotNil(t, threadData.Channel, "Channel should not be nil")
	assert.NotNil(t, threadData.Team, "Team should not be nil")
	assert.NotEmpty(t, threadData.Users, "Users should not be empty")

	// Check for specific expected data based on known content
	assert.Equal(t, "17bfnb1uwb8epewp4q3x3rx9go", threadData.Channel.Id)
	assert.Equal(t, "Developers: Server", threadData.Channel.DisplayName)
	assert.Equal(t, "rcgiyftm7jyrxnma1osd8zswby", threadData.Team.Id)
	assert.Equal(t, "Contributors", threadData.Team.DisplayName)

	// Validate user data
	require.Contains(t, threadData.Users, "guk95b7obfrq7b995jcxaknxga")
	assert.Equal(t, "harrison", threadData.Users["guk95b7obfrq7b995jcxaknxga"].Username)
}

func TestIsTransientProviderError(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"rate limit", "bifrost error: Rate limit exceeded (status=429 type=rate_limited code=1300 provider=mistral)", true},
		{"server error", "bifrost error: upstream unavailable (status=503 provider=openai)", true},
		{"timeout", "bifrost error: context deadline exceeded", true},
		{"rejected credentials", "bifrost error: all configured keys returned permanent per-key errors (401/402/403) (status=502 type=upstream_credentials_exhausted provider=openai)", false},
		{"exhausted quota", "bifrost error: You exceeded your current quota (status=429 type=insufficient_quota code=insufficient_quota provider=openai)", false},
		{"missing deployment", "bifrost error: The API deployment for this resource does not exist. (status=404 type=invalid_request_error code=DeploymentNotFound provider=azure)", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTransientProviderError(errors.New(tc.msg)))
		})
	}
}
