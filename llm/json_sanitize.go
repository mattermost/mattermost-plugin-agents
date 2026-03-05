// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "strings"

// StripMarkdownCodeFencing removes markdown code block fencing (e.g. ```json ... ```)
// that LLMs sometimes wrap around JSON responses despite being instructed not to.
func StripMarkdownCodeFencing(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return s
	}
	// Remove opening ``` prefix (and optional language tag like "json", "javascript", etc.)
	content := strings.TrimPrefix(trimmed, "```")
	if firstNewline := strings.Index(content, "\n"); firstNewline != -1 {
		content = content[firstNewline+1:]
	} else {
		// Single-line fenced payload, e.g. ```json {"a":1}```
		content = strings.TrimSpace(content)
		if spaceIdx := strings.IndexAny(content, " \t"); spaceIdx != -1 {
			if isLanguageTag(content[:spaceIdx]) {
				content = strings.TrimSpace(content[spaceIdx:])
			}
		}
	}

	// Remove closing fence
	if idx := strings.LastIndex(content, "```"); idx != -1 {
		content = content[:idx]
	}
	return strings.TrimSpace(content)
}

// isLanguageTag returns true if s looks like a markdown code fence language tag
// (e.g. "json", "javascript", "go", "c++", "c-sharp").
func isLanguageTag(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		isSpecial := r == '-' || r == '_' || r == '+'
		if !isLetter && !isDigit && !isSpecial {
			return false
		}
	}
	return true
}
