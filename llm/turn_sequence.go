// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"slices"
	"sort"
	"strings"
)

// Kinds of assistant output a turn is made of, in the order the provider
// streamed them.
const (
	TurnSegmentText       = "text"
	TurnSegmentThinking   = "thinking"
	TurnSegmentServerTool = "server_tool"
)

// TextRange is a half-open byte range in the concatenation of a turn's text
// segments. It is used for deletion-only rewrites such as removing citation
// markers without moving text around intervening activity.
type TextRange struct {
	Start int
	End   int
}

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

// RemoveTextRanges deletes ranges from the concatenated response text while
// retaining every text segment's position relative to reasoning and server
// tools. original must match the sequence's text exactly; a mismatch returns
// false without changing the sequence.
func (s *TurnSequence) RemoveTextRanges(original string, ranges []TextRange) bool {
	if s.Text() != original {
		return false
	}
	if len(ranges) == 0 {
		return true
	}

	normalized := slices.Clone(ranges)
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Start == normalized[j].Start {
			return normalized[i].End < normalized[j].End
		}
		return normalized[i].Start < normalized[j].Start
	})
	for i := range normalized {
		if normalized[i].Start < 0 || normalized[i].End < normalized[i].Start || normalized[i].End > len(original) {
			return false
		}
		if i > 0 && normalized[i].Start < normalized[i-1].End {
			return false
		}
	}

	updated := make([]TurnSegment, 0, len(s.segments))
	textOffset := 0
	for _, segment := range s.segments {
		if segment.Kind != TurnSegmentText {
			updated = append(updated, segment)
			continue
		}

		segmentStart := textOffset
		segmentEnd := segmentStart + len(segment.Text)
		textOffset = segmentEnd
		cursor := 0
		var cleaned strings.Builder
		for _, textRange := range normalized {
			if textRange.End <= segmentStart || textRange.Start >= segmentEnd {
				continue
			}
			removeStart := max(textRange.Start, segmentStart) - segmentStart
			removeEnd := min(textRange.End, segmentEnd) - segmentStart
			cleaned.WriteString(segment.Text[cursor:removeStart])
			cursor = removeEnd
		}
		cleaned.WriteString(segment.Text[cursor:])
		segment.Text = cleaned.String()
		if segment.Text != "" {
			updated = append(updated, segment)
		}
	}

	s.segments = updated
	return true
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
