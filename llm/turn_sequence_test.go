// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTurnSequenceInterleavedArrival is the case the fixed-category renderer got
// wrong: a turn that narrates, runs a sandbox command, narrates again, then runs
// another. Grouping by kind puts both narrations above both executions, so the
// bot reads as having described work before doing it.
func TestTurnSequenceInterleavedArrival(t *testing.T) {
	var s TurnSequence

	s.AppendText("I'll create the file.")
	s.RecordServerTools([]ServerToolUse{{ID: "srv1", Tool: NativeToolCodeInterpreter}})
	s.AppendText("That didn't produce a file.")
	s.RecordServerTools([]ServerToolUse{
		{ID: "srv1", Tool: NativeToolCodeInterpreter},
		{ID: "srv2", Tool: NativeToolCodeInterpreter},
	})
	s.AppendText("Done.")

	require.Equal(t, []TurnSegment{
		{Kind: TurnSegmentText, Text: "I'll create the file."},
		{Kind: TurnSegmentServerTool, ServerToolID: "srv1"},
		{Kind: TurnSegmentText, Text: "That didn't produce a file."},
		{Kind: TurnSegmentServerTool, ServerToolID: "srv2"},
		{Kind: TurnSegmentText, Text: "Done."},
	}, s.Segments())

	assert.Equal(t, "I'll create the file.That didn't produce a file.Done.", s.Text())
}

func TestTurnSequenceMergesConsecutiveDeltas(t *testing.T) {
	var s TurnSequence

	s.AppendText("Hel")
	s.AppendText("lo")
	s.AppendText("")
	s.AppendReasoning("think")
	s.AppendReasoning("ing")
	s.AppendText(" there")

	require.Equal(t, []TurnSegment{
		{Kind: TurnSegmentText, Text: "Hello"},
		{Kind: TurnSegmentThinking, Text: "thinking"},
		{Kind: TurnSegmentText, Text: " there"},
	}, s.Segments())
}

// TestTurnSequenceReasoningBlocks covers multiple reasoning blocks in one turn:
// each end event closes only the block in progress, and the provider's final
// text replaces whatever partial text was streamed for it.
func TestTurnSequenceReasoningBlocks(t *testing.T) {
	var s TurnSequence

	s.AppendReasoning("partial one")
	s.FinishReasoning(ReasoningData{Text: "first thought", Signature: "sig1"})
	s.AppendText("interlude")
	s.AppendReasoning("partial two")
	s.FinishReasoning(ReasoningData{Text: "second thought", Signature: "sig2"})

	require.Equal(t, []TurnSegment{
		{Kind: TurnSegmentThinking, Text: "first thought", Signature: "sig1", finalized: true},
		{Kind: TurnSegmentText, Text: "interlude"},
		{Kind: TurnSegmentThinking, Text: "second thought", Signature: "sig2", finalized: true},
	}, s.Segments())
}

// TestTurnSequenceUnfinishedReasoningIsKept pins that reasoning streamed before
// an error or cancel still renders — the partial thought is all there is.
func TestTurnSequenceUnfinishedReasoningIsKept(t *testing.T) {
	var s TurnSequence
	s.AppendReasoning("partial")

	require.Equal(t, []TurnSegment{{Kind: TurnSegmentThinking, Text: "partial"}}, s.Segments())
}

// TestTurnSequenceFinishReasoningWithoutDeltas covers thinking display modes
// that stream no reasoning text and deliver it only at the end.
func TestTurnSequenceFinishReasoningWithoutDeltas(t *testing.T) {
	var s TurnSequence
	s.AppendText("answer")
	s.FinishReasoning(ReasoningData{Text: "summary", Signature: "sig"})
	s.FinishReasoning(ReasoningData{Text: "", Signature: "sig"})

	require.Equal(t, []TurnSegment{
		{Kind: TurnSegmentText, Text: "answer"},
		{Kind: TurnSegmentThinking, Text: "summary", Signature: "sig", finalized: true},
	}, s.Segments())
}

// TestTurnSequenceRecordServerToolsIsIdempotent pins that cumulative snapshots
// do not duplicate an invocation, and that an id keeps its original position
// when a later snapshot updates it in place.
func TestTurnSequenceRecordServerToolsIsIdempotent(t *testing.T) {
	var s TurnSequence

	s.RecordServerTools([]ServerToolUse{{ID: "srv1", Status: ServerToolStatusInProgress}})
	s.AppendText("working")
	s.RecordServerTools([]ServerToolUse{{ID: "srv1", Status: ServerToolStatusSuccess}})
	s.RecordServerTools([]ServerToolUse{{ID: "", Status: ServerToolStatusSuccess}})

	require.Equal(t, []TurnSegment{
		{Kind: TurnSegmentServerTool, ServerToolID: "srv1"},
		{Kind: TurnSegmentText, Text: "working"},
	}, s.Segments())
}

// TestTurnSequenceReplaceText covers web-search citation cleanup, which rewrites
// the whole message: the cleaned text takes the first text position so it still
// follows the search activity that produced it.
func TestTurnSequenceReplaceText(t *testing.T) {
	var s TurnSequence

	s.RecordServerTools([]ServerToolUse{{ID: "srv1", Tool: NativeToolWebSearch}})
	s.AppendText("Answer with [1] markers")
	s.RecordServerTools([]ServerToolUse{{ID: "srv2", Tool: NativeToolWebFetch}})
	s.AppendText(" and more [2]")

	s.ReplaceText("Answer with markers and more")

	require.Equal(t, []TurnSegment{
		{Kind: TurnSegmentServerTool, ServerToolID: "srv1"},
		{Kind: TurnSegmentText, Text: "Answer with markers and more"},
		{Kind: TurnSegmentServerTool, ServerToolID: "srv2"},
	}, s.Segments())
}

func TestTurnSequenceReplaceTextWithNoPriorText(t *testing.T) {
	var s TurnSequence
	s.RecordServerTools([]ServerToolUse{{ID: "srv1", Tool: NativeToolWebSearch}})
	s.ReplaceText("cleaned")

	require.Equal(t, []TurnSegment{
		{Kind: TurnSegmentServerTool, ServerToolID: "srv1"},
		{Kind: TurnSegmentText, Text: "cleaned"},
	}, s.Segments())
}

func TestTurnSequenceResetAndHasText(t *testing.T) {
	var s TurnSequence
	assert.False(t, s.HasText())

	s.AppendReasoning("thinking")
	assert.False(t, s.HasText(), "reasoning is not response text")

	s.AppendText("hi")
	assert.True(t, s.HasText())

	s.Reset()
	assert.Empty(t, s.Segments())
	assert.False(t, s.HasText())
	assert.Empty(t, s.Text())
}
