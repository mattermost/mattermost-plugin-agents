// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testStaleMillis = int64(60_000)

func setupStreamStore(t *testing.T) (*Store, string) {
	t.Helper()
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())
	conv := makeConversation()
	require.NoError(t, s.CreateConversation(conv))
	return s, conv.ID
}

// ageTurn pushes a turn's heartbeat into the past by ms, as if its node died.
func ageTurn(t *testing.T, s *Store, id string, ms int64) {
	t.Helper()
	_, err := s.db.Exec(`UPDATE LLM_Turns SET UpdatedAt = UpdatedAt - $2 WHERE ID = $1`, id, ms)
	require.NoError(t, err)
}

func createTurnWithStatus(t *testing.T, s *Store, convID, status string) *Turn {
	t.Helper()
	postID := "post-" + status
	turn := makeTurn(convID, 0, func(tu *Turn) {
		tu.Role = "assistant"
		tu.Status = status
		tu.PostID = &postID
	})
	require.NoError(t, s.CreateTurnAutoSequence(turn))
	return turn
}

func getTurn(t *testing.T, s *Store, id string) Turn {
	t.Helper()
	var turn Turn
	require.NoError(t, s.db.Get(&turn, `SELECT ID, ConversationID, PostID, Role, Content, TokensIn, TokensOut, Sequence, CreatedAt, Status, UpdatedAt FROM LLM_Turns WHERE ID = $1`, id))
	return turn
}

func TestTurnStatusDefaults(t *testing.T) {
	s, convID := setupStreamStore(t)

	plain := makeTurn(convID, 1)
	require.NoError(t, s.CreateTurn(plain))
	auto := makeTurn(convID, 0)
	require.NoError(t, s.CreateTurnAutoSequence(auto))

	turns, err := s.GetTurnsForConversation(convID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	for _, tu := range turns {
		assert.Equal(t, TurnStatusComplete, tu.Status)
		assert.NotZero(t, tu.UpdatedAt)
	}
}

func TestGetStaleStreamingTurns(t *testing.T) {
	s, convID := setupStreamStore(t)

	staleStreaming := createTurnWithStatus(t, s, convID, TurnStatusStreaming)
	ageTurn(t, s, staleStreaming.ID, 2*testStaleMillis)
	freshStreaming := makeTurn(convID, 0, func(tu *Turn) { tu.Role = "assistant"; tu.Status = TurnStatusStreaming })
	require.NoError(t, s.CreateTurnAutoSequence(freshStreaming))
	oldComplete := createTurnWithStatus(t, s, convID, TurnStatusComplete)
	ageTurn(t, s, oldComplete.ID, 2*testStaleMillis)
	oldInterrupted := createTurnWithStatus(t, s, convID, TurnStatusInterrupted)
	ageTurn(t, s, oldInterrupted.ID, 2*testStaleMillis)

	stale, err := s.GetStaleStreamingTurns(testStaleMillis, 100)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, staleStreaming.ID, stale[0].ID)

	tests := []struct {
		name        string
		id          string
		wantClaimed bool
		wantStatus  string
	}{
		{name: "stale streaming is claimed", id: staleStreaming.ID, wantClaimed: true, wantStatus: TurnStatusInterrupted},
		{name: "fresh streaming is untouched", id: freshStreaming.ID, wantClaimed: false, wantStatus: TurnStatusStreaming},
		{name: "old complete is untouched", id: oldComplete.ID, wantClaimed: false, wantStatus: TurnStatusComplete},
		{name: "already interrupted is not reclaimed", id: oldInterrupted.ID, wantClaimed: false, wantStatus: TurnStatusInterrupted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claimed, err := s.ClaimStaleStreamingTurn(tc.id, testStaleMillis)
			require.NoError(t, err)
			assert.Equal(t, tc.wantClaimed, claimed)
			assert.Equal(t, tc.wantStatus, getTurn(t, s, tc.id).Status)
		})
	}
}

func TestClaimStaleStreamingTurnConcurrent(t *testing.T) {
	s, convID := setupStreamStore(t)
	turn := createTurnWithStatus(t, s, convID, TurnStatusStreaming)
	ageTurn(t, s, turn.ID, 2*testStaleMillis)

	const workers = 8
	var wg sync.WaitGroup
	results := make([]bool, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = s.ClaimStaleStreamingTurn(turn.ID, testStaleMillis)
		}()
	}
	close(start)
	wg.Wait()

	wins := 0
	for i := range workers {
		require.NoError(t, errs[i])
		if results[i] {
			wins++
		}
	}
	assert.Equal(t, 1, wins)
}

