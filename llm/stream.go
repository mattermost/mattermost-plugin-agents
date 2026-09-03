// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"slices"
	"strings"
)

// MaxPostAttachments is the Mattermost per-post attachment limit. It bounds
// how many files tools may create for or attach to a single post.
const MaxPostAttachments = 10

// EventType represents the type of event in the text stream
type EventType int

const (
	// EventTypeText represents a text chunk event
	EventTypeText EventType = iota
	// EventTypeEnd represents the end of the stream
	EventTypeEnd
	// EventTypeError represents an error event
	EventTypeError
	// EventTypeToolCalls represents a tool call event
	EventTypeToolCalls
	// EventTypeReasoning represents a reasoning summary chunk event
	EventTypeReasoning
	// EventTypeReasoningEnd represents the end of reasoning summary
	EventTypeReasoningEnd
	// EventTypeAnnotations represents annotations/citations in the response
	EventTypeAnnotations
	// EventTypeUsage represents token usage data
	EventTypeUsage
	// EventTypeFiles carries the IDs of files created during the turn that
	// should be attached to the response post. Value is []string of
	// Mattermost file IDs.
	EventTypeFiles
	// EventTypeServerToolUse represents provider-executed (server) tool
	// activity — e.g. Anthropic web_search / web_fetch / code_execution or
	// OpenAI web_search / code_interpreter. The value is the cumulative
	// []ServerToolUse for the current round; receivers replace prior state.
	EventTypeServerToolUse
)

// Server tool activity status values. They intentionally match the
// conversation content-block status strings so the streaming layer can persist
// them verbatim.
const (
	ServerToolStatusInProgress = "in_progress"
	ServerToolStatusSuccess    = "success"
	ServerToolStatusError      = "error"
)

// ServerToolUse describes one provider-executed tool invocation observed on
// the stream. Tool uses the same neutral ids as BotConfig.EnabledNativeTools
// (NativeToolWebSearch, NativeToolWebFetch, NativeToolCodeInterpreter); the
// remaining fields are populated per tool and pre-truncated for display.
type ServerToolUse struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Status string `json:"status"`

	// Query is the web_search query.
	Query string `json:"query,omitempty"`
	// URL is the web_fetch target (request URL, replaced by the resolved
	// result URL when the fetch completes).
	URL string `json:"url,omitempty"`
	// Title is the fetched document title (web_fetch).
	Title string `json:"title,omitempty"`
	// SubTool is the code-execution sub-tool: "bash", "text_editor" or
	// "python" (Anthropic), empty for providers without sub-tools.
	SubTool string `json:"sub_tool,omitempty"`
	// Command is the code or shell command executed in the sandbox.
	Command string `json:"command,omitempty"`
	// Output is the execution output (stdout/logs, with stderr appended when
	// present) or other human-readable result summary.
	Output string `json:"output,omitempty"`
	// ErrorCode is the provider error code when the invocation failed.
	ErrorCode string `json:"error_code,omitempty"`
	// FileIDs are provider-side ids of files left in the sandbox output directory.
	FileIDs []string `json:"file_ids,omitempty"`

	// ProviderRoute is the Bifrost route that produced FileIDs. Runtime-only:
	// needed for fallback downloads, never broadcast or persisted for display.
	ProviderRoute string `json:"-"`
}

// Clone returns a copy whose FileIDs slice is independent of the original, so
// presentation-side mutation cannot corrupt the canonical replay snapshot.
func (s ServerToolUse) Clone() ServerToolUse {
	s.FileIDs = slices.Clone(s.FileIDs)
	return s
}

// CloneServerToolUses copies FileIDs so presentation sanitation cannot mutate
// the canonical provider replay snapshot.
func CloneServerToolUses(uses []ServerToolUse) []ServerToolUse {
	cloned := slices.Clone(uses)
	for i := range cloned {
		cloned[i] = cloned[i].Clone()
	}
	return cloned
}

// Sanitize escapes Unicode bidi/spoofing characters in every LLM- or
// web-influenced string field, mirroring ToolCall.SanitizeArguments. Call it
// before broadcasting or persisting the activity.
func (s *ServerToolUse) Sanitize() {
	s.Query = SanitizeNonPrintableChars(s.Query)
	s.URL = SanitizeNonPrintableChars(s.URL)
	s.Title = SanitizeNonPrintableChars(s.Title)
	s.Command = SanitizeNonPrintableChars(s.Command)
	s.Output = SanitizeNonPrintableChars(s.Output)
	s.ErrorCode = SanitizeNonPrintableChars(s.ErrorCode)
	for i := range s.FileIDs {
		s.FileIDs[i] = SanitizeNonPrintableChars(s.FileIDs[i])
	}
}

// TokenUsage represents token usage statistics for an LLM request. Cached,
// reasoning, and cost fields stay zero when the provider doesn't report them.
type TokenUsage struct {
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CachedReadTokens  int64   `json:"cached_read_tokens,omitempty"`
	CachedWriteTokens int64   `json:"cached_write_tokens,omitempty"`
	ReasoningTokens   int64   `json:"reasoning_tokens,omitempty"`
	Cost              float64 `json:"cost,omitempty"`
}

// ReasoningData represents the complete reasoning/thinking data including signature
type ReasoningData struct {
	Text      string // The reasoning/thinking text content
	Signature string // Opaque verification signature from the model
}

// TextStreamEvent represents an event in the text stream
type TextStreamEvent struct {
	Type  EventType
	Value any
}

// TextStreamResult represents a stream of text events
type TextStreamResult struct {
	Stream <-chan TextStreamEvent
}

func NewStreamFromString(text string) *TextStreamResult {
	stream := make(chan TextStreamEvent)

	go func() {
		// Send the text as a text event
		stream <- TextStreamEvent{
			Type:  EventTypeText,
			Value: text,
		}

		// Send end event
		stream <- TextStreamEvent{
			Type:  EventTypeEnd,
			Value: nil,
		}

		close(stream)
	}()

	return &TextStreamResult{
		Stream: stream,
	}
}

func (t *TextStreamResult) ReadAll() (string, error) {
	var result strings.Builder
	for event := range t.Stream {
		switch event.Type {
		case EventTypeText:
			if textChunk, ok := event.Value.(string); ok {
				result.WriteString(textChunk)
			}
		case EventTypeError:
			if err, ok := event.Value.(error); ok {
				return "", err
			}
			if msg, ok := event.Value.(string); ok {
				return "", fmt.Errorf("%s", msg)
			}
			return "", fmt.Errorf("unknown stream error")
		case EventTypeEnd:
			return result.String(), nil
		case EventTypeToolCalls:
			// Tool calls may appear as progress events from auto-run tools; skip them.
			continue
		case EventTypeAnnotations, EventTypeReasoning, EventTypeReasoningEnd, EventTypeUsage, EventTypeFiles, EventTypeServerToolUse:
			// These event types are ignored in ReadAll, continue reading text
			continue
		}
	}

	return result.String(), nil
}
