// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTurn(conversationID string, sequence int, overrides ...func(*Turn)) *Turn {
	turn := &Turn{
		ID:             model.NewId(),
		ConversationID: conversationID,
		Role:           "user",
		Content:        json.RawMessage(`[{"type":"text","text":"test message"}]`),
		TokensIn:       0,
		TokensOut:      0,
		Sequence:       sequence,
		CreatedAt:      model.GetMillis(),
	}
	for _, fn := range overrides {
		fn(turn)
	}
	return turn
}

func TestCreateTurn(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store) string // returns conversationID
		validate func(t *testing.T, s *Store, convID string)
	}{
		{
			name: "creates turn with JSONB content",
			setup: func(t *testing.T, s *Store) string {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)
				return conv.ID
			},
			validate: func(t *testing.T, s *Store, convID string) {
				content := json.RawMessage(`[{"type":"text","text":"hello"}]`)
				turn := makeTurn(convID, 1, func(tu *Turn) {
					tu.Content = content
				})
				err := s.CreateTurn(turn)
				require.NoError(t, err)

				turns, err := s.GetTurnsForConversation(convID)
				require.NoError(t, err)
				require.Len(t, turns, 1)
				assert.JSONEq(t, string(content), string(turns[0].Content))
			},
		},
		{
			name: "creates turn with nil PostID",
			setup: func(t *testing.T, s *Store) string {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)
				return conv.ID
			},
			validate: func(t *testing.T, s *Store, convID string) {
				turn := makeTurn(convID, 1)
				err := s.CreateTurn(turn)
				require.NoError(t, err)

				turns, err := s.GetTurnsForConversation(convID)
				require.NoError(t, err)
				require.Len(t, turns, 1)
				assert.Nil(t, turns[0].PostID)
			},
		},
		{
			name: "creates turn with non-nil PostID",
			setup: func(t *testing.T, s *Store) string {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)
				return conv.ID
			},
			validate: func(t *testing.T, s *Store, convID string) {
				turn := makeTurn(convID, 1, func(tu *Turn) {
					tu.PostID = stringPtr("post123")
				})
				err := s.CreateTurn(turn)
				require.NoError(t, err)

				turns, err := s.GetTurnsForConversation(convID)
				require.NoError(t, err)
				require.Len(t, turns, 1)
				require.NotNil(t, turns[0].PostID)
				assert.Equal(t, "post123", *turns[0].PostID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)

			err := s.RunMigrations()
			require.NoError(t, err)

			convID := tt.setup(t, s)
			tt.validate(t, s, convID)
		})
	}
}

func TestGetTurnsForConversation(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store) string // returns conversationID
		validate func(t *testing.T, s *Store, convID string)
	}{
		{
			name: "returns turns ordered by Sequence",
			setup: func(t *testing.T, s *Store) string {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				// Insert in scrambled order: 3, 1, 2
				turn3 := makeTurn(conv.ID, 3, func(tu *Turn) {
					tu.Content = json.RawMessage(`[{"type":"text","text":"third"}]`)
				})
				err = s.CreateTurn(turn3)
				require.NoError(t, err)

				turn1 := makeTurn(conv.ID, 1, func(tu *Turn) {
					tu.Content = json.RawMessage(`[{"type":"text","text":"first"}]`)
				})
				err = s.CreateTurn(turn1)
				require.NoError(t, err)

				turn2 := makeTurn(conv.ID, 2, func(tu *Turn) {
					tu.Content = json.RawMessage(`[{"type":"text","text":"second"}]`)
				})
				err = s.CreateTurn(turn2)
				require.NoError(t, err)

				return conv.ID
			},
			validate: func(t *testing.T, s *Store, convID string) {
				turns, err := s.GetTurnsForConversation(convID)
				require.NoError(t, err)
				require.Len(t, turns, 3)
				assert.Equal(t, 1, turns[0].Sequence)
				assert.Equal(t, 2, turns[1].Sequence)
				assert.Equal(t, 3, turns[2].Sequence)
			},
		},
		{
			name: "returns empty slice for nonexistent conversation",
			setup: func(t *testing.T, s *Store) string {
				return "nonexistent"
			},
			validate: func(t *testing.T, s *Store, convID string) {
				turns, err := s.GetTurnsForConversation(convID)
				require.NoError(t, err)
				assert.Equal(t, []Turn{}, turns)
			},
		},
		{
			name: "returns empty slice not nil for conversation with no turns",
			setup: func(t *testing.T, s *Store) string {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)
				return conv.ID
			},
			validate: func(t *testing.T, s *Store, convID string) {
				turns, err := s.GetTurnsForConversation(convID)
				require.NoError(t, err)
				assert.NotNil(t, turns)
				assert.Equal(t, []Turn{}, turns)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)

			err := s.RunMigrations()
			require.NoError(t, err)

			convID := tt.setup(t, s)
			tt.validate(t, s, convID)
		})
	}
}

