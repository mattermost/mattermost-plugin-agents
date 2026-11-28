// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// DictationPromptMaxChars keeps the prompt under OpenAI's limit of 1024
	// characters; a longer prompt fails the whole session.
	DictationPromptMaxChars = 1000
	// dictationNameMaxRunes cuts a single name or channel name.
	dictationNameMaxRunes = 64
)

// DictationContext is the prompt and keywords a transcription session gets
// for the conversation the user dictates into.
type DictationContext struct {
	Prompt   string
	Keywords []string
}

// DictationUserName is the name people are likely to say: the full name when
// the server shows full names, else the nickname, else the username.
func DictationUserName(user *model.User, showFullName bool) string {
	if showFullName {
		if name := strings.TrimSpace(user.GetFullName()); name != "" {
			return name
		}
	}
	if user.Nickname != "" {
		return user.Nickname
	}
	return user.Username
}

// DictationChannelContext builds the transcription hints from a channel name
// (empty for direct and group messages) and people's names in priority
// order. Names are cleaned, deduplicated and added until the prompt reaches
// DictationPromptMaxChars; the first name that doesn't fit ends the list, so
// a cut keeps the names that come first. Keywords are the channel name and
// the names that made it into the prompt.
func DictationChannelContext(channelName string, names []string) DictationContext {
	channelName = sanitizeDictationName(channelName)
	var prompt strings.Builder
	if channelName != "" {
		prompt.WriteString(`Dictation in the Mattermost channel "` + channelName + `".`)
	} else {
		prompt.WriteString("Dictation in a Mattermost conversation.")
	}
	var keywords []string
	if channelName != "" {
		keywords = append(keywords, channelName)
	}

	const peopleLabel = " People in this conversation: "
	seen := map[string]bool{}
	var people []string
	length := utf16Len(prompt.String()) + utf16Len(peopleLabel) + 1 // closing period
	for _, raw := range names {
		name := sanitizeDictationName(raw)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		added := utf16Len(name)
		if len(people) > 0 {
			added += 2 // ", "
		}
		if length+added > DictationPromptMaxChars {
			break
		}
		seen[key] = true
		length += added
		people = append(people, name)
	}

	if len(people) == 0 && channelName == "" {
		return DictationContext{}
	}
	if len(people) > 0 {
		prompt.WriteString(peopleLabel + strings.Join(people, ", ") + ".")
	}
	for _, name := range people {
		if !strings.EqualFold(name, channelName) {
			keywords = append(keywords, name)
		}
	}
	return DictationContext{Prompt: prompt.String(), Keywords: keywords}
}

// sanitizeDictationName keeps a name on one line and inside the prompt's
// quotes: OpenAI rejects keywords with angle brackets or line breaks, so
// those, quotes, and control and format characters are dropped, whitespace
// is collapsed, and long names are cut.
func sanitizeDictationName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r == '<' || r == '>' || r == '"':
			return -1
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, name)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if runes := []rune(cleaned); len(runes) > dictationNameMaxRunes {
		cleaned = strings.TrimSpace(string(runes[:dictationNameMaxRunes]))
	}
	return cleaned
}

// utf16Len counts characters the way the provider's limit may: characters
// outside the Basic Multilingual Plane count twice.
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}
