// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Text stored in the seeded user turns, keyed to the post each turn anchors to.
const (
	rootTurnText  = "root turn text"
	replyTurnText = "reply turn text"
)

// deletedPostFixture holds a channel conversation with two user turns: one
// anchored to the thread root post, one anchored to a mid-thread reply.
type deletedPostFixture struct {
	service        *conversations.Conversations
	store          *store.Store
	conversationID string
	rootPostID     string
	replyPostID    string
	channelID      string
	userID         string
}

// setupDeletedPostFixture builds a Conversations backed by a real store in a
// fresh schema and seeds the two-turn conversation through conversation.Service.
func setupDeletedPostFixture(t *testing.T) *deletedPostFixture {
	t.Helper()

	setupDB, err := sqlx.Connect("postgres", channelMentionTestConnStr)
	require.NoError(t, err)

	schemaName := fmt.Sprintf("test_%d", time.Now().UnixNano())
	_, err = setupDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schemaName))
	require.NoError(t, err)
	setupDB.Close()

	// search_path goes in the connection string so every pooled connection
	// inherits it, including the ones opened for nested statements.
	db, err := sqlx.Connect("postgres", channelMentionTestConnStr+"&search_path="+schemaName)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schemaName))
		db.Close()
	})

	s := store.New(db)
	require.NoError(t, s.RunMigrations())

	botID := model.NewId()
	userID := model.NewId()
	channelID := model.NewId()
	rootPostID := model.NewId()
	replyPostID := model.NewId()

	mmClient := &fakeMMClient{
		posts: map[string]*model.Post{
			rootPostID: {
				Id:        rootPostID,
				UserId:    userID,
				ChannelId: channelID,
				Message:   rootTurnText,
			},
			replyPostID: {
				Id:        replyPostID,
				UserId:    userID,
				ChannelId: channelID,
				RootId:    rootPostID,
				Message:   replyTurnText,
			},
		},
	}

	convService := conversation.NewService(s, nil, mmClient, &channelMentionBotLookup{
		botIDs: map[string]bool{botID: true},
	})

	first, err := convService.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:       userID,
		BotID:        botID,
		ChannelID:    channelID,
		RootPostID:   rootPostID,
		Operation:    "conversation",
		SystemPrompt: "You are helpful",
		UserMessage:  rootTurnText,
		UserPostID:   &rootPostID,
	})
	require.NoError(t, err)
	require.True(t, first.IsNew)

	second, err := convService.GetOrCreateConversation(conversation.GetOrCreateParams{
		UserID:      userID,
		BotID:       botID,
		ChannelID:   channelID,
		RootPostID:  rootPostID,
		Operation:   "conversation",
		UserMessage: replyTurnText,
		UserPostID:  &replyPostID,
	})
	require.NoError(t, err)
	require.False(t, second.IsNew)
	require.Equal(t, first.Conversation.ID, second.Conversation.ID)

	service := conversations.New(
		nil,
		mmClient,
		nil,
		nil,
		nil,
		mmapi.NewTestDBClient(db),
		nil,
		nil,
		nil,
		nil,
	)
	service.SetConversationService(convService)

	fixture := &deletedPostFixture{
		service:        service,
		store:          s,
		conversationID: first.Conversation.ID,
		rootPostID:     rootPostID,
		replyPostID:    replyPostID,
		channelID:      channelID,
		userID:         userID,
	}

	// Both turn texts are stored before the method under test runs.
	require.Contains(t, fixture.turnContents(t), rootTurnText)
	require.Contains(t, fixture.turnContents(t), replyTurnText)

	return fixture
}

// turnContents returns the concatenated raw JSON content of every turn still
// stored for the conversation.
func (f *deletedPostFixture) turnContents(t *testing.T) string {
	t.Helper()

	turns, err := f.store.GetTurnsForConversation(f.conversationID)
	require.NoError(t, err)

	var b strings.Builder
	for _, turn := range turns {
		b.Write(turn.Content)
	}
	return b.String()
}

// conversationIsRetrievable reports whether the conversation row is still
// readable, which the store scopes to rows that are not soft-deleted.
func (f *deletedPostFixture) conversationIsRetrievable(t *testing.T) bool {
	t.Helper()

	conv, err := f.store.GetConversation(f.conversationID)
	if errors.Is(err, store.ErrConversationNotFound) {
		return false
	}
	require.NoError(t, err)
	require.NotNil(t, conv)
	return true
}

func TestDeleteConversationsForDeletedPostTurnContent(t *testing.T) {
	tests := []struct {
		name     string
		post     func(f *deletedPostFixture) *model.Post
		validate func(t *testing.T, f *deletedPostFixture)
	}{
		{
			name: "mid-thread reply post",
			post: func(f *deletedPostFixture) *model.Post {
				return &model.Post{
					Id:        f.replyPostID,
					UserId:    f.userID,
					ChannelId: f.channelID,
					RootId:    f.rootPostID,
					Message:   replyTurnText,
				}
			},
			validate: func(t *testing.T, f *deletedPostFixture) {
				contents := f.turnContents(t)
				assert.NotContains(t, contents, replyTurnText,
					"turn content anchored to the deleted reply should not remain")
				assert.Contains(t, contents, rootTurnText,
					"turn content anchored to other posts should remain")
			},
		},
		{
			name: "thread root post",
			post: func(f *deletedPostFixture) *model.Post {
				return &model.Post{
					Id:        f.rootPostID,
					UserId:    f.userID,
					ChannelId: f.channelID,
					Message:   rootTurnText,
				}
			},
			validate: func(t *testing.T, f *deletedPostFixture) {
				assert.NotContains(t, f.turnContents(t), rootTurnText,
					"turn content anchored to the deleted root should not remain")
				assert.False(t, f.conversationIsRetrievable(t),
					"conversation keyed to the deleted root post should be soft-deleted")
			},
		},
		{
			name: "post unrelated to the conversation",
			post: func(f *deletedPostFixture) *model.Post {
				unrelatedID := model.NewId()
				return &model.Post{
					Id:        unrelatedID,
					UserId:    f.userID,
					ChannelId: f.channelID,
					Message:   "unrelated message",
				}
			},
			validate: func(t *testing.T, f *deletedPostFixture) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, rootTurnText)
				assert.Contains(t, contents, replyTurnText)
				assert.True(t, f.conversationIsRetrievable(t))
			},
		},
		{
			name: "nil post",
			post: func(f *deletedPostFixture) *model.Post {
				return nil
			},
			validate: func(t *testing.T, f *deletedPostFixture) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, rootTurnText)
				assert.Contains(t, contents, replyTurnText)
				assert.True(t, f.conversationIsRetrievable(t))
			},
		},
		{
			name: "post with empty id",
			post: func(f *deletedPostFixture) *model.Post {
				return &model.Post{ChannelId: f.channelID}
			},
			validate: func(t *testing.T, f *deletedPostFixture) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, rootTurnText)
				assert.Contains(t, contents, replyTurnText)
				assert.True(t, f.conversationIsRetrievable(t))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := setupDeletedPostFixture(t)

			require.NoError(t, f.service.DeleteConversationsForDeletedPost(tt.post(f)))

			tt.validate(t, f)
		})
	}
}
