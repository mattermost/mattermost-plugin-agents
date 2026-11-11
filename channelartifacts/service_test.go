// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"encoding/json"
	"strings"
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

func TestReplaceContentReturnsTheCommittedHeadWhenPruneFails(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	userID := model.NewId()
	channelID := model.NewId()
	entry, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID,
		PostID:    model.NewId(),
		FileID:    model.NewId(),
		Title:     "Notes",
	}, []byte("<html>1</html>"), "1.html", userID, "Author", "", "", ContentLimits{MaxSize: 1024})
	require.NoError(t, err)
	_, err = svc.ReplaceContent(userID, entry.ID, ReplaceRequest{PostID: model.NewId(), FileID: model.NewId()}, []byte("<html>2</html>"), "2.html", userID, "Author", "", ContentLimits{MaxSize: 1024, VersionsToKeep: 2})
	require.NoError(t, err)

	kv.histCASCount = 0
	kv.failHistCASErrorAt = 2
	updated, err := svc.ReplaceContent(userID, entry.ID, ReplaceRequest{PostID: model.NewId(), FileID: model.NewId()}, []byte("<html>3</html>"), "3.html", userID, "Author", "", ContentLimits{MaxSize: 1024, VersionsToKeep: 2})
	require.ErrorIs(t, err, ErrCleanupDeferred)
	require.Equal(t, 3, updated.Version)
	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 3, stored.Version)
}

func TestRootPostID(t *testing.T) {
	root := model.NewId()
	tests := []struct {
		name string
		post *model.Post
		want string
	}{
		{name: "child post", post: &model.Post{Id: model.NewId(), RootId: root}, want: root},
		{name: "top-level post", post: &model.Post{Id: root}, want: root},
		{name: "nil post", post: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, RootPostID(tt.post))
		})
	}
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

	resp, err := svc.ListByTeam(userID, teamID)
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	require.Equal(t, "On team", resp.Entries[0].Title)
}

func TestServiceListByTeamSkipsChannelsTheCallerCannotRead(t *testing.T) {
	kv := newMemoryKV()
	kv.readAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})

	teamID := model.NewId()
	openChannel := model.NewId()
	privateChannel := model.NewId()
	userID := model.NewId()
	kv.channels[openChannel] = &model.Channel{Id: openChannel, TeamId: teamID, Type: model.ChannelTypeOpen}
	kv.channels[privateChannel] = &model.Channel{Id: privateChannel, TeamId: teamID, Type: model.ChannelTypePrivate}
	kv.unreadable = map[string]bool{privateChannel: true}

	_, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: openChannel,
		PostID:    model.NewId(),
		FileID:    model.NewId(),
		Title:     "Open",
	}, []byte("<html>a</html>"), "a.html", userID, "Author", teamID, model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)
	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: privateChannel,
		PostID:    model.NewId(),
		FileID:    model.NewId(),
		Title:     "Private",
	}, []byte("<html>b</html>"), "b.html", userID, "Author", teamID, model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)

	resp, err := svc.ListByTeam(userID, teamID)
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)
	require.Equal(t, "Open", resp.Entries[0].Title)

	kv.permissionAll = true
	resp, err = svc.ListByTeam(userID, teamID)
	require.NoError(t, err)
	require.Len(t, resp.Entries, 2)
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

func TestCreateReplacementPersistsTitleWhenTheHTMLChanges(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	channelID := model.NewId()
	userID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}

	entry, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Demo", Description: "first",
	}, []byte("<html>v1</html>"), "v1.html", userID, "Author", "", model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)

	updated, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), ReplaceID: entry.ID, Title: "Renamed", Description: "second",
	}, []byte("<html>v2</html>"), "v2.html", userID, "Author", "", model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)
	require.Equal(t, "Renamed", updated.Title)
	require.Equal(t, "second", updated.Description)

	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, "Renamed", stored.Title)
	require.Equal(t, 2, stored.Version)
	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, "Renamed", versions[len(versions)-1].Title)
	require.Equal(t, "second", versions[len(versions)-1].Description)
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

	_, err := svc.PurgeChannel(channelID)
	require.NoError(t, err)
	left, err := store.List(channelID)
	require.NoError(t, err)
	require.Empty(t, left)
}

