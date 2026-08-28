// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"slices"
	"sort"
	"strings"
)

const (
	TurnSegmentText       = "text"
	TurnSegmentThinking   = "thinking"
	TurnSegmentServerTool = "server_tool"
)

// TextRange is a half-open byte range in concatenated text segments, used to
// delete citation markers without moving text around intervening activity.
type TextRange struct {
	Start int
	End   int
}

// TurnSegment is one piece of assistant output in arrival order.
type TurnSegment struct {
	Kind      string
	Text      string
	Signature string

	// ServerToolID looks up the payload in the latest activity snapshot.
	// The provider updates invocations in place, so the segment only stores the id.
	ServerToolID string

	// finalized is set when reasoning-end arrives, so later reasoning starts a new segment.
	finalized bool
}

// TurnSequence records assistant output in arrival order. Grouping by kind
// (all text, then all activity) reorders interleaved narration and sandbox
// runs so the bot appears to describe work before doing it.
type TurnSequence struct {
	segments []TurnSegment
}

// AppendText extends the current text segment when the previous arrival was also text.
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

// AppendReasoning extends the current unfinalized thinking segment.
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

// FinishReasoning closes the current thinking segment. Later reasoning starts a new one.
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

// RecordServerTools positions each new invocation at first appearance.
// Snapshots are cumulative; later updates must not move an already-placed id.
func (s *TurnSequence) RecordServerTools(uses []ServerToolUse) {
	for i := range uses {
		id := uses[i].ID
		if id == "" || s.hasServerTool(id) {
			continue
		}
		s.segments = append(s.segments, TurnSegment{Kind: TurnSegmentServerTool, ServerToolID: id})
	}
}

// RemoveTextRanges deletes ranges from concatenated text in place so activity
// between text segments keeps its position. original must match Text() exactly.
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

// Text concatenates response-text segments, excluding reasoning and activity.
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
