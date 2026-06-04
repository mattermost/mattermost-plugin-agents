// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePrompt_NonEmptyASCII(t *testing.T) {
	for _, prof := range []string{"mixed", "read_search_heavy", "short", "tool_heavy", "unknown_profile"} {
		for _, mode := range []TriggerMode{TriggerModeBoth, TriggerModeDM, TriggerModeChannelMention} {
			for n := int64(0); n < 5; n++ {
				s := GeneratePrompt(prof, mode, n)
				require.NotEmpty(t, s)
				for _, r := range s {
					assert.True(t, r <= unicode.MaxASCII, "non-ASCII in %q", s)
				}
			}
		}
	}
}

func TestGeneratePrompt_VariesByProfileAndCounter(t *testing.T) {
	a := GeneratePrompt("mixed", TriggerModeBoth, 1)
	b := GeneratePrompt("read_search_heavy", TriggerModeBoth, 1)
	c := GeneratePrompt("mixed", TriggerModeBoth, 2)
	assert.NotEqual(t, a, b)
	assert.NotEqual(t, a, c)
}

func TestGeneratePrompt_ModeRotatesTemplate(t *testing.T) {
	x := GeneratePrompt("mixed", TriggerModeChannelMention, 0)
	y := GeneratePrompt("mixed", TriggerModeDM, 0)
	assert.NotEqual(t, x, y)
}
