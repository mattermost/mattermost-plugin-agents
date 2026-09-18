// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Message the reply post carries after the edit.
const editedReplyText = "edited reply text"

func TestUpdateTurnForEditedPost(t *testing.T) {
	tests := []struct {
		name     string
		post     func(f *deletedPostFixture) *model.Post
		previous func(f *deletedPostFixture) *model.Post
		validate func(t *testing.T, f *deletedPostFixture)
	}{
		{
			name: "reply post with a new message",
			post: func(f *deletedPostFixture) *model.Post {
				return &model.Post{
					Id:        f.replyPostID,
					UserId:    f.userID,
					ChannelId: f.channelID,
					RootId:    f.rootPostID,
					Message:   editedReplyText,
				}
			},
			previous: func(f *deletedPostFixture) *model.Post {
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
				assert.Contains(t, contents, editedReplyText,
					"turn content anchored to the post should carry its current message")
				assert.NotContains(t, contents, replyTurnText)
				assert.Contains(t, contents, rootTurnText,
					"turn content anchored to other posts should be left as stored")
			},
		},
		{
			name: "reply post with an unchanged message",
			post: func(f *deletedPostFixture) *model.Post {
				return &model.Post{
					Id:        f.replyPostID,
					UserId:    f.userID,
					ChannelId: f.channelID,
					RootId:    f.rootPostID,
					Message:   replyTurnText,
				}
			},
			previous: func(f *deletedPostFixture) *model.Post {
				return &model.Post{
					Id:        f.replyPostID,
					UserId:    f.userID,
					ChannelId: f.channelID,
					RootId:    f.rootPostID,
					Message:   replyTurnText,
				}
			},
			validate: func(t *testing.T, f *deletedPostFixture) {
				assert.Contains(t, f.turnContents(t), replyTurnText)
			},
		},
		{
			name: "post unrelated to the conversation",
			post: func(f *deletedPostFixture) *model.Post {
				return &model.Post{
					Id:        model.NewId(),
					UserId:    f.userID,
					ChannelId: f.channelID,
					Message:   editedReplyText,
				}
			},
			previous: func(f *deletedPostFixture) *model.Post { return nil },
			validate: func(t *testing.T, f *deletedPostFixture) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, rootTurnText)
				assert.Contains(t, contents, replyTurnText)
				assert.NotContains(t, contents, editedReplyText)
			},
		},
		{
			name:     "nil post",
			post:     func(f *deletedPostFixture) *model.Post { return nil },
			previous: func(f *deletedPostFixture) *model.Post { return nil },
			validate: func(t *testing.T, f *deletedPostFixture) {
				contents := f.turnContents(t)
				assert.Contains(t, contents, rootTurnText)
				assert.Contains(t, contents, replyTurnText)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := setupDeletedPostFixture(t)

			require.NoError(t, f.service.UpdateTurnForEditedPost(tt.post(f), tt.previous(f)))

			tt.validate(t, f)
		})
	}
}