func TestPurgeChannelFinishesAPurgingArtifact(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	channelID := model.NewId()
	userID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}

	entry, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Notes",
	}, []byte("<html>notes</html>"), "notes.html", userID, "Author", "", model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)

	kv.failBlobDeletes = 1
	require.Error(t, store.Purge(entry))
	listed, err := store.List(channelID)
	require.NoError(t, err)
	require.Empty(t, listed)

	_, err = svc.PurgeChannel(channelID)
	require.NoError(t, err)
	_, err = store.Get(entry.ID)
	require.ErrorIs(t, err, ErrNotFound)
	idx, _, err := store.getIndex(channelID)
	require.NoError(t, err)
	require.NotContains(t, idx.IDs, entry.ID)
}

func TestListByTeamFollowsTheChannelCurrentTeam(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	oldTeam := model.NewId()
	newTeam := model.NewId()
	channelID := model.NewId()
	userID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, TeamId: oldTeam, Type: model.ChannelTypeOpen}

	_, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID,
		PostID:    model.NewId(),
		FileID:    model.NewId(),
		Title:     "Moved",
	}, []byte("<html>a</html>"), "a.html", userID, "Author", oldTeam, model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)

	kv.channels[channelID].TeamId = newTeam
	moved, err := svc.ListByTeam(userID, newTeam)
	require.NoError(t, err)
	require.Len(t, moved.Entries, 1)
	require.Equal(t, "Moved", moved.Entries[0].Title)
	left, err := svc.ListByTeam(userID, oldTeam)
	require.NoError(t, err)
	require.Empty(t, left.Entries)
}

func TestPatchReappliesTheTitleAfterAConflict(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	userID := model.NewId()
	channelID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}
	entry, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Notes",
	}, []byte("<html>a</html>"), "a.html", userID, "Author", "", model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)

	kv.conflictNextMeta = true
	kv.onFailNextMeta = func(data map[string][]byte) {
		var stored Entry
		if unmarshalErr := json.Unmarshal(data[metaKey(entry.ID)], &stored); unmarshalErr != nil {
			return
		}
		stored.Description = "from the other write"
		raw, marshalErr := json.Marshal(stored)
		if marshalErr != nil {
			return
		}
		data[metaKey(entry.ID)] = raw
	}
	title := "Renamed"
	updated, err := svc.Patch(userID, entry.ID, PatchRequest{Title: &title})
	require.NoError(t, err)
	require.Equal(t, "Renamed", updated.Title)
	require.Equal(t, "from the other write", updated.Description)
}

func TestPurgeChannelDropsAnIndexIDWhoseMetaIsGone(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	channelID := model.NewId()
	userID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}
	limits := ContentLimits{MaxSize: 1024, MaxSitewide: 2}
	kept, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Kept",
	}, []byte("<html>kept</html>"), "kept.html", userID, "Author", "", model.NewId(), limits)
	require.NoError(t, err)
	gone, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Gone",
	}, []byte("<html>gone</html>"), "gone.html", userID, "Author", "", model.NewId(), limits)
	require.NoError(t, err)
	require.NoError(t, kv.KVDelete(metaKey(gone.ID)))

	_, err = svc.PurgeChannel(channelID)
	require.NoError(t, err)
	_, raw, err := store.getIndex(channelID)
	require.NoError(t, err)
	require.Nil(t, raw)
	_, err = store.Get(gone.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.Get(kept.ID)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Again",
	}, []byte("<html>again</html>"), "again.html", userID, "Author", "", model.NewId(), limits)
	require.NoError(t, err)
	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Twice",
	}, []byte("<html>twice</html>"), "twice.html", userID, "Author", "", model.NewId(), limits)
	require.NoError(t, err)
}

func TestPurgeChannelRejectsInvalidID(t *testing.T) {
	svc := NewService(NewStore(newMemoryKV()), newMemoryKV(), allowAllMembers{})
	_, err := svc.PurgeChannel("not-an-id")
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestDeleteOfTheOnlyVersionKeepsAReplacementThatLandsDuringTheDelete(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	channelID := model.NewId()
	userID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}
	limits := ContentLimits{MaxSize: 1024}

	entry, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Demo",
	}, []byte("<html>v1</html>"), "v1.html", userID, "Author", "", model.NewId(), limits)
	require.NoError(t, err)
	v1 := kv.snapshot()
	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), ReplaceID: entry.ID, Title: "Renamed",
	}, []byte("<html>v2</html>"), "v2.html", userID, "Author", "", model.NewId(), limits)
	require.NoError(t, err)
	v2 := kv.snapshot()
	kv.restore(v1)
	kv.beforeMetaCAS = func(data map[string][]byte) {
		restoreKVData(data, v2)
	}

	require.NoError(t, svc.Delete(userID, entry.ID, false))
	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 1, stored.Version)
	body, err := store.ReadContent(stored)
	require.NoError(t, err)
	require.Equal(t, "<html>v1</html>", string(body))
}

