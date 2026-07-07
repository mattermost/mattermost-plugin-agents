// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func postList(order []string, posts map[string]*model.Post) *model.PostList {
	return &model.PostList{Order: order, Posts: posts}
}

func TestFindPreviousThreadPost(t *testing.T) {
	const (
		rootID  = "root-id"
		replyID = "reply-id"
	)

	root := &model.Post{Id: rootID, CreateAt: 100}
	prev := &model.Post{Id: "prev-id", RootId: rootID, CreateAt: 200}
	reply := &model.Post{Id: replyID, RootId: rootID, CreateAt: 300}

	cases := []struct {
		name   string
		target *model.Post
		thread *model.PostList
		wantID string
	}{
		{
			name:   "returns previous post by thread order",
			target: reply,
			thread: postList(
				[]string{rootID, prev.Id, replyID},
				map[string]*model.Post{rootID: root, prev.Id: prev, replyID: reply},
			),
			wantID: prev.Id,
		},
		{
			name:   "returns nil when post is first in thread",
			target: reply,
			thread: postList([]string{replyID}, map[string]*model.Post{replyID: reply}),
		},
		{
			name:   "skips missing posts while walking backward",
			target: reply,
			thread: postList(
				[]string{rootID, "missing", prev.Id, replyID},
				map[string]*model.Post{rootID: root, prev.Id: prev, replyID: reply},
			),
			wantID: prev.Id,
		},
		{
			name:   "uses latest earlier post when current post is missing from order",
			target: reply,
			thread: postList(
				[]string{rootID, prev.Id},
				map[string]*model.Post{
					rootID:  root,
					prev.Id: prev,
					replyID: reply,
				},
			),
			wantID: prev.Id,
		},
		{
			name:   "uses id tie-breaker when current post is missing from order",
			target: &model.Post{Id: "m-current", RootId: rootID, CreateAt: 300},
			thread: postList(
				[]string{rootID},
				map[string]*model.Post{
					rootID:     root,
					"a-before": {Id: "a-before", RootId: rootID, CreateAt: 300},
					"z-after":  {Id: "z-after", RootId: rootID, CreateAt: 300},
				},
			),
			wantID: "a-before",
		},
		{
			name:   "uses latest earlier post when thread order is not chronological",
			target: reply,
			thread: postList(
				[]string{rootID, replyID, prev.Id},
				map[string]*model.Post{rootID: root, prev.Id: prev, replyID: reply},
			),
			wantID: prev.Id,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := mocks.NewMockClient(t)
			mockClient.EXPECT().GetPostThread(rootID).Return(tc.thread, nil)

			conv := &Conversations{mmClient: mockClient}
			got, err := conv.findPreviousThreadPost(tc.target)
			require.NoError(t, err)
			if tc.wantID == "" {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tc.wantID, got.Id)
		})
	}

	t.Run("returns error when thread fetch fails", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockClient.EXPECT().GetPostThread(rootID).Return(nil, errors.New("thread not found"))

		conv := &Conversations{mmClient: mockClient}
		got, err := conv.findPreviousThreadPost(reply)
		require.Error(t, err)
		require.Nil(t, got)
	})

	t.Run("returns nil when thread is empty", func(t *testing.T) {
		mockClient := mocks.NewMockClient(t)
		mockClient.EXPECT().GetPostThread(rootID).Return(nil, nil)

		conv := &Conversations{mmClient: mockClient}
		got, err := conv.findPreviousThreadPost(reply)
		require.NoError(t, err)
		require.Nil(t, got)
	})
}
