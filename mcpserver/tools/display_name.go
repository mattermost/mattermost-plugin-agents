// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"strings"
	"unicode"

	"github.com/mattermost/mattermost/server/public/model"
)

// channelDisplayNameOrFallback returns the channel's display name, or the
// placeholder get_channel_info reports when it is empty (usual for direct
// messages): "Direct Message", "Group Message", or the channel name.
func channelDisplayNameOrFallback(channel *model.Channel) string {
	if channel.DisplayName != "" {
		return channel.DisplayName
	}
	switch channel.Type {
	case model.ChannelTypeDirect:
		return "Direct Message"
	case model.ChannelTypeGroup:
		return "Group Message"
	default:
		return channel.Name
	}
}

// displayNamesMatch reports whether a display name supplied by the model
// refers to the actual one, ignoring cosmetic differences: emoji and other
// pictographic symbols (with their variation selectors, skin-tone modifiers,
// zero-width joiners and tag sequences), case, and surrounding or repeated
// whitespace. Names that differ in any other way do not match. Names made up
// solely of symbols are compared exactly, so two different all-emoji names
// never collapse into a match.
func displayNamesMatch(provided, actual string) bool {
	if provided == actual {
		return true
	}
	normalizedProvided := normalizeDisplayName(provided)
	normalizedActual := normalizeDisplayName(actual)
	if normalizedProvided == "" || normalizedActual == "" {
		return false
	}
	return normalizedProvided == normalizedActual
}

// normalizeDisplayName strips decorative runes, collapses whitespace and
// lower-cases a display name for comparison.
func normalizeDisplayName(name string) string {
	stripped := strings.Map(func(r rune) rune {
		if isDecorativeRune(r) {
			return -1
		}
		return r
	}, name)
	return strings.ToLower(strings.Join(strings.Fields(stripped), " "))
}

// isDecorativeRune reports whether r is an emoji or pictograph, or a rune that
// only modifies one: variation selectors, the zero-width joiner, the
// combining enclosing keycap, skin-tone modifiers and emoji tag characters.
func isDecorativeRune(r rune) bool {
	switch {
	case r == '\u200D', r == '\u20E3':
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF:
		return true
	case r >= 0x1F3FB && r <= 0x1F3FF:
		return true
	case r >= 0xE0020 && r <= 0xE007F:
		return true
	}
	return unicode.Is(unicode.So, r)
}
