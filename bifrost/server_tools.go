// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// Display caps for server-tool activity payloads. Sandbox code and output can
// be arbitrarily large; everything past these limits is elided before the
// event leaves the bifrost layer so websocket broadcasts and persisted turns
// stay bounded.
const (
	serverToolCommandMaxLen = 10000
	serverToolOutputMaxLen  = 4000
)

// serverToolTracker accumulates provider-executed (server) tool activity
// observed on a Responses stream, keyed by item id, preserving arrival order.
type serverToolTracker struct {
	order []string
	byID  map[string]*llm.ServerToolUse
}

func newServerToolTracker() *serverToolTracker {
	return &serverToolTracker{byID: make(map[string]*llm.ServerToolUse)}
}

// snapshot returns a copy of the accumulated activity in arrival order,
// suitable for emitting as an EventTypeServerToolUse value.
func (t *serverToolTracker) snapshot() []llm.ServerToolUse {
	out := make([]llm.ServerToolUse, 0, len(t.order))
	for _, id := range t.order {
		out = append(out, *t.byID[id])
	}
	return out
}

// upsert merges use into the tracked entry with the same ID, creating it when
// unseen. Non-empty incoming fields win; the entry's Status always follows the
// incoming value when set.
func (t *serverToolTracker) upsert(use llm.ServerToolUse) {
	if use.ID == "" {
		return
	}
	existing, ok := t.byID[use.ID]
	if !ok {
		u := use
		t.byID[use.ID] = &u
		t.order = append(t.order, use.ID)
		return
	}
	if use.Status != "" {
		existing.Status = use.Status
	}
	if use.Query != "" {
		existing.Query = use.Query
	}
	if use.URL != "" {
		existing.URL = use.URL
	}
	if use.Title != "" {
		existing.Title = use.Title
	}
	if use.SubTool != "" {
		existing.SubTool = use.SubTool
	}
	if use.Command != "" {
		existing.Command = use.Command
	}
	if use.Output != "" {
		existing.Output = use.Output
	}
	if use.ErrorCode != "" {
		existing.ErrorCode = use.ErrorCode
	}
}

// setCommand records the sandbox code/command for an already-tracked
// code-execution item (from a code_interpreter_call_code.done event). Returns
// false when the item is unknown.
func (t *serverToolTracker) setCommand(itemID, command string) bool {
	entry, ok := t.byID[itemID]
	if !ok {
		return false
	}
	entry.Command = truncateForDisplay(command, serverToolCommandMaxLen)
	return true
}

// observeItem converts a Responses output item into tracked server-tool
// activity. Returns true when the item was server-tool related (i.e. callers
// should emit a fresh snapshot).
func (t *serverToolTracker) observeItem(item *schemas.ResponsesMessage) bool {
	use := serverToolUseFromItem(item)
	if use == nil {
		return false
	}
	t.upsert(*use)
	return true
}

// serverToolUseFromItem maps a web_search_call / web_fetch_call /
// code_interpreter_call output item onto the neutral activity struct. Returns
// nil for every other item type.
func serverToolUseFromItem(item *schemas.ResponsesMessage) *llm.ServerToolUse {
	if item == nil || item.Type == nil {
		return nil
	}

	use := llm.ServerToolUse{Status: mapServerToolStatus(item.Status)}
	if item.ID != nil {
		use.ID = *item.ID
	}
	tm := item.ResponsesToolMessage
	if use.ID == "" && tm != nil && tm.CallID != nil {
		use.ID = *tm.CallID
	}
	if use.ID == "" {
		return nil
	}

	switch *item.Type {
	case schemas.ResponsesMessageTypeWebSearchCall:
		use.Tool = llm.NativeToolWebSearch
		if tm != nil && tm.Action != nil && tm.Action.ResponsesWebSearchToolCallAction != nil {
			if q := tm.Action.ResponsesWebSearchToolCallAction.Query; q != nil {
				use.Query = *q
			}
		}

	case schemas.ResponsesMessageTypeWebFetchCall:
		use.Tool = llm.NativeToolWebFetch
		if tm != nil {
			if tm.Action != nil && tm.Action.ResponsesWebFetchToolCallAction != nil {
				use.URL = tm.Action.ResponsesWebFetchToolCallAction.URL
			}
			if fetch := tm.ResponsesWebFetchCall; fetch != nil {
				if fetch.URL != nil && *fetch.URL != "" {
					use.URL = *fetch.URL
				}
				if fetch.Document != nil && fetch.Document.Title != nil {
					use.Title = truncateForDisplay(*fetch.Document.Title, serverToolOutputMaxLen)
				}
				if fetch.ErrorCode != nil && *fetch.ErrorCode != "" {
					use.ErrorCode = *fetch.ErrorCode
					use.Status = llm.ServerToolStatusError
				}
			}
		}

	case schemas.ResponsesMessageTypeCodeInterpreterCall:
		use.Tool = llm.NativeToolCodeInterpreter
		if tm != nil {
			populateCodeExecutionFields(&use, tm)
		}

	default:
		return nil
	}

	return &use
}

