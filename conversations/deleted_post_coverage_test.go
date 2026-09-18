// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errTurnLookup is returned by the turn lookup of turnLookupErrorStore.
var errTurnLookup = errors.New("turn lookup unavailable")

// turnLookupErrorStore is a conversation store whose turn-by-post lookup always
// fails, leaving every other operation to the embedded store.
type turnLookupErrorStore struct {
	*fakeConvStore
}

func (s *turnLookupErrorStore) GetTurnByPostID(string) (*store.Turn, error) {
	return nil, errTurnLookup
}

func TestDeleteConversationsForDeletedPostSoftDeletesWhenTurnLookupFails(t *testing.T) {
	f := setupDeletedPostFixture(t)
	f.service.SetConversationService(conversation.NewService(
		&turnLookupErrorStore{fakeConvStore: newFakeConvStore()}, nil, nil, nil))

	err := f.service.DeleteConversationsForDeletedPost(&model.Post{
		Id:        f.rootPostID,
		UserId:    f.userID,
		ChannelId: f.channelID,
		Message:   rootTurnText,
	})
	require.Error(t, err, "an unusable turn lookup is reported to the caller")

	assert.False(t, f.conversationIsRetrievable(t),
		"conversation keyed to the deleted root post should be soft-deleted whatever the turn lookup reports")
}

// TestThreadRootDeletionLeavesConversationUnreadable covers the shape
// Mattermost produces when a thread root is deleted: the server marks the root
// and every reply deleted in one statement and invokes the deletion hook once,
// for the root post only.
func TestThreadRootDeletionLeavesConversationUnreadable(t *testing.T) {
	f := setupDeletedPostFixture(t)

	require.NoError(t, f.service.DeleteConversationsForDeletedPost(&model.Post{
		Id:        f.rootPostID,
		UserId:    f.userID,
		ChannelId: f.channelID,
		Message:   rootTurnText,
	}))

	assert.Contains(t, f.turnContents(t), replyTurnText,
		"the hook runs for the root post alone, so reply turns keep their stored text")
	assert.False(t, f.conversationIsRetrievable(t),
		"a conversation whose root post is gone is no longer readable")
}
