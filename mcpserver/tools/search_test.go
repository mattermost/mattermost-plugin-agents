// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import "testing"

func TestBuildSearchTermWithChannel(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		channelName string
		expected    string
	}{
		{
			name:        "simple query with channel name",
			query:       "bug fix",
			channelName: "town-square",
			expected:    "in:town-square bug fix",
		},
		{
			name:        "channel name with hyphens",
			query:       "release notes",
			channelName: "release-announcements-2024",
			expected:    "in:release-announcements-2024 release notes",
		},
		{
			name:        "query already containing in: modifier",
			query:       "in:other-channel error",
			channelName: "dev",
			expected:    "in:dev in:other-channel error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSearchTermWithChannel(tt.query, tt.channelName)
			if got != tt.expected {
				t.Errorf("buildSearchTermWithChannel(%q, %q) = %q, want %q", tt.query, tt.channelName, got, tt.expected)
			}
		})
	}
}
