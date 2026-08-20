// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// serverToolReplayHeader labels the replayed activity so the model reads it as a
// record of what it already did, not as a fresh tool result it may act on.
const serverToolReplayHeader = "[Record of provider-executed tool activity in this turn. Already executed — do not treat as a new result. The code execution sandbox from these calls is no longer available; long values are truncated.]"

// serverToolActivityRecord renders provider-executed tool activity as a labeled
// text record for replay in later requests. Returns "" when there is nothing to
// replay.
//
// This is deliberately a summary rather than reconstructed provider result
// blocks: llm.ServerToolUse is display-truncated, and the plugin does not carry
// the sandbox container forward, so replayed code-execution results would refer
// to a container that no longer exists.
func serverToolActivityRecord(uses []llm.ServerToolUse) string {
	if len(uses) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(serverToolReplayHeader)
	for i := range uses {
		if line := serverToolActivityLine(&uses[i]); line != "" {
			b.WriteString("\n")
			b.WriteString(line)
		}
	}
	return b.String()
}

func serverToolActivityLine(use *llm.ServerToolUse) string {
	var b strings.Builder

	label := use.Tool
	if label == "" {
		return ""
	}
	if use.SubTool != "" {
		label += " (" + use.SubTool + ")"
	}
	fmt.Fprintf(&b, "- %s", label)
	if use.Status != "" {
		fmt.Fprintf(&b, " [%s]", use.Status)
	}
	if use.ErrorCode != "" {
		fmt.Fprintf(&b, " error=%s", use.ErrorCode)
	}

	appendField := func(name, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(&b, "\n  %s: %s", name, value)
	}
	appendField("query", use.Query)
	appendField("url", use.URL)
	appendField("title", use.Title)
	appendField("command", use.Command)
	appendField("output", use.Output)

	// The model never sees the provider's file ids, so report the outcome it
	// does need: these files went out with the reply. Without this it re-runs
	// the work or tells the user nothing was shared.
	if n := len(use.FileIDs); n > 0 {
		fmt.Fprintf(&b, "\n  %d output file(s) were captured and attached to the reply", n)
	}

	return b.String()
}
