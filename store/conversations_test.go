// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stringPtr(s string) *string {
	return &s
}

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
					c.ChannelID = stringPtr("channel1")
					c.RootPostID = stringPtr("post1")
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
					c.RootPostID = stringPtr("post1")
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.UserID = "userDup"
					c.RootPostID = stringPtr("post1")
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
					c.RootPostID = stringPtr("post1")
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.UserID = "userB"
					c.RootPostID = stringPtr("post1")
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
					c.RootPostID = stringPtr("post1")
					c.BotID = "bot1"
				})
				err := s.CreateConversation(conv)
				require.NoError(t, err)
			},
			validate: func(t *testing.T, s *Store) {
				conv := makeConversation(func(c *Conversation) {
					c.RootPostID = stringPtr("post1")
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
					c.RootPostID = stringPtr("post1")
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
					c.RootPostID = stringPtr("post1")
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
					c.RootPostID = stringPtr("post1")
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
			name:  "updates title and UpdatedAt",
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
				assert.GreaterOrEqual(t, got.UpdatedAt, conv.UpdatedAt)
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

				for i := 0; i < 5; i++ {
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
					c.RootPostID = stringPtr(rootPostID)
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
