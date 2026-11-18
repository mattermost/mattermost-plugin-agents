// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"encoding/json"
	"strconv"
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

// toolRoundOf is a tool_result turn written by the run of the response post.
func toolRoundOf(postID string) func(*Turn) {
	return func(tu *Turn) {
		tu.Role = "tool_result"
		tu.RunPostID = &postID
	}
}

func getTurn(t *testing.T, s *Store, id string) Turn {
	t.Helper()
	var turn Turn
	require.NoError(t, s.db.Get(&turn, `SELECT ID, ConversationID, PostID, Role, Content, TokensIn, TokensOut, Sequence, CreatedAt, Status, UpdatedAt, RunPostID FROM LLM_Turns WHERE ID = $1`, id))
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

func TestHasLiveStreamingTurn(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		ageMs    int64
		wantLive bool
	}{
		{name: "fresh streaming turn is live", status: TurnStatusStreaming, wantLive: true},
		{name: "stale streaming turn is not live", status: TurnStatusStreaming, ageMs: 2 * testStaleMillis},
		{name: "complete turn is not live", status: TurnStatusComplete},
		{name: "interrupted turn is not live", status: TurnStatusInterrupted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, convID := setupStreamStore(t)
			turn := createTurnWithStatus(t, s, convID, tc.status)
			if tc.ageMs != 0 {
				ageTurn(t, s, turn.ID, tc.ageMs)
			}

			live, err := s.HasLiveStreamingTurn(*turn.PostID, testStaleMillis)
			require.NoError(t, err)
			assert.Equal(t, tc.wantLive, live)

			live, err = s.HasLiveStreamingTurn("other-post", testStaleMillis)
			require.NoError(t, err)
			assert.False(t, live)
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
		wg.Go(func() {
			<-start
			results[i], errs[i] = s.ClaimStaleStreamingTurn(turn.ID, testStaleMillis)
		})
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
	alive, err := s.HeartbeatStreamingTurn(turn.ID, content)
	require.NoError(t, err)
	assert.True(t, alive)

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
	alive, err = s.HeartbeatStreamingTurn(turn.ID, json.RawMessage(`[]`))
	require.NoError(t, err)
	assert.False(t, alive, "a heartbeat on a claimed turn reports it lost")
	final := getTurn(t, s, turn.ID)
	assert.Equal(t, TurnStatusInterrupted, final.Status)
	assert.JSONEq(t, string(content), string(final.Content))

	// Nor does a late finalize: the turn must not become complete (and its
	// tool calls approvable) after the sweep reported it lost.
	settled, err := s.FinalizeStreamingTurn(turn.ID, json.RawMessage(`[{"type":"text","text":"late"}]`), 1, 1, TurnStatusComplete)
	require.NoError(t, err)
	assert.False(t, settled, "a late finalize reports the turn lost")
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
				require.NoError(t, s.CreateTurnAutoSequence(makeTurn(convID, 0, toolRoundOf("post-streaming"))))
			}

			content := json.RawMessage(`[{"type":"text","text":"done"}]`)
			settled, err := s.FinalizeStreamingTurn(turn.ID, content, 3, 4, tc.status)
			require.NoError(t, err)
			require.True(t, settled)

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

	// A released turn is not live and can be claimed again at once.
	live, err := s.HasLiveStreamingTurn(*turn.PostID, testStaleMillis)
	require.NoError(t, err)
	assert.False(t, live)
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
		require.NoError(t, s.CreateTurnAutoSequence(makeTurn(convID, 0, toolRoundOf("post-streaming"))))
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

func TestSettlingStreamingTurnKeepsLaterUserTurnsInPlace(t *testing.T) {
	tests := []struct {
		name   string
		settle func(t *testing.T, s *Store, id string)
	}{
		{name: "sweep claims a dead node's turn", settle: func(t *testing.T, s *Store, id string) {
			ageTurn(t, s, id, 2*testStaleMillis)
			claimed, err := s.ClaimStaleStreamingTurn(id, testStaleMillis)
			require.NoError(t, err)
			require.True(t, claimed)
		}},
		{name: "stream finalizes", settle: func(t *testing.T, s *Store, id string) {
			_, err := s.FinalizeStreamingTurn(id, json.RawMessage(`[]`), 0, 0, TurnStatusComplete)
			require.NoError(t, err)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, convID := setupStreamStore(t)
			appendTurn := func(role string) string {
				turn := makeTurn(convID, 0, func(tu *Turn) { tu.Role = role })
				require.NoError(t, s.CreateTurnAutoSequence(turn))
				return turn.ID
			}
			first := appendTurn("user")
			anchor := createTurnWithStatus(t, s, convID, TurnStatusStreaming)
			toolRoundTurn := makeTurn(convID, 0, toolRoundOf("post-streaming"))
			require.NoError(t, s.CreateTurnAutoSequence(toolRoundTurn))
			toolRound := toolRoundTurn.ID
			followUp := appendTurn("user")
			followUpReply := appendTurn("assistant")

			tc.settle(t, s, anchor.ID)

			turns, err := s.GetTurnsForConversation(convID)
			require.NoError(t, err)
			var order []string
			for _, turn := range turns {
				order = append(order, turn.ID)
			}
			assert.Equal(t, []string{first, toolRound, anchor.ID, followUp, followUpReply}, order)
		})
	}
}

func TestConcurrentSettlesInOneConversation(t *testing.T) {
	s, convID := setupStreamStore(t)
	const streams = 6
	ids := make([]string, streams)
	for i := range ids {
		postID := "post-" + strconv.Itoa(i)
		turn := makeTurn(convID, 0, func(tu *Turn) { tu.Role = "assistant"; tu.Status = TurnStatusStreaming; tu.PostID = &postID })
		require.NoError(t, s.CreateTurnAutoSequence(turn))
		require.NoError(t, s.CreateTurnAutoSequence(makeTurn(convID, 0, toolRoundOf(postID))))
		ids[i] = turn.ID
	}

	var wg sync.WaitGroup
	errs := make([]error, streams)
	start := make(chan struct{})
	for i, id := range ids {
		wg.Go(func() {
			<-start
			_, errs[i] = s.FinalizeStreamingTurn(id, json.RawMessage(`[]`), 0, 0, TurnStatusComplete)
		})
	}
	close(start)
	wg.Wait()

	for i, id := range ids {
		require.NoError(t, errs[i])
		assert.Equal(t, TurnStatusComplete, getTurn(t, s, id).Status)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestRunSpanSkipsAnotherPostsToolResult pins that a tool_result a channel
// approval appended for an earlier post is not treated as part of a later
// post's run: settling that run does not move past it, and regenerating the
// later post does not delete it.
func TestRunSpanSkipsAnotherPostsToolResult(t *testing.T) {
	block := func(kind, id string) string {
		if kind == "tool_use" {
			return `[{"type":"tool_use","id":"` + id + `","name":"x"}]`
		}
		return `[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]`
	}
	tests := []struct {
		name string
		// foreignResultInSpan places the earlier post's result between the
		// later post's user turn and its anchor instead of after its run.
		foreignResultInSpan bool
		// foreignBeforeOwnRounds lands the earlier post's result after the
		// later post's anchor but before that run's own tool rounds.
		foreignBeforeOwnRounds bool
	}{
		{name: "earlier post's result appended after the run"},
		{name: "earlier post's result inside the scrubbed span", foreignResultInSpan: true},
		{name: "earlier post's result between the anchor and the run's own rounds", foreignBeforeOwnRounds: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, convID := setupStreamStore(t)
			add := func(role, postID, content, status string, runPostID ...string) string {
				turn := makeTurn(convID, 0, func(tu *Turn) {
					tu.Role = role
					tu.Content = json.RawMessage(content)
					if postID != "" {
						tu.PostID = &postID
					}
					if len(runPostID) > 0 {
						tu.RunPostID = &runPostID[0]
					}
					if status != "" {
						tu.Status = status
					}
				})
				require.NoError(t, s.CreateTurnAutoSequence(turn))
				return turn.ID
			}
			add("user", "", `[]`, "")
			earlierAnchor := add("assistant", "post-1", block("tool_use", "tc-1"), "")
			add("user", "", `[]`, "")
			var foreign string
			if tt.foreignResultInSpan {
				foreign = add("tool_result", "", block("tool_result", "tc-1"), "", "post-1")
			}
			anchor := add("assistant", "post-2", `[]`, TurnStatusStreaming)
			if tt.foreignBeforeOwnRounds {
				foreign = add("tool_result", "", block("tool_result", "tc-1"), "", "post-1")
			}
			add("assistant", "", block("tool_use", "tc-2"), "", "post-2")
			add("tool_result", "", block("tool_result", "tc-2"), "", "post-2")
			if !tt.foreignResultInSpan && !tt.foreignBeforeOwnRounds {
				foreign = add("tool_result", "", block("tool_result", "tc-1"), "", "post-1")
			}

			settled, err := s.FinalizeStreamingTurn(anchor, json.RawMessage(`[]`), 0, 0, TurnStatusComplete)
			require.NoError(t, err)
			require.True(t, settled)
			turns, err := s.GetTurnsForConversation(convID)
			require.NoError(t, err)
			index := map[string]int{}
			for i, turn := range turns {
				index[turn.ID] = i
			}
			if !tt.foreignResultInSpan && !tt.foreignBeforeOwnRounds {
				assert.Less(t, index[anchor], index[foreign], "the run must not move past another post's result")
			}
			assert.Equal(t, len(turns)-1-boolToInt(!tt.foreignResultInSpan && !tt.foreignBeforeOwnRounds), index[anchor],
				"the anchor ends after its run's own tool rounds")

			require.NoError(t, s.DeleteResponseTurns(convID, "post-2"))
			turns, err = s.GetTurnsForConversation(convID)
			require.NoError(t, err)
			var kept []string
			for _, turn := range turns {
				kept = append(kept, turn.ID)
			}
			assert.Contains(t, kept, foreign, "regenerating a later post must keep another post's tool result")
			assert.Contains(t, kept, earlierAnchor)
			assert.NotContains(t, kept, anchor)
			assert.Len(t, kept, 4, "only the later post's run is scrubbed")
		})
	}
}

// Two replies streaming in one conversation: the first run's tool rounds land
// after the second reply's anchor, and the first anchor must still settle
// after them.
func TestSettleMovesPastRoundsWrittenAfterAnotherStreamingAnchor(t *testing.T) {
	s, convID := setupStreamStore(t)
	add := func(mutate func(*Turn)) *Turn {
		turn := makeTurn(convID, 0, mutate)
		require.NoError(t, s.CreateTurnAutoSequence(turn))
		return turn
	}
	add(func(tu *Turn) { tu.Role = "user" })
	postA, postB := "post-a", "post-b"
	anchorA := add(func(tu *Turn) { tu.Role = "assistant"; tu.Status = TurnStatusStreaming; tu.PostID = &postA })
	anchorB := add(func(tu *Turn) { tu.Role = "assistant"; tu.Status = TurnStatusStreaming; tu.PostID = &postB })
	roundA := add(toolRoundOf(postA))

	settled, err := s.FinalizeStreamingTurn(anchorA.ID, json.RawMessage(`[]`), 0, 0, TurnStatusComplete)
	require.NoError(t, err)
	require.True(t, settled)

	turns, err := s.GetTurnsForConversation(convID)
	require.NoError(t, err)
	index := map[string]int{}
	for i, turn := range turns {
		index[turn.ID] = i
	}
	assert.Less(t, index[roundA.ID], index[anchorA.ID], "the first answer comes after its own tool rounds")
	assert.Contains(t, index, anchorB.ID)
}

func toolStatuses(t *testing.T, turn Turn) map[string]string {
	t.Helper()
	var blocks []struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(turn.Content, &blocks))
	out := map[string]string{}
	for _, b := range blocks {
		if b.Type == "tool_use" {
			out[b.ID] = b.Status
		}
	}
	return out
}

// An approval run's claimed calls have an unknown outcome once its turn is
// interrupted, however it got there, and are claimed again when the claim is
// released for a retry.
func TestInterruptingATurnMarksItsClaimedToolCalls(t *testing.T) {
	claimed := json.RawMessage(`[{"type":"text","text":"go"},
{"type":"tool_use","id":"run","name":"a","status":"accepted"},
{"type":"tool_use","id":"no","name":"b","status":"rejected"},
{"type":"tool_use","id":"done","name":"c","status":"success"}]`)
	want := map[string]string{"run": "interrupted", "no": "rejected", "done": "success"}
	tests := []struct {
		name      string
		interrupt func(t *testing.T, s *Store, turn *Turn)
	}{
		{name: "transition", interrupt: func(t *testing.T, s *Store, turn *Turn) {
			moved, err := s.TransitionTurnStatus(turn.ID, TurnStatusStreaming, TurnStatusInterrupted)
			require.NoError(t, err)
			require.True(t, moved)
		}},
		{name: "recovery sweep claim", interrupt: func(t *testing.T, s *Store, turn *Turn) {
			ageTurn(t, s, turn.ID, 2*testStaleMillis)
			ok, err := s.ClaimStaleStreamingTurn(turn.ID, testStaleMillis)
			require.NoError(t, err)
			require.True(t, ok)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, convID := setupStreamStore(t)
			turn := makeTurn(convID, 0, func(tu *Turn) {
				tu.Role = "assistant"
				tu.Status = TurnStatusStreaming
				tu.Content = claimed
				postID := "post-run"
				tu.PostID = &postID
			})
			require.NoError(t, s.CreateTurnAutoSequence(turn))

			tc.interrupt(t, s, turn)
			assert.Equal(t, want, toolStatuses(t, getTurn(t, s, turn.ID)))

			require.NoError(t, s.ReleaseStreamingTurnClaim(turn.ID))
			released := getTurn(t, s, turn.ID)
			assert.Equal(t, TurnStatusStreaming, released.Status)
			assert.Equal(t, map[string]string{"run": "accepted", "no": "rejected", "done": "success"}, toolStatuses(t, released))
		})
	}
}

// Scrubbing a response keeps the calls whose outcome is unknown, detached from
// the post, so a regenerate that fails after the scrub does not lose them.
func TestDeleteResponseTurnsKeepsInterruptedToolCalls(t *testing.T) {
	s, convID := setupStreamStore(t)
	require.NoError(t, s.CreateTurnAutoSequence(makeTurn(convID, 0, func(tu *Turn) { tu.Role = "user" })))
	postID := "post-run"
	anchor := makeTurn(convID, 0, func(tu *Turn) {
		tu.Role = "assistant"
		tu.PostID = &postID
		tu.Status = TurnStatusInterrupted
		tu.Content = json.RawMessage(`[{"type":"text","text":"go"},
{"type":"tool_use","id":"lost","name":"a","status":"interrupted"},
{"type":"tool_use","id":"no","name":"b","status":"rejected"}]`)
	})
	require.NoError(t, s.CreateTurnAutoSequence(anchor))

	require.NoError(t, s.DeleteResponseTurns(convID, postID))

	turns, err := s.GetTurnsForConversation(convID)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	kept := turns[1]
	assert.Equal(t, anchor.ID, kept.ID)
	assert.Nil(t, kept.PostID)
	assert.Equal(t, TurnStatusComplete, kept.Status)
	assert.Equal(t, map[string]string{"lost": "interrupted"}, toolStatuses(t, kept))
}