func TestUpdateTurnContent(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store) (convID, turnID string)
		validate func(t *testing.T, s *Store, convID, turnID string)
	}{
		{
			name: "replaces JSONB content",
			setup: func(t *testing.T, s *Store) (string, string) {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				turn := makeTurn(conv.ID, 1, func(tu *Turn) {
					tu.Content = json.RawMessage(`[{"type":"text","text":"original"}]`)
				})
				err = s.CreateTurn(turn)
				require.NoError(t, err)

				return conv.ID, turn.ID
			},
			validate: func(t *testing.T, s *Store, convID, turnID string) {
				newContent := json.RawMessage(`[{"type":"text","text":"updated"}]`)
				err := s.UpdateTurnContent(turnID, newContent)
				require.NoError(t, err)

				turns, err := s.GetTurnsForConversation(convID)
				require.NoError(t, err)
				require.Len(t, turns, 1)
				assert.JSONEq(t, string(newContent), string(turns[0].Content))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)

			err := s.RunMigrations()
			require.NoError(t, err)

			convID, turnID := tt.setup(t, s)
			tt.validate(t, s, convID, turnID)
		})
	}
}

func TestUpdateTurnTokens(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store) (convID, turnID string)
		validate func(t *testing.T, s *Store, convID, turnID string)
	}{
		{
			name: "sets TokensIn and TokensOut",
			setup: func(t *testing.T, s *Store) (string, string) {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				turn := makeTurn(conv.ID, 1)
				err = s.CreateTurn(turn)
				require.NoError(t, err)

				return conv.ID, turn.ID
			},
			validate: func(t *testing.T, s *Store, convID, turnID string) {
				err := s.UpdateTurnTokens(turnID, 1500, 200)
				require.NoError(t, err)

				turns, err := s.GetTurnsForConversation(convID)
				require.NoError(t, err)
				require.Len(t, turns, 1)
				assert.Equal(t, int64(1500), turns[0].TokensIn)
				assert.Equal(t, int64(200), turns[0].TokensOut)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)

			err := s.RunMigrations()
			require.NoError(t, err)

			convID, turnID := tt.setup(t, s)
			tt.validate(t, s, convID, turnID)
		})
	}
}

