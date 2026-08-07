// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type diffFixture struct {
	Name    string   `json:"name"`
	Secret  string   `json:"secret"`
	Tags    []string `json:"tags,omitempty"`
	Enabled bool     `json:"enabled"`
}

func TestChangedJSONKeys(t *testing.T) {
	tests := []struct {
		name     string
		prev     any
		next     any
		expected []string
	}{
		{
			name:     "identical values report nothing",
			prev:     &diffFixture{Name: "a", Secret: "s"},
			next:     diffFixture{Name: "a", Secret: "s"},
			expected: []string{},
		},
		{
			name:     "only differing keys are reported, sorted",
			prev:     &diffFixture{Name: "a", Secret: "old"},
			next:     diffFixture{Name: "b", Secret: "new", Enabled: true},
			expected: []string{"enabled", "name", "secret"},
		},
		{
			name:     "clearing an omitempty field is reported",
			prev:     &diffFixture{Name: "a", Tags: []string{"x"}},
			next:     diffFixture{Name: "a"},
			expected: []string{"tags"},
		},
		{
			name:     "nil previous reports every key of the new value",
			prev:     (*diffFixture)(nil),
			next:     diffFixture{},
			expected: []string{"enabled", "name", "secret"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChangedJSONKeys(tt.prev, tt.next)
			assert.Equal(t, tt.expected, got)

			// The invariant this helper exists for: key names only.
			for _, key := range got {
				assert.NotContains(t, key, "old")
				assert.NotContains(t, key, "new")
			}
		})
	}
}
