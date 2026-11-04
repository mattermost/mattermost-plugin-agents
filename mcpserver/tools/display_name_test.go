// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDisplayNamesMatch(t *testing.T) {
	tests := []struct {
		name     string
		provided string
		actual   string
		want     bool
	}{
		{name: "identical", provided: "Town Square", actual: "Town Square", want: true},
		{name: "ZWJ emoji prefix", provided: "Company KPI Hallway", actual: "🚶‍♀️Company KPI Hallway", want: true},
		{name: "emoji prefix with space", provided: "Releases", actual: "🚀 Releases", want: true},
		{name: "emoji suffix", provided: "Releases", actual: "Releases 🎉", want: true},
		{name: "emoji in the middle", provided: "Dev Ops", actual: "Dev 🔧 Ops", want: true},
		{name: "skin tone modifier", provided: "Welcome", actual: "👋🏽 Welcome", want: true},
		{name: "flag sequence", provided: "Canada Team", actual: "🇨🇦 Canada Team", want: true},
		{name: "keycap digit stays significant", provided: "Priority", actual: "1️⃣ Priority", want: false},
		{name: "provided has emoji, actual does not", provided: "🚶‍♀️ Company KPI Hallway", actual: "Company KPI Hallway", want: true},
		{name: "case difference", provided: "town square", actual: "Town Square", want: true},
		{name: "extra and surrounding whitespace", provided: "  Town   Square ", actual: "Town Square", want: true},
		{name: "different team name", provided: "Mattermost", actual: "Staff", want: false},
		{name: "different channel name with same emoji", provided: "🚶‍♀️Company KPI Lobby", actual: "🚶‍♀️Company KPI Hallway", want: false},
		{name: "extra word", provided: "Company KPI Hallway 2", actual: "🚶‍♀️Company KPI Hallway", want: false},
		{name: "punctuation is significant", provided: "dev-ops", actual: "dev ops", want: false},
		{name: "different all-emoji names", provided: "🚀", actual: "🎉", want: false},
		{name: "empty provided against emoji-only name", provided: "", actual: "🚀", want: false},
		{name: "emoji-only provided against text name", provided: "🚀", actual: "Releases", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, displayNamesMatch(tt.provided, tt.actual))
		})
	}
}