func TestGetMaxSequenceForConversation(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store) string // returns conversationID
		validate func(t *testing.T, s *Store, convID string)
	}{
		{
			name: "returns 0 for conversation with no turns",
			setup: func(t *testing.T, s *Store) string {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)
				return conv.ID
			},
			validate: func(t *testing.T, s *Store, convID string) {
				maxSeq, err := s.GetMaxSequenceForConversation(convID)
				require.NoError(t, err)
				assert.Equal(t, 0, maxSeq)
			},
		},
		{
			name: "returns 0 for nonexistent conversation",
			setup: func(t *testing.T, s *Store) string {
				return "nonexistent"
			},
			validate: func(t *testing.T, s *Store, convID string) {
				maxSeq, err := s.GetMaxSequenceForConversation(convID)
				require.NoError(t, err)
				assert.Equal(t, 0, maxSeq)
			},
		},
		{
			name: "returns correct max after multiple turns",
			setup: func(t *testing.T, s *Store) string {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				for i := 1; i <= 5; i++ {
					turn := makeTurn(conv.ID, i)
					err = s.CreateTurn(turn)
					require.NoError(t, err)
				}
				return conv.ID
			},
			validate: func(t *testing.T, s *Store, convID string) {
				maxSeq, err := s.GetMaxSequenceForConversation(convID)
				require.NoError(t, err)
				assert.Equal(t, 5, maxSeq)
			},
		},
		{
			name: "returns max even with non-contiguous sequences",
			setup: func(t *testing.T, s *Store) string {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				for _, seq := range []int{1, 3, 7} {
					turn := makeTurn(conv.ID, seq)
					err = s.CreateTurn(turn)
					require.NoError(t, err)
				}
				return conv.ID
			},
			validate: func(t *testing.T, s *Store, convID string) {
				maxSeq, err := s.GetMaxSequenceForConversation(convID)
				require.NoError(t, err)
				assert.Equal(t, 7, maxSeq)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)

			err := s.RunMigrations()
			require.NoError(t, err)

			convID := tt.setup(t, s)
			tt.validate(t, s, convID)
		})
	}
}

func TestGetTurnByPostID(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store) string // returns conversationID
		validate func(t *testing.T, s *Store, convID string)
	}{
		{
			name: "returns turn matching post ID",
			setup: func(t *testing.T, s *Store) string {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				turn := makeTurn(conv.ID, 1, func(tu *Turn) {
					tu.PostID = stringPtr("target-post-id")
					tu.Content = json.RawMessage(`[{"type":"text","text":"found me"}]`)
				})
				err = s.CreateTurn(turn)
				require.NoError(t, err)

				return conv.ID
			},
			validate: func(t *testing.T, s *Store, _ string) {
				turn, err := s.GetTurnByPostID("target-post-id")
				require.NoError(t, err)
				require.NotNil(t, turn)
				assert.JSONEq(t, `[{"type":"text","text":"found me"}]`, string(turn.Content))
				require.NotNil(t, turn.PostID)
				assert.Equal(t, "target-post-id", *turn.PostID)
			},
		},
		{
			name: "returns nil for non-existent post ID",
			setup: func(t *testing.T, s *Store) string {
				return ""
			},
			validate: func(t *testing.T, s *Store, _ string) {
				turn, err := s.GetTurnByPostID("nonexistent")
				require.NoError(t, err)
				assert.Nil(t, turn)
			},
		},
		{
			name: "does not match turns with nil post ID",
			setup: func(t *testing.T, s *Store) string {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				turn := makeTurn(conv.ID, 1) // PostID is nil by default
				err = s.CreateTurn(turn)
				require.NoError(t, err)

				return conv.ID
			},
			validate: func(t *testing.T, s *Store, _ string) {
				turn, err := s.GetTurnByPostID("anything")
				require.NoError(t, err)
				assert.Nil(t, turn)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)

			err := s.RunMigrations()
			require.NoError(t, err)

			convID := tt.setup(t, s)
			tt.validate(t, s, convID)
		})
	}
}

func TestTurnCleanupWithConversation(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store)
	}{
		{
			name:  "CleanupDeletedConversations removes associated turns",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				for i := 1; i <= 3; i++ {
					turn := makeTurn(conv.ID, i)
					err = s.CreateTurn(turn)
					require.NoError(t, err)
				}

				err = s.SoftDeleteConversation(conv.ID, model.GetMillis())
				require.NoError(t, err)

				err = s.CleanupDeletedConversations()
				require.NoError(t, err)

				turns, err := s.GetTurnsForConversation(conv.ID)
				require.NoError(t, err)
				assert.Equal(t, []Turn{}, turns)

				var count int
				err = s.db.Get(&count, "SELECT COUNT(*) FROM LLM_Turns WHERE ConversationID = $1", conv.ID)
				require.NoError(t, err)
				assert.Equal(t, 0, count)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)

			err := s.RunMigrations()
			require.NoError(t, err)

			tt.setup(t, s)
			tt.validate(t, s)
		})
	}
}
