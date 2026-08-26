// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
	"fmt"
	"strings"
)

// PromptProfile selects prompt templates for load tests.
type PromptProfile string

const (
	PromptProfileMixed           PromptProfile = "mixed"
	PromptProfileReadSearchHeavy PromptProfile = "read_search_heavy"
	PromptProfileShort           PromptProfile = "short"
	PromptProfileToolHeavy       PromptProfile = "tool_heavy"
)

// GeneratePrompt returns a deterministic ASCII-only user message for the given profile,
// trigger mode (for template variety), and sequence counter n.
func GeneratePrompt(profile string, mode TriggerMode, n int64) string {
	p := PromptProfile(strings.TrimSpace(profile))
	if p == "" {
		p = PromptProfileMixed
	}

	// Mode rotates template family slightly so channel vs DM exercises different phrasing.
	modeBias := int64(0)
	switch mode {
	case TriggerModeChannelMention:
		modeBias = 1
	case TriggerModeDM:
		modeBias = 2
	case TriggerModeBoth:
		modeBias = 3
	}

	seq := n + modeBias

	switch p {
	case PromptProfileReadSearchHeavy:
		return fmt.Sprintf("%s [%d]", pickTemplate(readSearchHeavyTemplates, seq), n)
	case PromptProfileShort:
		return fmt.Sprintf("Quick check: summarize the last few posts in this channel. n=%d", n)
	case PromptProfileToolHeavy:
		return fmt.Sprintf("%s <%d>", pickTemplate(toolHeavyTemplates, seq), n)
	case PromptProfileMixed:
		fallthrough
	default:
		return fmt.Sprintf("%s #%d", pickTemplate(mixedTemplates, seq), n)
	}
}

// pickTemplate selects a template by sequence number, wrapping around the slice.
func pickTemplate(templates []string, seq int64) string {
	idx := int(seq % int64(len(templates)))
	if idx < 0 {
		idx = -idx
	}
	return templates[idx]
}

var mixedTemplates = []string{
	"Give a brief summary of recent discussion here.",
	"Search this workspace for onboarding docs and list two relevant threads.",
	"Who posted most in this channel lately? Name users if you can infer from context.",
	"What open questions remain from the last few messages?",
	"Find posts mentioning releases and summarize the timeline.",
	"Draft a short follow-up asking for a decision on the open topic.",
	"List three action items implied by recent messages.",
	"Compare two recent threads: what changed between them?",
}

var readSearchHeavyTemplates = []string{
	"Search for runbooks and summarize the top results.",
	"Read recent channel activity and extract key facts only.",
	"Look up the last incident thread and list resolution steps mentioned.",
	"Find discussions about performance; cite message themes, not internal IDs.",
	"Summarize search hits for the keyword rollout.",
	"Scan recent posts for blockers raised by the team.",
	"Identify who asked for help and what they needed.",
	"Produce a tight briefing from the latest twenty messages.",
}

var toolHeavyTemplates = []string{
	"Use search to find config changes, then suggest a verification checklist.",
	"Locate the latest design note and extract requirements as bullets.",
	"Find users discussing testing; summarize their concerns.",
	"Search for bugs filed this week and group by theme.",
	"Pull recent posts about migration and list risks mentioned.",
	"Look for SLA references in recent threads.",
	"Find onboarding questions and draft concise answers.",
	"Search for API mentions and summarize integration pitfalls.",
}
