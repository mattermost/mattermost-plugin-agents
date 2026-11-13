// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareAndSwapTurnContentAndStatus(t *testing.T) {
	const (
		pending  = `[{"type":"tool_use","id":"t1","status":"pending"}]`
		accepted = `[{"type":"tool_use","id":"t1","status":"accepted"}]`
	)
	tests := []struct {
		name            string
		status          string
		expectedContent string
		expectedStatus  string
		wantSwapped     bool
	}{
		{name: "matching content and status swaps", status: TurnStatusComplete, expectedContent: pending, expectedStatus: TurnStatusComplete, wantSwapped: true},
		{name: "content compared as JSON, not bytes", status: TurnStatusComplete, expectedContent: `[{"status":"pending","id":"t1","type":"tool_use"}]`, expectedStatus: TurnStatusComplete, wantSwapped: true},
		{name: "changed content does not swap", status: TurnStatusComplete, expectedContent: accepted, expectedStatus: TurnStatusComplete},
		{name: "changed status does not swap", status: TurnStatusInterrupted, expectedContent: pending, expectedStatus: TurnStatusStreaming},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, convID := setupStreamStore(t)
			turn := makeTurn(convID, 0, func(tu *Turn) {
				tu.Role = "assistant"
				tu.Status = tc.status
				tu.Content = json.RawMessage(pending)
			})
			require.NoError(t, s.CreateTurnAutoSequence(turn))
			ageTurn(t, s, turn.ID, 2*testStaleMillis)
			before := getTurn(t, s, turn.ID)

			swapped, err := s.CompareAndSwapTurnContentAndStatus(turn.ID, json.RawMessage(tc.expectedContent), tc.expectedStatus, json.RawMessage(accepted), TurnStatusStreaming)
			require.NoError(t, err)
			assert.Equal(t, tc.wantSwapped, swapped)

			after := getTurn(t, s, turn.ID)
			if tc.wantSwapped {
				assert.JSONEq(t, accepted, string(after.Content))
				assert.Equal(t, TurnStatusStreaming, after.Status)
				assert.Greater(t, after.UpdatedAt, before.UpdatedAt, "the swap is a fresh heartbeat")
				return
			}
			assert.JSONEq(t, pending, string(after.Content))
			assert.Equal(t, tc.status, after.Status)
			assert.Equal(t, before.UpdatedAt, after.UpdatedAt)
		})
	}

	t.Run("missing turn reports false", func(t *testing.T) {
		s, _ := setupStreamStore(t)
		swapped, err := s.CompareAndSwapTurnContentAndStatus("missing", json.RawMessage(pending), TurnStatusComplete, json.RawMessage(accepted), TurnStatusStreaming)
		require.NoError(t, err)
		assert.False(t, swapped)
	})
}

func TestTouchStreamingTurn(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		wantStill bool
	}{
		{name: "streaming turn is touched", status: TurnStatusStreaming, wantStill: true},
		{name: "interrupted turn is not revived", status: TurnStatusInterrupted},
		{name: "complete turn is untouched", status: TurnStatusComplete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, convID := setupStreamStore(t)
			turn := createTurnWithStatus(t, s, convID, tc.status)
			ageTurn(t, s, turn.ID, 2*testStaleMillis)
			before := getTurn(t, s, turn.ID)

			still, err := s.TouchStreamingTurn(turn.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.wantStill, still)

			after := getTurn(t, s, turn.ID)
			assert.Equal(t, tc.status, after.Status)
			assert.JSONEq(t, string(before.Content), string(after.Content), "a touch never changes content")
			if tc.wantStill {
				assert.Greater(t, after.UpdatedAt, before.UpdatedAt)
				claimed, claimErr := s.ClaimStaleStreamingTurn(turn.ID, testStaleMillis)
				require.NoError(t, claimErr)
				assert.False(t, claimed, "a touched turn is not stale")
			} else {
				assert.Equal(t, before.UpdatedAt, after.UpdatedAt)
			}
		})
	}
}

func TestTransitionTurnStatus(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		from, to   string
		wantMoved  bool
		wantStatus string
	}{
		{name: "streaming to interrupted", status: TurnStatusStreaming, from: TurnStatusStreaming, to: TurnStatusInterrupted, wantMoved: true, wantStatus: TurnStatusInterrupted},
		{name: "a sweep's interrupted verdict is not overwritten", status: TurnStatusInterrupted, from: TurnStatusStreaming, to: TurnStatusComplete, wantStatus: TurnStatusInterrupted},
		{name: "complete is not interrupted", status: TurnStatusComplete, from: TurnStatusStreaming, to: TurnStatusInterrupted, wantStatus: TurnStatusComplete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, convID := setupStreamStore(t)
			turn := createTurnWithStatus(t, s, convID, tc.status)
			before := getTurn(t, s, turn.ID)

			moved, err := s.TransitionTurnStatus(turn.ID, tc.from, tc.to)
			require.NoError(t, err)
			assert.Equal(t, tc.wantMoved, moved)
			after := getTurn(t, s, turn.ID)
			assert.Equal(t, tc.wantStatus, after.Status)
			assert.JSONEq(t, string(before.Content), string(after.Content))
		})
	}
}
