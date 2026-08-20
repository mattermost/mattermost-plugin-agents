// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "strings"

// Kinds of assistant output a turn is made of, in the order the provider
// streamed them.
const (
	TurnSegmentText       = "text"
	TurnSegmentThinking   = "thinking"
	TurnSegmentServerTool = "server_tool"
)

// TurnSegment is one piece of assistant output, recorded when it arrived.
type TurnSegment struct {
	Kind string

	// Text is the segment's content for text and thinking segments.
	Text string

	// Signature is the provider-issued signature of a thinking segment.
	Signature string

	// ServerToolID identifies which provider-executed tool invocation a
	// server_tool segment stands for. The payload is not copied here: the
	// provider updates an invocation in place as it progresses, so renderers
	// look it up in the latest activity snapshot.
	ServerToolID string

	// finalized marks a thinking segment whose provider-issued final text has
	// already arrived, so a later reasoning-end event starts a new one.
	finalized bool
}

// TurnSequence records assistant output in arrival order.
//
// A provider interleaves narration, reasoning, and provider-executed tool
// activity — text, then a code execution, then more text. Collecting those into
// per-kind buckets and rendering the buckets in a fixed order silently reorders
// the turn: every narration fragment collapses into one block, and the activity
// that happened between them all pools together, so the bot appears to have
// described work before doing it. Recording arrival order keeps the rendered
// turn in the order it actually happened.
type TurnSequence struct {
	segments []TurnSegment
}

// AppendText adds streamed response text, extending the current text segment
// when the previous arrival was also text.
func (s *TurnSequence) AppendText(text string) {
	if text == "" {
		return
	}
	if last := s.last(); last != nil && last.Kind == TurnSegmentText {
		last.Text += text
		return
	}
	s.segments = append(s.segments, TurnSegment{Kind: TurnSegmentText, Text: text})
}

// AppendReasoning adds streamed reasoning text, extending the current
// unfinalized thinking segment when the previous arrival was also reasoning.
func (s *TurnSequence) AppendReasoning(text string) {
	if text == "" {
		return
	}
	if last := s.last(); last != nil && last.Kind == TurnSegmentThinking && !last.finalized {
		last.Text += text
		return
	}
	s.segments = append(s.segments, TurnSegment{Kind: TurnSegmentThinking, Text: text})
}

// FinishReasoning records the provider's final text and signature for the
// reasoning block in progress, replacing any partial text streamed for it. A
// turn can contain several reasoning blocks, so this closes only the current
// one; the next reasoning text starts a new segment.
func (s *TurnSequence) FinishReasoning(data ReasoningData) {
	if data.Text == "" {
		return
	}
	if last := s.last(); last != nil && last.Kind == TurnSegmentThinking && !last.finalized {
		last.Text = data.Text
		last.Signature = data.Signature
		last.finalized = true
		return
	}
	s.segments = append(s.segments, TurnSegment{
		Kind:      TurnSegmentThinking,
		Text:      data.Text,
		Signature: data.Signature,
		finalized: true,
	})
}

// RecordServerTools notes the position of each provider-executed invocation in
// the given activity snapshot. Snapshots are cumulative and an invocation is
// updated in place as it progresses, so only invocations not yet positioned are
// appended — an id keeps the place where it first appeared.
func (s *TurnSequence) RecordServerTools(uses []ServerToolUse) {
	for i := range uses {
		id := uses[i].ID
		if id == "" || s.hasServerTool(id) {
			continue
		}
		s.segments = append(s.segments, TurnSegment{Kind: TurnSegmentServerTool, ServerToolID: id})
	}
}

// ReplaceText collapses the turn's text into a single segment holding the given
// content, keeping the position of the first text segment (or appending when
// there is none). Used for whole-message rewrites such as web-search citation
// cleanup, which cannot be mapped back onto the individual fragments.
func (s *TurnSequence) ReplaceText(text string) {
	kept := make([]TurnSegment, 0, len(s.segments)+1)
	inserted := false
	for _, segment := range s.segments {
		if segment.Kind != TurnSegmentText {
			kept = append(kept, segment)
			continue
		}
		if !inserted && text != "" {
			kept = append(kept, TurnSegment{Kind: TurnSegmentText, Text: text})
			inserted = true
		}
	}
	if !inserted && text != "" {
		kept = append(kept, TurnSegment{Kind: TurnSegmentText, Text: text})
	}
	s.segments = kept
}

// Reset drops every recorded segment.
func (s *TurnSequence) Reset() {
	s.segments = nil
}

// Segments returns the recorded segments in arrival order.
func (s *TurnSequence) Segments() []TurnSegment {
	return s.segments
}

// Text returns every text segment concatenated, i.e. the turn's full response
// text with reasoning and activity removed.
func (s *TurnSequence) Text() string {
	var b strings.Builder
	for _, segment := range s.segments {
		if segment.Kind == TurnSegmentText {
			b.WriteString(segment.Text)
		}
	}
	return b.String()
}

// HasText reports whether any response text was recorded.
func (s *TurnSequence) HasText() bool {
	for _, segment := range s.segments {
		if segment.Kind == TurnSegmentText && segment.Text != "" {
			return true
		}
	}
	return false
}

func (s *TurnSequence) last() *TurnSegment {
	if len(s.segments) == 0 {
		return nil
	}
	return &s.segments[len(s.segments)-1]
}

func (s *TurnSequence) hasServerTool(id string) bool {
	for _, segment := range s.segments {
		if segment.Kind == TurnSegmentServerTool && segment.ServerToolID == id {
			return true
		}
	}
	return false
}
