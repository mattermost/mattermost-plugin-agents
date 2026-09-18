// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Text stored on the conversations seeded by this file, and the title they
// carry when the post hooks run.
const (
	storedTitle     = "Deploying the unreleased pricing change"
	titleSourceText = "Deploy the unreleased pricing change"
	editedRootText  = "the root post as it now reads"
	unanchoredText  = "turn written without a post of its own"
)

// TestConversationTitleFollowsAnchoredPost covers the title a conversation
// carries: it is written from the message of the opening user turn, so it is
// dropped when the post that turn is anchored to no longer carries the text it
// was written from, and kept in every other case.
func TestConversationTitleFollowsAnchoredPost(t *testing.T) {
	tests := []struct {
		name string
		// run drives the post hook the case is about and returns the ID of the
		// conversation whose title the case is about.
		run func(t *testing.T, f *deletedPostFixture) string
		// expectedTitle is the title that conversation carries afterwards.
		expectedTitle string
		// validate makes the additional assertions about stored turn text.
		validate func(t *testing.T, f *deletedPostFixture, conversationID string)
	}{
		{
			name: "the post the title was written from is edited",
			run: func(t *testing.T, f *deletedPostFixture) string {
				require.NoError(t, f.service.UpdateTurnForEditedPost(
					rootPostWithMessage(f, editedRootText),
					rootPostWithMessage(f, rootTurnText),
				))
				return f.conversationID
			},
			expectedTitle: "",
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, editedRootText,
					"the turn holds the message its post now carries")
				assert.NotContains(t, contents, rootTurnText)
			},
		},
		{
			name: "the post the title was written from is deleted",
			run: func(t *testing.T, f *deletedPostFixture) string {
				anchorPostID := model.NewId()
				conversationID := seedTitledConversation(t, f, &anchorPostID, titleSourceText)

				require.NoError(t, f.service.DeleteConversationsForDeletedPost(&model.Post{
					Id:        anchorPostID,
					UserId:    f.userID,
					ChannelId: f.channelID,
					RootId:    f.rootPostID,
					Message:   titleSourceText,
				}))
				return conversationID
			},
			expectedTitle: "",
			validate: func(t *testing.T, f *deletedPostFixture, conversationID string) {
				assert.NotContains(t, conversationTurnContents(t, f, conversationID), titleSourceText,
					"the turn the title was written from holds no text either")
			},
		},
		{
			name: "a post other than the one the title was written from is edited",
			run: func(t *testing.T, f *deletedPostFixture) string {
				require.NoError(t, f.service.UpdateTurnForEditedPost(
					replyPostWithMessage(f, editedReplyText),
					replyPostWithMessage(f, replyTurnText),
				))
				return f.conversationID
			},
			expectedTitle: storedTitle,
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, editedReplyText,
					"the edited post's own turn holds the message it now carries")
				assert.Contains(t, contents, rootTurnText,
					"the turn the title was written from is left as stored")
			},
		},
		{
			name: "a post other than the one the title was written from is deleted",
			run: func(t *testing.T, f *deletedPostFixture) string {
				require.NoError(t, f.service.DeleteConversationsForDeletedPost(
					replyPostWithMessage(f, replyTurnText)))
				return f.conversationID
			},
			expectedTitle: storedTitle,
			validate: func(t *testing.T, f *deletedPostFixture, _ string) {
				contents := f.turnContents(t)
				assert.NotContains(t, contents, replyTurnText)
				assert.Contains(t, contents, rootTurnText,
					"the turn the title was written from is left as stored")
			},
		},
		{
			name: "a conversation whose opening turn has no anchored post",
			run: func(t *testing.T, f *deletedPostFixture) string {
				conversationID := seedTitledConversation(t, f, nil, unanchoredText)

				require.NoError(t, f.service.UpdateTurnForEditedPost(
					rootPostWithMessage(f, editedRootText),
					rootPostWithMessage(f, rootTurnText),
				))
				return conversationID
			},
			expectedTitle: storedTitle,
			validate: func(t *testing.T, f *deletedPostFixture, conversationID string) {
				assert.Contains(t, conversationTurnContents(t, f, conversationID), unanchoredText,
					"a turn no post is anchored to keeps its text")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := setupDeletedPostFixture(t)
			require.NoError(t, f.store.UpdateConversationTitle(f.conversationID, storedTitle))

			conversationID := tt.run(t, f)

			conv, err := f.store.GetConversation(conversationID)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedTitle, conv.Title,
				"the stored title describes the text the opening user turn holds")

			if tt.validate != nil {
				tt.validate(t, f, conversationID)
			}
		})
	}
}

// seedTitledConversation stores a titled conversation in the thread of the
// fixture, holding a single opening user turn anchored to anchorPostID, or
// anchored to no post when that is nil.
func seedTitledConversation(t *testing.T, f *deletedPostFixture, anchorPostID *string, text string) string {
	t.Helper()

	content, err := json.Marshal([]conversation.ContentBlock{
		{Type: conversation.BlockTypeText, Text: text},
	})
	require.NoError(t, err)

	now := model.GetMillis()
	channelID := f.channelID
	rootPostID := f.rootPostID
	conv := &store.Conversation{
		ID:         model.NewId(),
		UserID:     f.userID,
		BotID:      model.NewId(),
		ChannelID:  &channelID,
		RootPostID: &rootPostID,
		Title:      storedTitle,
		Operation:  "conversation",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	require.NoError(t, f.store.CreateConversation(conv))
	require.NoError(t, f.store.CreateTurn(&store.Turn{
		ID:             model.NewId(),
		ConversationID: conv.ID,
		PostID:         anchorPostID,
		Role:           "user",
		Content:        content,
		Sequence:       1,
		CreatedAt:      now,
	}))

	return conv.ID
}

// conversationTurnContents returns the concatenated raw JSON content of every
// turn stored for the given conversation.
func conversationTurnContents(t *testing.T, f *deletedPostFixture, conversationID string) string {
	t.Helper()

	turns, err := f.store.GetTurnsForConversation(conversationID)
	require.NoError(t, err)

	var contents string
	for _, turn := range turns {
		contents += string(turn.Content)
	}
	return contents
}

// rootPostWithMessage builds the fixture's thread root post carrying message.
func rootPostWithMessage(f *deletedPostFixture, message string) *model.Post {
	return &model.Post{
		Id:        f.rootPostID,
		UserId:    f.userID,
		ChannelId: f.channelID,
		Message:   message,
	}
}

// replyPostWithMessage builds the fixture's thread reply carrying message.
func replyPostWithMessage(f *deletedPostFixture, message string) *model.Post {
	return &model.Post{
		Id:        f.replyPostID,
		UserId:    f.userID,
		ChannelId: f.channelID,
		RootId:    f.rootPostID,
		Message:   message,
	}
}
