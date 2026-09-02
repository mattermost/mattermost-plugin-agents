// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// serverToolReplayHeader labels replay so the model treats it as already-done
// work, not a fresh tool result.
const serverToolReplayHeader = "[Record of provider-executed tool activity in this turn. Already executed — do not treat as a new result. The code execution sandbox from these calls is no longer available; long values are truncated.]"

// serverToolActivityRecord is a summary, not reconstructed provider blocks:
// fields are truncated and the sandbox container is gone.
func serverToolActivityRecord(uses []llm.ServerToolUse) string {
	lines := make([]string, 0, len(uses))
	for i := range uses {
		if line := serverToolActivityLine(&uses[i]); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return serverToolReplayHeader + "\n" + strings.Join(lines, "\n")
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

	// Do not claim upload success: download, permissions, or post limits can still reject them.
	if n := len(use.FileIDs); n > 0 {
		fmt.Fprintf(&b, "\n  %d output file(s) were captured for attachment to the reply", n)
	}

	return b.String()
}
