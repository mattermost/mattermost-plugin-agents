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
	// Remove opening ``` prefix (and optional language tag like "json")
	content := strings.TrimPrefix(trimmed, "```")
	if firstNewline := strings.Index(content, "\n"); firstNewline != -1 {
		content = content[firstNewline+1:]
	} else {
		// Single-line fenced payload, e.g. ```json {"a":1}```
		content = strings.TrimSpace(content)
		if len(content) >= 4 && strings.EqualFold(content[:4], "json") {
			if len(content) == 4 || content[4] == ' ' || content[4] == '\t' {
				content = strings.TrimSpace(content[4:])
			}
		}
	}

	// Remove closing fence
	if idx := strings.LastIndex(content, "```"); idx != -1 {
		content = content[:idx]
	}
	return strings.TrimSpace(content)
}