func TestHeartbeatStreamingTurn(t *testing.T) {
	s, convID := setupStreamStore(t)
	turn := createTurnWithStatus(t, s, convID, TurnStatusStreaming)
	ageTurn(t, s, turn.ID, 2*testStaleMillis)
	before := getTurn(t, s, turn.ID)

	content := json.RawMessage(`[{"type":"text","text":"partial"}]`)
	require.NoError(t, s.HeartbeatStreamingTurn(turn.ID, content))

	after := getTurn(t, s, turn.ID)
	assert.JSONEq(t, string(content), string(after.Content))
	assert.Greater(t, after.UpdatedAt, before.UpdatedAt)

	// A heartbeat revives the turn: it is no longer stale and cannot be claimed.
	claimed, err := s.ClaimStaleStreamingTurn(turn.ID, testStaleMillis)
	require.NoError(t, err)
	assert.False(t, claimed)

	// Once interrupted, a late heartbeat from a zombie stream does not revert it.
	ageTurn(t, s, turn.ID, 2*testStaleMillis)
	claimed, err = s.ClaimStaleStreamingTurn(turn.ID, testStaleMillis)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, s.HeartbeatStreamingTurn(turn.ID, json.RawMessage(`[]`)))
	final := getTurn(t, s, turn.ID)
	assert.Equal(t, TurnStatusInterrupted, final.Status)
	assert.JSONEq(t, string(content), string(final.Content))

	// Nor does a late finalize: the turn must not become complete (and its
	// tool calls approvable) after the sweep reported it lost.
	require.NoError(t, s.FinalizeStreamingTurn(turn.ID, json.RawMessage(`[{"type":"text","text":"late"}]`), 1, 1, TurnStatusComplete))
	final = getTurn(t, s, turn.ID)
	assert.Equal(t, TurnStatusInterrupted, final.Status)
	assert.JSONEq(t, string(content), string(final.Content))
}

func TestFinalizeStreamingTurn(t *testing.T) {
	tests := []struct {
		name         string
		appendAfter  int
		status       string
		wantLastTurn bool
	}{
		{name: "no later turns keeps sequence", appendAfter: 0, status: TurnStatusComplete, wantLastTurn: true},
		{name: "tool turns appended mid-stream moves anchor to end", appendAfter: 2, status: TurnStatusComplete, wantLastTurn: true},
		{name: "interrupted status is persisted", appendAfter: 0, status: TurnStatusInterrupted, wantLastTurn: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, convID := setupStreamStore(t)
			require.NoError(t, s.CreateTurnAutoSequence(makeTurn(convID, 0)))
			turn := createTurnWithStatus(t, s, convID, TurnStatusStreaming)
			for range tc.appendAfter {
				require.NoError(t, s.CreateTurnAutoSequence(makeTurn(convID, 0, func(tu *Turn) { tu.Role = "tool_result" })))
			}

			content := json.RawMessage(`[{"type":"text","text":"done"}]`)
			require.NoError(t, s.FinalizeStreamingTurn(turn.ID, content, 3, 4, tc.status))

			turns, err := s.GetTurnsForConversation(convID)
			require.NoError(t, err)
			last := turns[len(turns)-1]
			assert.Equal(t, tc.wantLastTurn, last.ID == turn.ID)
			got := getTurn(t, s, turn.ID)
			assert.Equal(t, tc.status, got.Status)
			assert.JSONEq(t, string(content), string(got.Content))
			assert.Equal(t, int64(3), got.TokensIn)
			assert.Equal(t, int64(4), got.TokensOut)
		})
	}
}

func TestReleaseStreamingTurnClaim(t *testing.T) {
	s, convID := setupStreamStore(t)
	turn := createTurnWithStatus(t, s, convID, TurnStatusStreaming)
	ageTurn(t, s, turn.ID, 2*testStaleMillis)

	claimed, err := s.ClaimStaleStreamingTurn(turn.ID, testStaleMillis)
	require.NoError(t, err)
	require.True(t, claimed)

	require.NoError(t, s.ReleaseStreamingTurnClaim(turn.ID))
	assert.Equal(t, TurnStatusStreaming, getTurn(t, s, turn.ID).Status)

	// Released turns are claimable again once stale.
	ageTurn(t, s, turn.ID, 2*testStaleMillis)
	claimed, err = s.ClaimStaleStreamingTurn(turn.ID, testStaleMillis)
	require.NoError(t, err)
	assert.True(t, claimed)

	// Completed turns are never reverted.
	done := createTurnWithStatus(t, s, convID, TurnStatusComplete)
	require.NoError(t, s.ReleaseStreamingTurnClaim(done.ID))
	assert.Equal(t, TurnStatusComplete, getTurn(t, s, done.ID).Status)
}

func TestClaimStaleStreamingTurnMovesAfterToolRounds(t *testing.T) {
	s, convID := setupStreamStore(t)
	turn := createTurnWithStatus(t, s, convID, TurnStatusStreaming)
	// Tool rounds persisted while the response was still streaming.
	for range 2 {
		require.NoError(t, s.CreateTurnAutoSequence(makeTurn(convID, 0, func(tu *Turn) { tu.Role = "tool_result" })))
	}
	ageTurn(t, s, turn.ID, 2*testStaleMillis)

	claimed, err := s.ClaimStaleStreamingTurn(turn.ID, testStaleMillis)
	require.NoError(t, err)
	require.True(t, claimed)

	turns, err := s.GetTurnsForConversation(convID)
	require.NoError(t, err)
	require.Equal(t, turn.ID, turns[len(turns)-1].ID, "the interrupted response stays after its tool rounds")
	assert.Equal(t, TurnStatusInterrupted, turns[len(turns)-1].Status)
}
