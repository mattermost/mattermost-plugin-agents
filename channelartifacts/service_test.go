// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

type allowAllMembers struct{}

func (allowAllMembers) GetMember(string, string) (*model.ChannelMember, error) {
	return &model.ChannelMember{SchemeAdmin: true}, nil
}

func TestServiceFindLineageMatchAndDelete(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})

	channelID := model.NewId()
	userID := model.NewId()
	fileID := model.NewId()
	rootID := model.NewId()
	postID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}

	entry, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID,
		PostID:    postID,
		FileID:    fileID,
		Title:     "Lineage",
	}, []byte("<html>one</html>"), "one.html", userID, "Author", "", rootID, ContentLimits{MaxSize: 1024})
	require.NoError(t, err)
	require.Equal(t, rootID, entry.SourceRootPostID)

	byFile := svc.FindLineageMatch(channelID, postID, fileID, rootID)
	require.NotNil(t, byFile)
	require.Equal(t, entry.ID, byFile.ID)

	byRoot := svc.FindLineageMatch(channelID, model.NewId(), model.NewId(), rootID)
	require.NotNil(t, byRoot)
	require.Equal(t, entry.ID, byRoot.ID)

	_, err = svc.ReplaceContent(userID, entry.ID, ReplaceRequest{PostID: postID, FileID: fileID}, []byte("<html>two</html>"), "two.html", userID, "Author", rootID, ContentLimits{MaxSize: 1024})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(userID, entry.ID, false))
	still, err := svc.GetReadable(userID, entry.ID)
	require.NoError(t, err)
	require.Equal(t, 1, still.Version)

	require.NoError(t, svc.Delete(userID, entry.ID, true))
	_, err = svc.GetReadable(userID, entry.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRootPostID(t *testing.T) {
	root := model.NewId()
	child := &model.Post{Id: model.NewId(), RootId: root}
	require.Equal(t, root, RootPostID(child))
	top := &model.Post{Id: root}
	require.Equal(t, root, RootPostID(top))
	require.Empty(t, RootPostID(nil))
}

func TestServiceListByTeam(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})

	teamID := model.NewId()
	otherTeam := model.NewId()
	channelID := model.NewId()
	otherChannel := model.NewId()
	userID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, TeamId: teamID, Type: model.ChannelTypeOpen}
	kv.channels[otherChannel] = &model.Channel{Id: otherChannel, TeamId: otherTeam, Type: model.ChannelTypeOpen}

	_, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID,
		PostID:    model.NewId(),
		FileID:    model.NewId(),
		Title:     "On team",
	}, []byte("<html>a</html>"), "a.html", userID, "Author", teamID, model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)

	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: otherChannel,
		PostID:    model.NewId(),
		FileID:    model.NewId(),
		Title:     "Other team",
	}, []byte("<html>b</html>"), "b.html", userID, "Author", otherTeam, model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)

	resp, err := svc.ListByTeam(teamID)
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	require.Equal(t, "On team", resp.Entries[0].Title)
}
