// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelcontext

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func openChannel() *model.Channel {
	return &model.Channel{Id: model.NewId(), Type: model.ChannelTypeOpen}
}

func notFoundErr() error {
	return model.NewAppError("GetPost", "app.post.get.app_error", nil, "", http.StatusNotFound)
}

func TestServiceSetInstructionsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name         string
		channel      *model.Channel
		instructions string
	}{
		{name: "missing channel", channel: nil, instructions: "hello"},
		{name: "direct message channel", channel: &model.Channel{Id: model.NewId(), Type: model.ChannelTypeDirect}, instructions: "hello"},
		{name: "group message channel", channel: &model.Channel{Id: model.NewId(), Type: model.ChannelTypeGroup}, instructions: "hello"},
		{name: "too long", channel: openChannel(), instructions: strings.Repeat("界", MaxInstructionsRunes+1)},
	}

	dbClient := testDB(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(NewStore(dbClient), mocks.NewMockClient(t))

			_, err := svc.SetInstructions(tc.channel, tc.instructions, model.NewId())
			require.ErrorIs(t, err, ErrValidation)

			if tc.channel != nil {
				stored, getErr := svc.GetInstructions(tc.channel.Id)
				require.NoError(t, getErr)
				require.Empty(t, stored)
			}
		})
	}
}

func TestServiceSetInstructions(t *testing.T) {
	svc := NewService(NewStore(testDB(t)), mocks.NewMockClient(t))
	channel := &model.Channel{Id: model.NewId(), Type: model.ChannelTypePrivate}

	saved, err := svc.SetInstructions(channel, "  Our deploys freeze on Fridays.\n", model.NewId())
	require.NoError(t, err)
	require.Equal(t, "Our deploys freeze on Fridays.", saved)
	stored, err := svc.GetInstructions(channel.Id)
	require.NoError(t, err)
	require.Equal(t, saved, stored)

	longest := strings.Repeat("界", MaxInstructionsRunes)
	saved, err = svc.SetInstructions(channel, longest, model.NewId())
	require.NoError(t, err)
	require.Equal(t, longest, saved)

	saved, err = svc.SetInstructions(channel, " \n\t ", model.NewId())
	require.NoError(t, err)
	require.Empty(t, saved)
	stored, err = svc.GetInstructions(channel.Id)
	require.NoError(t, err)
	require.Empty(t, stored, "blank instructions clear the channel's instructions")
}

func TestServicePinPostRejectsInvalidPosts(t *testing.T) {
	channel := openChannel()

	tests := []struct {
		name    string
		channel *model.Channel
		postID  string
		post    *model.Post
		postErr error
		// false: the error is an infrastructure failure, not ErrValidation.
		wantValidation bool
	}{
		{name: "malformed post ID", channel: channel, postID: "not-an-id", wantValidation: true},
		{name: "direct message channel", channel: &model.Channel{Id: channel.Id, Type: model.ChannelTypeDirect}, postID: model.NewId(), wantValidation: true},
		{name: "post not found", channel: channel, postID: model.NewId(), postErr: notFoundErr(), wantValidation: true},
		{name: "post in another channel", channel: channel, postID: model.NewId(), post: &model.Post{ChannelId: model.NewId()}, wantValidation: true},
		{name: "deleted post", channel: channel, postID: model.NewId(), post: &model.Post{ChannelId: channel.Id, DeleteAt: 1}, wantValidation: true},
		{name: "system message", channel: channel, postID: model.NewId(), post: &model.Post{ChannelId: channel.Id, Type: model.PostTypeJoinChannel}, wantValidation: true},
		{name: "post lookup failure", channel: channel, postID: model.NewId(), postErr: errors.New("server unavailable"), wantValidation: false},
	}

	dbClient := testDB(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mmClient := mocks.NewMockClient(t)
			if tc.post != nil || tc.postErr != nil {
				mmClient.EXPECT().GetPost(tc.postID).Return(tc.post, tc.postErr)
			}
			s := NewStore(dbClient)
			svc := NewService(s, mmClient)

			err := svc.PinPost(tc.channel, tc.postID, model.NewId())
			require.Error(t, err)
			if tc.wantValidation {
				require.ErrorIs(t, err, ErrValidation)
			} else {
				require.NotErrorIs(t, err, ErrValidation)
			}

			pins, listErr := s.ListPins(tc.channel.Id)
			require.NoError(t, listErr)
			require.Empty(t, pins)
		})
	}
}

