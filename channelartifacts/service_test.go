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

type fixedMember struct {
	member *model.ChannelMember
}

func (f fixedMember) GetMember(string, string) (*model.ChannelMember, error) {
	if f.member == nil {
		return nil, ErrNotFound
	}
	return f.member, nil
}

func TestCanManage(t *testing.T) {
	userID := model.NewId()
	channelID := model.NewId()

	tests := []struct {
		name       string
		userID     string
		admin      bool
		read       bool
		channel    *model.Channel
		member     *model.ChannelMember
		wantManage bool
	}{
		{name: "empty user", userID: "", wantManage: false},
		{name: "system admin", userID: userID, admin: true, wantManage: true},
		{
			name: "deleted channel", userID: userID, read: true,
			channel: &model.Channel{Id: channelID, Type: model.ChannelTypeOpen, DeleteAt: 1},
		},
		{
			name: "direct channel member", userID: userID, read: true,
			channel:    &model.Channel{Id: channelID, Type: model.ChannelTypeDirect},
			wantManage: true,
		},
		{
			name: "group channel member", userID: userID, read: true,
			channel:    &model.Channel{Id: channelID, Type: model.ChannelTypeGroup},
			wantManage: true,
		},
		{
			name: "guest", userID: userID, read: true,
			channel: &model.Channel{Id: channelID, Type: model.ChannelTypeOpen},
			member:  &model.ChannelMember{SchemeGuest: true, SchemeAdmin: true},
		},
		{
			name: "channel admin", userID: userID, read: true,
			channel:    &model.Channel{Id: channelID, Type: model.ChannelTypeOpen},
			member:     &model.ChannelMember{SchemeAdmin: true},
			wantManage: true,
		},
		{
			name: "regular member", userID: userID, read: true,
			channel: &model.Channel{Id: channelID, Type: model.ChannelTypeOpen},
			member:  &model.ChannelMember{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kv := newMemoryKV()
			kv.permissionAll = tt.admin
			kv.readAll = tt.read
			if tt.channel != nil {
				kv.channels[channelID] = tt.channel
			}
			svc := NewService(NewStore(kv), kv, fixedMember{member: tt.member})
			require.Equal(t, tt.wantManage, svc.CanManage(tt.userID, channelID))
		})
	}
}

func TestRollbackRestoresVersionAndActor(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	svc := NewService(NewStore(kv), kv, allowAllMembers{})
	channelID := model.NewId()
	userID := model.NewId()
	actor := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}

	entry, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Notes",
	}, []byte("<html>v1</html>"), "v1.html", userID, "Author", "", model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)
	_, err = svc.ReplaceContent(userID, entry.ID, ReplaceRequest{PostID: model.NewId(), FileID: model.NewId()}, []byte("<html>v2</html>"), "v2.html", userID, "Author", "", ContentLimits{MaxSize: 1024})
	require.NoError(t, err)

	restored, err := svc.Rollback(actor, entry.ID, 1)
	require.NoError(t, err)
	require.Equal(t, 1, restored.Version)
	require.Equal(t, actor, restored.UpdatedBy)
	body, err := svc.ReadContent(restored)
	require.NoError(t, err)
	require.Equal(t, "<html>v1</html>", string(body))
}

func TestPurgeChannelRemovesEveryArtifact(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	channelID := model.NewId()
	userID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}

	for _, title := range []string{"One", "Two"} {
		_, err := svc.CreateFromHTML(userID, CreateRequest{
			ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: title,
		}, []byte("<html>"+title+"</html>"), title+".html", userID, "Author", "", model.NewId(), ContentLimits{MaxSize: 1024})
		require.NoError(t, err)
	}

	require.NoError(t, svc.PurgeChannel(channelID))
	left, err := store.List(channelID)
	require.NoError(t, err)
	require.Empty(t, left)
}

func TestPurgeChannelRejectsInvalidID(t *testing.T) {
	svc := NewService(NewStore(newMemoryKV()), newMemoryKV(), allowAllMembers{})
	require.ErrorIs(t, svc.PurgeChannel("not-an-id"), ErrInvalidInput)
}
