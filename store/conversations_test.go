// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeConversation(overrides ...func(*Conversation)) *Conversation {
	conv := &Conversation{
		ID:        model.NewId(),
		UserID:    model.NewId(),
		BotID:     model.NewId(),
		Title:     "",
		Operation: "conversation",
		CreatedAt: model.GetMillis(),
		UpdatedAt: model.GetMillis(),
		DeleteAt:  0,
	}
	for _, fn := range overrides {
		fn(conv)
	}
	return conv
}

func TestCreateConversation(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store)
	}{
		{
			name:  "creates conversation with all fields",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.ChannelID = new("channel1")
					c.RootPostID = new("post1")
					c.Title = "Test Title"
					c.SystemPrompt = "You are a helpful assistant"
					c.Operation = "conversation"
				})

				err := s.CreateConversation(conv)
				require.NoError(t, err)

				got, err := s.GetConversation(conv.ID)
				require.NoError(t, err)
				assert.Equal(t, conv.ID, got.ID)
				assert.Equal(t, conv.UserID, got.UserID)
				assert.Equal(t, conv.BotID, got.BotID)
				assert.Equal(t, conv.ChannelID, got.ChannelID)
				assert.Equal(t, conv.RootPostID, got.RootPostID)
				assert.Equal(t, conv.Title, got.Title)
				assert.Equal(t, conv.SystemPrompt, got.SystemPrompt)
				assert.Equal(t, conv.Operation, got.Operation)
				assert.Equal(t, conv.CreatedAt, got.CreatedAt)
				assert.Equal(t, conv.UpdatedAt, got.UpdatedAt)
				assert.Equal(t, conv.DeleteAt, got.DeleteAt)
			},
		},
		{
			name:  "creates conversation with nil optional fields",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation()

				err := s.CreateConversation(conv)
				require.NoError(t, err)

				got, err := s.GetConversation(conv.ID)
				require.NoError(t, err)
				assert.Nil(t, got.ChannelID)
				assert.Nil(t, got.RootPostID)
			},
		},
		{
			name: "duplicate RootPostID+BotID+UserID returns conflict error",
			setup: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.UserID = "userDup"
					c.RootPostID = new("post1")
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.UserID = "userDup"
					c.RootPostID = new("post1")
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				assert.ErrorIs(t, err, ErrConversationConflict)
			},
		},
		{
			name: "allows same RootPostID+BotID with different UserID",
			setup: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.UserID = "userA"
					c.RootPostID = new("post1")
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.UserID = "userB"
					c.RootPostID = new("post1")
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				got, err := s.GetConversation(conv.ID)
				require.NoError(t, err)
				assert.Equal(t, conv.ID, got.ID)
			},
		},
		{
			name: "allows same RootPostID with different BotID",
			setup: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.RootPostID = new("post1")
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.RootPostID = new("post1")
					c.BotID = "bot2"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				got, err := s.GetConversation(conv.ID)
				require.NoError(t, err)
				assert.Equal(t, conv.ID, got.ID)
			},
		},
		{
			name: "allows multiple nil RootPostID rows",
			setup: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				assert.NoError(t, err)
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

func TestGetConversation(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store)
	}{
		{
			name: "returns conversation by ID",
			setup: func(t *testing.T, s *Store) {
				// Created via validate to capture the ID
			},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.Title = "Test Conversation"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				got, err := s.GetConversation(conv.ID)
				require.NoError(t, err)
				assert.Equal(t, conv.ID, got.ID)
				assert.Equal(t, conv.Title, got.Title)
			},
		},
		{
			name:  "returns ErrConversationNotFound for nonexistent ID",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				got, err := s.GetConversation("nonexistent")
				assert.Nil(t, got)
				assert.ErrorIs(t, err, ErrConversationNotFound)
			},
		},
		{
			name:  "does not return soft-deleted conversation",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				err = s.SoftDeleteConversation(conv.ID, model.GetMillis())
				require.NoError(t, err)

				got, err := s.GetConversation(conv.ID)
				assert.Nil(t, got)
				assert.ErrorIs(t, err, ErrConversationNotFound)
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

func TestGetConversationByThreadBotUser(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store)
	}{
		{
			name:  "returns conversation by RootPostID, BotID, and UserID",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				userID := model.NewId()
				conv := makeConversation(func(c *Conversation) {
					c.UserID = userID
					c.RootPostID = new("post1")
					c.BotID = "bot1"
					c.Title = "Thread Conversation"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				got, err := s.GetConversationByThreadBotUser("post1", "bot1", userID)
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, conv.ID, got.ID)
				assert.Equal(t, conv.Title, got.Title)
			},
		},
		{
			name:  "returns ErrConversationNotFound for nonexistent tuple",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				got, err := s.GetConversationByThreadBotUser("nonexistent", "nonexistent", "nonexistent")
				assert.ErrorIs(t, err, ErrConversationNotFound)
				assert.Nil(t, got)
			},
		},
		{
			name:  "does not return conversation owned by a different user",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				ownerID := model.NewId()
				otherID := model.NewId()
				conv := makeConversation(func(c *Conversation) {
					c.UserID = ownerID
					c.RootPostID = new("post1")
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				got, err := s.GetConversationByThreadBotUser("post1", "bot1", otherID)
				assert.ErrorIs(t, err, ErrConversationNotFound)
				assert.Nil(t, got)
			},
		},
		{
			name:  "does not return soft-deleted conversation",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				userID := model.NewId()
				conv := makeConversation(func(c *Conversation) {
					c.UserID = userID
					c.RootPostID = new("post1")
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				err = s.SoftDeleteConversation(conv.ID, model.GetMillis())
				require.NoError(t, err)

				got, err := s.GetConversationByThreadBotUser("post1", "bot1", userID)
				assert.ErrorIs(t, err, ErrConversationNotFound)
				assert.Nil(t, got)
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

func TestUpdateConversationTitle(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store)
	}{
		{
			name:  "updates title without touching UpdatedAt",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				err = s.UpdateConversationTitle(conv.ID, "New Title")
				require.NoError(t, err)

				got, err := s.GetConversation(conv.ID)
				require.NoError(t, err)
				assert.Equal(t, "New Title", got.Title)
				assert.Equal(t, conv.UpdatedAt, got.UpdatedAt, "a title is not activity")
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

func TestSoftDeleteConversation(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store)
	}{
		{
			name:  "sets DeleteAt on conversation",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				now := model.GetMillis()
				err = s.SoftDeleteConversation(conv.ID, now)
				require.NoError(t, err)

				// Direct DB query to verify DeleteAt was set
				var deleteAt int64
				err = s.db.Get(&deleteAt, "SELECT DeleteAt FROM LLM_Conversations WHERE ID = $1", conv.ID)
				require.NoError(t, err)
				assert.Equal(t, now, deleteAt)

				// GetConversation should not return it
				got, err := s.GetConversation(conv.ID)
				assert.Nil(t, got)
				assert.ErrorIs(t, err, ErrConversationNotFound)
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

func TestGetConversationSummariesForUser(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store)
	}{
		{
			name:  "returns empty slice for user with no conversations",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				summaries, err := s.GetConversationSummariesForUser("nonexistent_user", 60, 0)
				require.NoError(t, err)
				assert.NotNil(t, summaries)
				assert.Empty(t, summaries)
			},
		},
		{
			name: "returns conversations ordered by UpdatedAt DESC",
			setup: func(t *testing.T, s *Store) {
			},
			validate: func(t *testing.T, s *Store) {
				userID := model.NewId()

				conv1 := makeConversation(func(c *Conversation) {
					c.UserID = userID
					c.Title = "Older"
					c.UpdatedAt = 1000
				})
				err := s.CreateConversation(conv1)
				require.NoError(t, err)

				conv2 := makeConversation(func(c *Conversation) {
					c.UserID = userID
					c.Title = "Newer"
					c.UpdatedAt = 2000
				})
				err = s.CreateConversation(conv2)
				require.NoError(t, err)

				summaries, err := s.GetConversationSummariesForUser(userID, 60, 0)
				require.NoError(t, err)
				require.Len(t, summaries, 2)
				assert.Equal(t, "Newer", summaries[0].Title)
				assert.Equal(t, "Older", summaries[1].Title)
			},
		},
		{
			name:  "excludes soft-deleted conversations",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				userID := model.NewId()

				conv1 := makeConversation(func(c *Conversation) {
					c.UserID = userID
					c.Title = "Active"
				})
				err := s.CreateConversation(conv1)
				require.NoError(t, err)

				conv2 := makeConversation(func(c *Conversation) {
					c.UserID = userID
					c.Title = "Deleted"
				})
				err = s.CreateConversation(conv2)
				require.NoError(t, err)

				err = s.SoftDeleteConversation(conv2.ID, model.GetMillis())
				require.NoError(t, err)

				summaries, err := s.GetConversationSummariesForUser(userID, 60, 0)
				require.NoError(t, err)
				require.Len(t, summaries, 1)
				assert.Equal(t, "Active", summaries[0].Title)
			},
		},
		{
			name:  "respects limit and offset",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				userID := model.NewId()

				for i := range 5 {
					conv := makeConversation(func(c *Conversation) {
						c.UserID = userID
						c.UpdatedAt = int64(1000 + i)
					})
					err := s.CreateConversation(conv)
					require.NoError(t, err)
				}

				// Limit to 2
				summaries, err := s.GetConversationSummariesForUser(userID, 2, 0)
				require.NoError(t, err)
				assert.Len(t, summaries, 2)

				// Offset 2, limit 2
				summaries, err = s.GetConversationSummariesForUser(userID, 2, 2)
				require.NoError(t, err)
				assert.Len(t, summaries, 2)

				// Offset past all results
				summaries, err = s.GetConversationSummariesForUser(userID, 10, 10)
				require.NoError(t, err)
				assert.Empty(t, summaries)
			},
		},
		{
			name:  "returns correct turn count per conversation",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				userID := model.NewId()

				conv1 := makeConversation(func(c *Conversation) {
					c.UserID = userID
					c.Title = "With Turns"
					c.UpdatedAt = 2000
				})
				err := s.CreateConversation(conv1)
				require.NoError(t, err)

				for i := 1; i <= 3; i++ {
					turn := makeTurn(conv1.ID, i)
					err = s.CreateTurn(turn)
					require.NoError(t, err)
				}

				conv2 := makeConversation(func(c *Conversation) {
					c.UserID = userID
					c.Title = "No Turns"
					c.UpdatedAt = 1000
				})
				err = s.CreateConversation(conv2)
				require.NoError(t, err)

				summaries, err := s.GetConversationSummariesForUser(userID, 60, 0)
				require.NoError(t, err)
				require.Len(t, summaries, 2)

				assert.Equal(t, "With Turns", summaries[0].Title)
				assert.Equal(t, 3, summaries[0].TurnCount)

				assert.Equal(t, "No Turns", summaries[1].Title)
				assert.Equal(t, 0, summaries[1].TurnCount)
			},
		},
		{
			name:  "only returns conversations for the specified user",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				userA := model.NewId()
				userB := model.NewId()

				convA := makeConversation(func(c *Conversation) {
					c.UserID = userA
					c.Title = "User A Conv"
				})
				err := s.CreateConversation(convA)
				require.NoError(t, err)

				convB := makeConversation(func(c *Conversation) {
					c.UserID = userB
					c.Title = "User B Conv"
				})
				err = s.CreateConversation(convB)
				require.NoError(t, err)

				summaries, err := s.GetConversationSummariesForUser(userA, 60, 0)
				require.NoError(t, err)
				require.Len(t, summaries, 1)
				assert.Equal(t, "User A Conv", summaries[0].Title)
			},
		},
		{
			name:  "includes RootPostID and BotID in summary",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				userID := model.NewId()
				botID := model.NewId()
				rootPostID := model.NewId()

				conv := makeConversation(func(c *Conversation) {
					c.UserID = userID
					c.BotID = botID
					c.RootPostID = new(rootPostID)
					c.Title = "Thread Conv"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				summaries, err := s.GetConversationSummariesForUser(userID, 60, 0)
				require.NoError(t, err)
				require.Len(t, summaries, 1)
				assert.Equal(t, botID, summaries[0].BotID)
				require.NotNil(t, summaries[0].RootPostID)
				assert.Equal(t, rootPostID, *summaries[0].RootPostID)
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

func TestGetConversationSummariesForUserLastActivity(t *testing.T) {
	type convSpec struct {
		key       string
		user      string // "self" or "other"
		id        string // optional fixed ID, for tie-break cases
		updatedAt int64
		turns     []int64 // CreatedAt of each turn
		deleted   bool
	}
	type wantRow struct {
		key       string
		updatedAt int64
	}

	// Fixed IDs ordered lexically so the tie-break expectation is explicit.
	lowID := "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	highID := "zzzzzzzzzzzzzzzzzzzzzzzzzz"

	tests := []struct {
		name   string
		convs  []convSpec
		limit  int
		offset int
		want   []wantRow
	}{
		{
			name: "a newer turn moves an older conversation first and reports the turn time",
			convs: []convSpec{
				{key: "A", user: "self", updatedAt: 1000, turns: []int64{1000, 5000}},
				{key: "B", user: "self", updatedAt: 3000},
			},
			limit: 60,
			want:  []wantRow{{"A", 5000}, {"B", 3000}},
		},
		{
			name: "a conversation without turns reports its own UpdatedAt",
			convs: []convSpec{
				{key: "A", user: "self", updatedAt: 4000},
				{key: "B", user: "self", updatedAt: 1000, turns: []int64{2000}},
			},
			limit: 60,
			want:  []wantRow{{"A", 4000}, {"B", 2000}},
		},
		{
			name: "a turn older than UpdatedAt does not lower it",
			convs: []convSpec{
				{key: "A", user: "self", updatedAt: 3000, turns: []int64{100, 200}},
				{key: "B", user: "self", updatedAt: 2000, turns: []int64{2500}},
			},
			limit: 60,
			want:  []wantRow{{"A", 3000}, {"B", 2500}},
		},
		{
			name: "ties on last activity are ordered by ID",
			convs: []convSpec{
				{key: "high", user: "self", id: highID, updatedAt: 1000, turns: []int64{3000}},
				{key: "low", user: "self", id: lowID, updatedAt: 3000},
			},
			limit: 60,
			want:  []wantRow{{"low", 3000}, {"high", 3000}},
		},
		{
			name: "another user's conversations never appear even with newer turns",
			convs: []convSpec{
				{key: "continued", user: "self", updatedAt: 1000, turns: []int64{7000}},
				{key: "untouched", user: "self", updatedAt: 2000},
				{key: "theirs", user: "other", updatedAt: 1000, turns: []int64{9000}},
			},
			limit: 60,
			want:  []wantRow{{"continued", 7000}, {"untouched", 2000}},
		},
		{
			name: "deleted conversations are excluded even with newer turns",
			convs: []convSpec{
				{key: "continued", user: "self", updatedAt: 1000, turns: []int64{7000}},
				{key: "untouched", user: "self", updatedAt: 2000},
				{key: "deleted", user: "self", updatedAt: 1000, turns: []int64{9000}, deleted: true},
			},
			limit: 60,
			want:  []wantRow{{"continued", 7000}, {"untouched", 2000}},
		},
		{
			name: "limit and offset apply after ordering by last activity",
			convs: []convSpec{
				{key: "A", user: "self", updatedAt: 5000},
				{key: "B", user: "self", updatedAt: 4000},
				{key: "C", user: "self", updatedAt: 1000, turns: []int64{6000}},
				{key: "D", user: "self", updatedAt: 3000},
			},
			limit:  2,
			offset: 1,
			want:   []wantRow{{"A", 5000}, {"B", 4000}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())

			users := map[string]string{"self": model.NewId(), "other": model.NewId()}
			keyByID := map[string]string{}
			for _, spec := range tt.convs {
				conv := makeConversation(func(c *Conversation) {
					if spec.id != "" {
						c.ID = spec.id
					}
					c.UserID = users[spec.user]
					c.Title = spec.key
					c.CreatedAt = spec.updatedAt
					c.UpdatedAt = spec.updatedAt
				})
				require.NoError(t, s.CreateConversation(conv))
				keyByID[conv.ID] = spec.key
				for i, createdAt := range spec.turns {
					require.NoError(t, s.CreateTurn(makeTurn(conv.ID, i+1, func(turn *Turn) {
						turn.CreatedAt = createdAt
					})))
				}
				if spec.deleted {
					// Soft-delete directly so UpdatedAt is not bumped past the turns.
					_, err := s.db.Exec("UPDATE LLM_Conversations SET DeleteAt = $1 WHERE ID = $2", spec.updatedAt, conv.ID)
					require.NoError(t, err)
				}
			}

			summaries, err := s.GetConversationSummariesForUser(users["self"], tt.limit, tt.offset)
			require.NoError(t, err)

			got := make([]wantRow, len(summaries))
			for i, summary := range summaries {
				got[i] = wantRow{keyByID[summary.ID], summary.UpdatedAt}
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCleanupDeletedConversations(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, s *Store)
		validate func(t *testing.T, s *Store)
	}{
		{
			name:  "removes soft-deleted conversations and their turns",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				// Create two conversations
				conv1 := makeConversation()
				err := s.CreateConversation(conv1)
				require.NoError(t, err)

				conv2 := makeConversation()
				err = s.CreateConversation(conv2)
				require.NoError(t, err)

				// Add turns to both
				turn1 := makeTurn(conv1.ID, 1)
				err = s.CreateTurn(turn1)
				require.NoError(t, err)

				turn2 := makeTurn(conv2.ID, 1)
				err = s.CreateTurn(turn2)
				require.NoError(t, err)

				// Soft-delete conv1
				err = s.SoftDeleteConversation(conv1.ID, model.GetMillis())
				require.NoError(t, err)

				// Cleanup
				err = s.CleanupDeletedConversations()
				require.NoError(t, err)

				// conv1 and its turns should be gone
				var count int
				err = s.db.Get(&count, "SELECT COUNT(*) FROM LLM_Conversations WHERE ID = $1", conv1.ID)
				require.NoError(t, err)
				assert.Equal(t, 0, count)

				err = s.db.Get(&count, "SELECT COUNT(*) FROM LLM_Turns WHERE ConversationID = $1", conv1.ID)
				require.NoError(t, err)
				assert.Equal(t, 0, count)

				// conv2 and its turns should remain
				got, err := s.GetConversation(conv2.ID)
				require.NoError(t, err)
				assert.Equal(t, conv2.ID, got.ID)

				turns, err := s.GetTurnsForConversation(conv2.ID)
				require.NoError(t, err)
				assert.Len(t, turns, 1)
			},
		},
		{
			name:  "no-op when nothing soft-deleted",
			setup: func(t *testing.T, s *Store) {},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation()
				err := s.CreateConversation(conv)
				require.NoError(t, err)

				turn := makeTurn(conv.ID, 1)
				err = s.CreateTurn(turn)
				require.NoError(t, err)

				err = s.CleanupDeletedConversations()
				require.NoError(t, err)

				// Everything still exists
				got, err := s.GetConversation(conv.ID)
				require.NoError(t, err)
				assert.Equal(t, conv.ID, got.ID)

				turns, err := s.GetTurnsForConversation(conv.ID)
				require.NoError(t, err)
				assert.Len(t, turns, 1)
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

func TestRenameConversation(t *testing.T) {
	tests := []struct {
		name        string
		softDelete  bool
		missing     bool
		wantRenamed bool
		wantTitle   string
	}{
		{name: "renames an active conversation", wantRenamed: true, wantTitle: "Renamed"},
		{name: "does not rename a deleted conversation", softDelete: true, wantRenamed: false, wantTitle: "Original"},
		{name: "reports false for a missing conversation", missing: true, wantRenamed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())

			conv := makeConversation(func(c *Conversation) { c.Title = "Original" })
			id := model.NewId()
			if !tt.missing {
				require.NoError(t, s.CreateConversation(conv))
				id = conv.ID
				if tt.softDelete {
					require.NoError(t, s.SoftDeleteConversation(conv.ID, 1000))
				}
			}

			renamed, err := s.RenameConversation(id, "Renamed")
			require.NoError(t, err)
			assert.Equal(t, tt.wantRenamed, renamed)

			if tt.missing {
				return
			}
			var title string
			var deleteAt int64
			require.NoError(t, s.db.QueryRow("SELECT Title, DeleteAt FROM LLM_Conversations WHERE ID = $1", id).Scan(&title, &deleteAt))
			assert.Equal(t, tt.wantTitle, title)
			if tt.softDelete {
				assert.Equal(t, int64(1000), deleteAt, "a rename must never restore a deleted conversation")
			}
		})
	}
}

func TestSetConversationTitleIfUnset(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		softDelete  bool
		missing     bool
		wantUpdated bool
		wantTitle   string
	}{
		{name: "sets the title of an untitled conversation", wantUpdated: true, wantTitle: "Generated"},
		{name: "keeps a title already set", title: "Set by owner", wantUpdated: false, wantTitle: "Set by owner"},
		{name: "does not title a deleted conversation", softDelete: true, wantUpdated: false, wantTitle: ""},
		{name: "reports false for a missing conversation", missing: true, wantUpdated: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())

			conv := makeConversation(func(c *Conversation) {
				c.Title = tt.title
				c.UpdatedAt = 1000
			})
			id := model.NewId()
			if !tt.missing {
				require.NoError(t, s.CreateConversation(conv))
				id = conv.ID
				if tt.softDelete {
					require.NoError(t, s.SoftDeleteConversation(conv.ID, 1000))
				}
			}

			updated, err := s.SetConversationTitleIfUnset(id, "Generated")
			require.NoError(t, err)
			assert.Equal(t, tt.wantUpdated, updated)

			if tt.missing {
				return
			}
			var title string
			var updatedAt int64
			require.NoError(t, s.db.QueryRow("SELECT Title, UpdatedAt FROM LLM_Conversations WHERE ID = $1", id).Scan(&title, &updatedAt))
			assert.Equal(t, tt.wantTitle, title)
			assert.Equal(t, int64(1000), updatedAt, "a title is not activity")
		})
	}
}

// TestConversationActivityOrdering exercises the writes that do and do not
// count as activity for the conversation list order.
func TestConversationActivityOrdering(t *testing.T) {
	tests := []struct {
		name string
		// act runs against conversations "old" (UpdatedAt 1000) and "new"
		// (UpdatedAt 2000), both owned by the same user.
		act       func(t *testing.T, s *Store, ids map[string]string)
		wantOrder []string
		wantOld   int64
	}{
		{
			name:      "no activity keeps creation order",
			act:       func(t *testing.T, s *Store, ids map[string]string) {},
			wantOrder: []string{"new", "old"},
			wantOld:   1000,
		},
		{
			name: "a new turn moves a conversation to the top",
			act: func(t *testing.T, s *Store, ids map[string]string) {
				require.NoError(t, s.CreateTurn(makeTurn(ids["old"], 1, func(turn *Turn) { turn.CreatedAt = 5000 })))
			},
			wantOrder: []string{"old", "new"},
			wantOld:   5000,
		},
		{
			name: "an auto-sequenced turn moves a conversation to the top",
			act: func(t *testing.T, s *Store, ids map[string]string) {
				require.NoError(t, s.CreateTurnAutoSequence(makeTurn(ids["old"], 0, func(turn *Turn) { turn.CreatedAt = 5000 })))
			},
			wantOrder: []string{"old", "new"},
			wantOld:   5000,
		},
		{
			name: "a turn older than the last activity does not move it back",
			act: func(t *testing.T, s *Store, ids map[string]string) {
				require.NoError(t, s.CreateTurn(makeTurn(ids["old"], 1, func(turn *Turn) { turn.CreatedAt = 500 })))
			},
			wantOrder: []string{"new", "old"},
			wantOld:   1000,
		},
		{
			name: "a rename does not reorder",
			act: func(t *testing.T, s *Store, ids map[string]string) {
				renamed, err := s.RenameConversation(ids["old"], "Renamed")
				require.NoError(t, err)
				require.True(t, renamed)
			},
			wantOrder: []string{"new", "old"},
			wantOld:   1000,
		},
		{
			name: "a generated title does not reorder",
			act: func(t *testing.T, s *Store, ids map[string]string) {
				updated, err := s.SetConversationTitleIfUnset(ids["old"], "Generated")
				require.NoError(t, err)
				require.True(t, updated)
			},
			wantOrder: []string{"new", "old"},
			wantOld:   1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupTestStore(t)
			require.NoError(t, s.RunMigrations())

			userID := model.NewId()
			ids := map[string]string{}
			keyByID := map[string]string{}
			for key, updatedAt := range map[string]int64{"old": 1000, "new": 2000} {
				conv := makeConversation(func(c *Conversation) {
					c.UserID = userID
					c.CreatedAt = updatedAt
					c.UpdatedAt = updatedAt
				})
				require.NoError(t, s.CreateConversation(conv))
				ids[key] = conv.ID
				keyByID[conv.ID] = key
			}

			tt.act(t, s, ids)

			summaries, err := s.GetConversationSummariesForUser(userID, 60, 0)
			require.NoError(t, err)
			got := make([]string, len(summaries))
			for i, summary := range summaries {
				got[i] = keyByID[summary.ID]
				if summary.ID == ids["old"] {
					assert.Equal(t, tt.wantOld, summary.UpdatedAt)
				}
			}
			assert.Equal(t, tt.wantOrder, got)
		})
	}
}
