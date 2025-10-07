// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAccessModeConstants(t *testing.T) {
	// Test that the constants have the expected string values
	assert.Equal(t, "local", string(AccessModeLocal))
	assert.Equal(t, "remote", string(AccessModeRemote))
}

func TestAccessModeStringComparisons(t *testing.T) {
	// Test that AccessMode can be compared with strings
	testCases := []struct {
		name        string
		mode        AccessMode
		stringVal   string
		shouldMatch bool
	}{
		{
			name:        "local mode matches local string",
			mode:        AccessModeLocal,
			stringVal:   "local",
			shouldMatch: true,
		},
		{
			name:        "remote mode matches remote string",
			mode:        AccessModeRemote,
			stringVal:   "remote",
			shouldMatch: true,
		},
		{
			name:        "local mode does not match remote string",
			mode:        AccessModeLocal,
			stringVal:   "remote",
			shouldMatch: false,
		},
		{
			name:        "remote mode does not match local string",
			mode:        AccessModeRemote,
			stringVal:   "local",
			shouldMatch: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := string(tc.mode) == tc.stringVal
			assert.Equal(t, tc.shouldMatch, result)
		})
	}
}