// populateCodeExecutionFields fills the code-execution specifics from both the
// neutral code_interpreter view and the Anthropic fidelity carry.
func populateCodeExecutionFields(use *llm.ServerToolUse, tm *schemas.ResponsesToolMessage) {
	if ci := tm.ResponsesCodeInterpreterToolCall; ci != nil {
		if ci.Code != nil {
			use.Command = truncateForDisplay(*ci.Code, serverToolCommandMaxLen)
		}
		for _, out := range ci.Outputs {
			if out.ResponsesCodeInterpreterOutputLogs != nil && out.Logs != "" {
				use.Output = truncateForDisplay(out.Logs, serverToolOutputMaxLen)
				break
			}
		}
	}

	carry := tm.ResponsesCodeExecutionCall
	if carry == nil {
		return
	}
	use.SubTool = codeExecutionSubTool(carry.ToolName)
	if use.Command == "" && carry.Input != nil {
		use.Command = truncateForDisplay(*carry.Input, serverToolCommandMaxLen)
	}

	output := ""
	if carry.Stdout != nil {
		output = *carry.Stdout
	}
	if carry.Stderr != nil && *carry.Stderr != "" {
		if output != "" {
			output += "\n"
		}
		output += "[stderr]\n" + *carry.Stderr
	}
	if output != "" {
		use.Output = truncateForDisplay(output, serverToolOutputMaxLen)
	}

	if carry.ErrorCode != nil && *carry.ErrorCode != "" {
		use.ErrorCode = *carry.ErrorCode
		use.Status = llm.ServerToolStatusError
	} else if strings.HasSuffix(carry.ResultType, "_error") && carry.ResultType != "" {
		use.ErrorCode = carry.ResultType
		use.Status = llm.ServerToolStatusError
	}
}

// codeExecutionSubTool normalizes Anthropic's code-execution sub-tool names
// for display: bash_code_execution → "bash", text_editor_code_execution →
// "text_editor", code_execution (legacy) → "python". Unknown names pass
// through unchanged (OpenAI's code_interpreter has no sub-tool and yields "").
func codeExecutionSubTool(toolName string) string {
	switch toolName {
	case "":
		return ""
	case "code_execution":
		return "python"
	default:
		return strings.TrimSuffix(toolName, "_code_execution")
	}
}

// mapServerToolStatus converts a Responses item status to the neutral
// activity status. "incomplete" (e.g. an OpenAI code_interpreter_call cut off
// by max tokens, or an Anthropic call truncated mid-stream) is terminal and
// maps to error — leaving it in progress would spin forever in the UI.
func mapServerToolStatus(status *string) string {
	if status == nil {
		return llm.ServerToolStatusInProgress
	}
	switch *status {
	case "completed":
		return llm.ServerToolStatusSuccess
	case "failed", "incomplete":
		return llm.ServerToolStatusError
	default:
		return llm.ServerToolStatusInProgress
	}
}

// truncateForDisplay elides s past limit runes, marking the cut. Rune-safe so
// multi-byte content isn't split mid-character.
func truncateForDisplay(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "… (truncated)"
}
