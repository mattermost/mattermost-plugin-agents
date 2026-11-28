// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func TestDictationChannelContext(t *testing.T) {
	long := strings.Repeat("A", 40)
	var many []string
	for i := range 40 {
		many = append(many, long+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	tests := []struct {
		name         string
		channel      string
		names        []string
		wantPrompt   string
		wantKeywords []string
	}{
		{
			name:         "channel and people",
			channel:      "Dictation Team",
			names:        []string{"Søren Larsen", "Nnamdi Okafor"},
			wantPrompt:   `Dictation in the Mattermost channel "Dictation Team". People in this conversation: Søren Larsen, Nnamdi Okafor.`,
			wantKeywords: []string{"Dictation Team", "Søren Larsen", "Nnamdi Okafor"},
		},
		{
			name:         "no channel name",
			names:        []string{"Søren Larsen"},
			wantPrompt:   "Dictation in a Mattermost conversation. People in this conversation: Søren Larsen.",
			wantKeywords: []string{"Søren Larsen"},
		},
		{
			name:         "channel only",
			channel:      "Town Square",
			wantPrompt:   `Dictation in the Mattermost channel "Town Square".`,
			wantKeywords: []string{"Town Square"},
		},
		{
			name: "nothing to say",
		},
		{
			name:         "duplicates, empty names and a name equal to the channel",
			channel:      "Ines",
			names:        []string{"ines", "", "  ", "Søren", "SØREN"},
			wantPrompt:   `Dictation in the Mattermost channel "Ines". People in this conversation: ines, Søren.`,
			wantKeywords: []string{"Ines", "Søren"},
		},
		{
			name:         "markup, quotes, line breaks and control characters",
			channel:      "Q4 \"launch\"\n<plan>",
			names:        []string{"Eve\r\n\tAdams\u200b\x00", "<script>"},
			wantPrompt:   `Dictation in the Mattermost channel "Q4 launch plan". People in this conversation: Eve Adams, script.`,
			wantKeywords: []string{"Q4 launch plan", "Eve Adams", "script"},
		},
		{
			name:         "a long name is cut",
			names:        []string{strings.Repeat("x", 100)},
			wantPrompt:   "Dictation in a Mattermost conversation. People in this conversation: " + strings.Repeat("x", dictationNameMaxRunes) + ".",
			wantKeywords: []string{strings.Repeat("x", dictationNameMaxRunes)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DictationChannelContext(tt.channel, tt.names)
			assert.Equal(t, tt.wantPrompt, got.Prompt)
			assert.Equal(t, tt.wantKeywords, got.Keywords)
		})
	}

	t.Run("the budget keeps the first names and stays under the provider limit", func(t *testing.T) {
		got := DictationChannelContext("Team", many)
		assert.LessOrEqual(t, len(utf16.Encode([]rune(got.Prompt))), DictationPromptMaxChars)
		kept := got.Keywords[1:]
		assert.Less(t, len(kept), len(many))
		assert.Equal(t, many[:len(kept)], kept, "names are kept in order up to the first that doesn't fit")
	})

	t.Run("characters outside the BMP count twice", func(t *testing.T) {
		emoji := strings.Repeat("😀", 30) // 60 UTF-16 units, 30 runes
		var names []string
		for range 40 {
			names = append(names, emoji)
			emoji += "😀"
		}
		got := DictationChannelContext("", names)
		assert.LessOrEqual(t, len(utf16.Encode([]rune(got.Prompt))), DictationPromptMaxChars)
	})
}

func TestDictationUserName(t *testing.T) {
	tests := []struct {
		name         string
		user         *model.User
		showFullName bool
		want         string
	}{
		{name: "full name", user: &model.User{Username: "soren", FirstName: "Søren", LastName: "Larsen", Nickname: "S"}, showFullName: true, want: "Søren Larsen"},
		{name: "full names hidden: nickname", user: &model.User{Username: "soren", FirstName: "Søren", LastName: "Larsen", Nickname: "S"}, want: "S"},
		{name: "full names hidden: username", user: &model.User{Username: "soren", FirstName: "Søren", LastName: "Larsen"}, want: "soren"},
		{name: "no full name: nickname", user: &model.User{Username: "soren", Nickname: "S"}, showFullName: true, want: "S"},
		{name: "no full name or nickname", user: &model.User{Username: "soren"}, showFullName: true, want: "soren"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DictationUserName(tt.user, tt.showFullName))
		})
	}
}
