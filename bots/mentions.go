// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"strings"
	"unicode"

	"github.com/mattermost/mattermost/server/public/shared/markdown"
)

// Adapted from https://github.com/mattermost/mattermost-server/blob/14fcc8a22e05efdb9e535e49257da80a9228507e/server/channels/app/notification.go#L1313
func userIsMentioned(text, botUsername string) bool {
	for _, word := range strings.FieldsFunc(text, func(c rune) bool {
		// Split on any whitespace or punctuation that can't be part of an at mention or emoji pattern
		return !unicode.IsLetter(c) && !unicode.IsNumber(c) && c != ':' && c != '.' && c != '-' && c != '_' && c != '@'
	}) {
		// skip word with format ':word:' with an assumption that it is an emoji format only
		if word[0] == ':' && word[len(word)-1] == ':' {
			continue
		}

		if strings.EqualFold(strings.Trim(word, ":.-_"), "@"+botUsername) {
			return true
		}
	}
	return false
}

func userIsMentionedMarkdown(text, botUsername string) bool {
	foundMention := false
	markdown.Inspect(text, func(node any) bool {
		block, ok := node.(*markdown.Text)
		if !ok {
			return true
		}
		if userIsMentioned(block.Text, botUsername) {
			foundMention = true
		}
		return false
	})

	return foundMention
}