func TestServicePinPost(t *testing.T) {
	s := NewStore(testDB(t))
	channel := openChannel()
	mmClient := mocks.NewMockClient(t)
	mmClient.EXPECT().GetPost(mock.Anything).RunAndReturn(func(id string) (*model.Post, error) {
		return &model.Post{Id: id, ChannelId: channel.Id, Message: "context"}, nil
	})
	svc := NewService(s, mmClient)

	firstPostID := model.NewId()
	require.NoError(t, svc.PinPost(channel, firstPostID, model.NewId()))
	require.NoError(t, svc.PinPost(channel, firstPostID, model.NewId()), "re-pinning is a no-op success")

	for range MaxPinnedPosts - 1 {
		require.NoError(t, svc.PinPost(channel, model.NewId(), model.NewId()))
	}
	require.ErrorIs(t, svc.PinPost(channel, model.NewId(), model.NewId()), ErrPinLimitReached)
	require.NoError(t, svc.PinPost(channel, firstPostID, model.NewId()), "re-pinning still succeeds in a full channel")

	pins, err := s.ListPins(channel.Id)
	require.NoError(t, err)
	require.Len(t, pins, MaxPinnedPosts)

	require.NoError(t, svc.UnpinPost(channel.Id, firstPostID))
	require.NoError(t, svc.UnpinPost(channel.Id, firstPostID), "unpinning a post that is not pinned is a no-op success")
	require.NoError(t, svc.PinPost(channel, model.NewId(), model.NewId()), "unpinning frees a slot")
}

func TestServiceListPinnedPostsHandlesStalePins(t *testing.T) {
	s := NewStore(testDB(t))
	channelID := model.NewId()

	live := &model.Post{Id: model.NewId(), ChannelId: channelID, Message: "live"}
	deleted := &model.Post{Id: model.NewId(), ChannelId: channelID, DeleteAt: 1}
	moved := &model.Post{Id: model.NewId(), ChannelId: model.NewId()}
	missingID := model.NewId()
	unavailableID := model.NewId()

	for i, postID := range []string{live.Id, deleted.Id, moved.Id, missingID, unavailableID} {
		_, err := s.AddPin(Pin{ChannelID: channelID, PostID: postID, PinnedBy: model.NewId(), PinnedAt: int64(i)}, MaxPinnedPosts)
		require.NoError(t, err)
	}

	mmClient := mocks.NewMockClient(t)
	mmClient.EXPECT().GetPost(live.Id).Return(live, nil)
	mmClient.EXPECT().GetPost(deleted.Id).Return(deleted, nil)
	mmClient.EXPECT().GetPost(moved.Id).Return(moved, nil)
	mmClient.EXPECT().GetPost(missingID).Return(nil, notFoundErr())
	mmClient.EXPECT().GetPost(unavailableID).Return(nil, errors.New("server unavailable"))
	mmClient.EXPECT().LogWarn(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()

	pinned, err := NewService(s, mmClient).ListPinnedPosts(channelID)
	require.NoError(t, err)
	require.Len(t, pinned, 1)
	require.Equal(t, live, pinned[0].Post)

	pins, err := s.ListPins(channelID)
	require.NoError(t, err)
	remaining := make([]string, 0, len(pins))
	for _, pin := range pins {
		remaining = append(remaining, pin.PostID)
	}
	require.ElementsMatch(t, []string{live.Id, unavailableID}, remaining,
		"deleted, moved, and missing posts are unpinned; a transient lookup failure keeps the pin")
}

func TestServicePromptContext(t *testing.T) {
	s := NewStore(testDB(t))
	channel := openChannel()
	author := &model.User{Id: model.NewId(), Username: "alice"}
	first := &model.Post{Id: model.NewId(), ChannelId: channel.Id, UserId: author.Id, Message: "Deploys freeze on Fridays.", CreateAt: 1}
	second := &model.Post{Id: model.NewId(), ChannelId: channel.Id, UserId: author.Id, Message: "On-call: #payments-oncall", CreateAt: 2}

	mmClient := mocks.NewMockClient(t)
	mmClient.EXPECT().GetPost(first.Id).Return(first, nil).Maybe()
	mmClient.EXPECT().GetPost(second.Id).Return(second, nil).Maybe()
	mmClient.EXPECT().GetUser(author.Id).Return(author, nil).Once()
	svc := NewService(s, mmClient)

	empty, err := svc.PromptContext(channel.Id)
	require.NoError(t, err)
	require.Nil(t, empty, "a channel without instructions or pins has no context")

	_, err = svc.SetInstructions(channel, "Payments team channel.", model.NewId())
	require.NoError(t, err)
	instructionsOnly, err := svc.PromptContext(channel.Id)
	require.NoError(t, err)
	require.Equal(t, "Payments team channel.", instructionsOnly.Instructions)
	require.Empty(t, instructionsOnly.PinnedPosts)

	require.NoError(t, svc.PinPost(channel, first.Id, model.NewId()))
	require.NoError(t, svc.PinPost(channel, second.Id, model.NewId()))
	full, err := svc.PromptContext(channel.Id)
	require.NoError(t, err)
	require.Equal(t, "Payments team channel.", full.Instructions)
	require.Contains(t, full.PinnedPosts, "@alice")
	require.Contains(t, full.PinnedPosts, first.Id)
	firstAt := strings.Index(full.PinnedPosts, first.Message)
	secondAt := strings.Index(full.PinnedPosts, second.Message)
	require.True(t, firstAt >= 0 && secondAt > firstAt, "pinned posts render oldest pin first")
}