func TestExplicitPurgeRemovesAReplacementThatLandsDuringTheDelete(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	channelID := model.NewId()
	userID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}
	limits := ContentLimits{MaxSize: 1024}

	entry, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Demo",
	}, []byte("<html>v1</html>"), "v1.html", userID, "Author", "", model.NewId(), limits)
	require.NoError(t, err)
	v1 := kv.snapshot()
	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), ReplaceID: entry.ID, Title: "Renamed",
	}, []byte("<html>v2</html>"), "v2.html", userID, "Author", "", model.NewId(), limits)
	require.NoError(t, err)
	v2 := kv.snapshot()
	kv.restore(v1)
	kv.beforeMetaCAS = func(data map[string][]byte) {
		restoreKVData(data, v2)
	}

	require.NoError(t, svc.Delete(userID, entry.ID, true))
	_, err = store.Get(entry.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCreateReplacementKeepsATitlePatchedAfterTheLoad(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	channelID := model.NewId()
	userID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}

	entry, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Demo", Description: "first",
	}, []byte("<html>v1</html>"), "v1.html", userID, "Author", "", model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)

	kv.conflictNextMeta = true
	kv.onFailNextMeta = func(data map[string][]byte) {
		var stored Entry
		require.NoError(t, json.Unmarshal(data[metaKey(entry.ID)], &stored))
		stored.Title = "Patched"
		raw, marshalErr := json.Marshal(stored)
		require.NoError(t, marshalErr)
		data[metaKey(entry.ID)] = raw
	}

	updated, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), ReplaceID: entry.ID, Description: "second",
	}, []byte("<html>v2</html>"), "v2.html", userID, "Author", "", model.NewId(), ContentLimits{MaxSize: 1024})
	require.NoError(t, err)
	require.Equal(t, "Patched", updated.Title)
	require.Equal(t, "second", updated.Description)
	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, "Patched", stored.Title)
	require.Equal(t, 2, stored.Version)
}

func TestCreateReplacementLeavesMetadataWhenTheContentIsRejected(t *testing.T) {
	kv := newMemoryKV()
	kv.permissionAll = true
	store := NewStore(kv)
	svc := NewService(store, kv, allowAllMembers{})
	channelID := model.NewId()
	userID := model.NewId()
	kv.channels[channelID] = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}
	limits := ContentLimits{MaxSize: 1024}

	entry, err := svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Demo",
	}, []byte("<html>v1</html>"), "v1.html", userID, "Author", "", model.NewId(), limits)
	require.NoError(t, err)

	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), ReplaceID: entry.ID, Title: "Renamed",
	}, nil, "v2.html", userID, "Author", "", model.NewId(), limits)
	require.ErrorIs(t, err, ErrInvalidInput)

	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), ReplaceID: entry.ID, Title: "Renamed",
	}, []byte("<html>too-big</html>"), "v2.html", userID, "Author", "", model.NewId(), ContentLimits{MaxSize: 4})
	require.ErrorIs(t, err, ErrTooLarge)

	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), ReplaceID: entry.ID, Title: strings.Repeat("a", MaxTitleRunes+1),
	}, []byte("<html>v2</html>"), "v2.html", userID, "Author", "", model.NewId(), limits)
	require.ErrorIs(t, err, ErrInvalidInput)

	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), Title: "Other",
	}, []byte("<html>shared</html>"), "shared.html", userID, "Author", "", model.NewId(), limits)
	require.NoError(t, err)
	_, err = svc.CreateFromHTML(userID, CreateRequest{
		ChannelID: channelID, PostID: model.NewId(), FileID: model.NewId(), ReplaceID: entry.ID, Title: "Renamed",
	}, []byte("<html>shared</html>"), "shared.html", userID, "Author", "", model.NewId(), limits)
	require.ErrorIs(t, err, ErrDuplicate)

	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, "Demo", stored.Title)
	require.Equal(t, 1, stored.Version)
}
