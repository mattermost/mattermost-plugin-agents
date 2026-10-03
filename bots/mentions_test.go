// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserIsMentionedMarkdown(t *testing.T) {
	testCases := []struct {
		name         string
		text         string
		wasMentioned bool
	}{
		{
			name:         "user is mentioned",
			text:         "Hello @ai",
			wasMentioned: true,
		},
		{
			name:         "user is mentioned case-insensitively",
			text:         "Hello @AI",
			wasMentioned: true,
		},
		{
			name:         "user is not mentioned",
			text:         "Hello @somoneelse",
			wasMentioned: false,
		},
		{
			name:         "dots don't count",
			text:         "End of sentance @ai.",
			wasMentioned: true,
		},
		{
			name:         "Not somone else",
			text:         "This is @aisomoneelse",
			wasMentioned: false,
		},
		{
			name:         "Not somone else",
			text:         "This is @aisomoneelse.",
			wasMentioned: false,
		},
		{
			name:         "not a mention",
			text:         "This is ai",
			wasMentioned: false,
		},
		{
			name:         "not a mention",
			text:         "This is ai.",
			wasMentioned: false,
		},
		{
			name:         "not a mention",
			text:         "This is :ai:",
			wasMentioned: false,
		},
		{
			name:         "inline code block",
			text:         "This is a `code mention @ai hey`",
			wasMentioned: false,
		},
		{
			name:         "code block",
			text:         "This is a \n```\ncode mention @ai hey\n```",
			wasMentioned: false,
		},
		{
			name:         "code block with actual mention",
			text:         "This is @ai \n```\ncode mention @blue hey\n```",
			wasMentioned: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wasMentioned, userIsMentionedMarkdown(tc.text, "ai"))
		})
	}
}
