// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
)

type testBots map[string]string

func (b testBots) IsAnyBot(userID string) bool {
	_, ok := b[userID]
	return ok
}

func (b testBots) AgentDisplayName(userID string) string { return b[userID] }

func hashHex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

const testHTML = "<html>dashboard</html>"

func TestServiceCanAndCanRead(t *testing.T) {
	user := model.NewId()
	channelID := model.NewId()
	readOnly := map[*model.Permission]bool{model.PermissionReadChannel: true}
	everything := map[*model.Permission]bool{
		model.PermissionReadChannel:                  true,
		model.PermissionAddBookmarkPublicChannel:     true,
		model.PermissionEditBookmarkPublicChannel:    true,
		model.PermissionDeleteBookmarkPublicChannel:  true,
		model.PermissionAddBookmarkPrivateChannel:    true,
		model.PermissionDeleteBookmarkPrivateChannel: true,
		model.PermissionEditBookmarkPrivateChannel:   true,
		model.PermissionOrderBookmarkPublicChannel:   true,
		model.PermissionOrderBookmarkPrivateChannel:  true,
	}

	tests := []struct {
		name     string
		channel  *model.Channel
		getErr   error
		allowed  map[*model.Permission]bool
		op       channelartifacts.Op
		wantCan  bool
		wantRead bool
	}{
		{
			name: "public channel with the add permission", op: channelartifacts.OpAdd, wantCan: true, wantRead: true,
			channel: &model.Channel{Id: channelID, Type: model.ChannelTypeOpen},
			allowed: map[*model.Permission]bool{model.PermissionReadChannel: true, model.PermissionAddBookmarkPublicChannel: true},
		},
		{
			name: "public channel without the edit permission", op: channelartifacts.OpEdit, wantRead: true,
			channel: &model.Channel{Id: channelID, Type: model.ChannelTypeOpen},
			allowed: map[*model.Permission]bool{model.PermissionReadChannel: true, model.PermissionAddBookmarkPublicChannel: true},
		},
		{
			name: "private channel uses the private delete permission", op: channelartifacts.OpDelete, wantCan: true, wantRead: true,
			channel: &model.Channel{Id: channelID, Type: model.ChannelTypePrivate},
			allowed: map[*model.Permission]bool{model.PermissionReadChannel: true, model.PermissionDeleteBookmarkPrivateChannel: true},
		},
		{
			name: "archived channel is readable but not writable", op: channelartifacts.OpAdd, wantRead: true,
			channel: &model.Channel{Id: channelID, Type: model.ChannelTypeOpen, DeleteAt: 1},
			allowed: everything,
		},
		{name: "lookup failure fails closed", op: channelartifacts.OpAdd, getErr: errors.New("db down"), allowed: everything},
		{
			name: "direct message has no library", op: channelartifacts.OpAdd,
			channel: &model.Channel{Id: channelID, Type: model.ChannelTypeDirect}, allowed: everything,
		},
		{
			name: "group message has no library", op: channelartifacts.OpAdd,
			channel: &model.Channel{Id: channelID, Type: model.ChannelTypeGroup}, allowed: readOnly,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mm := mocks.NewMockClient(t)
			mm.EXPECT().GetChannel(channelID).Return(tt.channel, tt.getErr).Maybe()
			mm.EXPECT().HasPermissionToChannel(user, channelID, mock.Anything).RunAndReturn(
				func(_, _ string, p *model.Permission) bool { return tt.allowed[p] }).Maybe()

			svc := channelartifacts.NewService(channelartifacts.NewStore(nil), mm, testBots{})
			assert.Equal(t, tt.wantCan, svc.Can(user, channelID, tt.op))
			assert.Equal(t, tt.wantRead, svc.CanRead(user, channelID))
		})
	}
}

func TestServiceRefusesDirectAndGroupMessages(t *testing.T) {
	for _, channelType := range []model.ChannelType{model.ChannelTypeDirect, model.ChannelTypeGroup} {
		t.Run(string(channelType), func(t *testing.T) {
			e := newServiceEnv(t)
			dm := model.NewId()
			e.mm.EXPECT().GetChannel(dm).Return(&model.Channel{Id: dm, Type: channelType}, nil).Maybe()
			e.mm.EXPECT().HasPermissionToChannel(e.userID, dm, mock.Anything).Return(true).Maybe()
			postID, fileID := model.NewId(), model.NewId()
			seedSource(t, e.db, postID, fileID)
			e.mm.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, PostId: postID, Name: "a.html", Extension: "html", Size: 5}, nil).Maybe()
			e.mm.EXPECT().GetPost(postID).Return(&model.Post{Id: postID, UserId: e.botID, ChannelId: dm, FileIds: model.StringArray{fileID}}, nil).Maybe()
			e.mm.EXPECT().GetFile(fileID).RunAndReturn(func(string) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("<html>")), nil
			}).Maybe()

			_, err := e.svc.Save(e.userID, e.mm, channelartifacts.SaveRequest{ChannelID: dm, PostID: postID, FileID: fileID}, channelartifacts.Limits{MaxSize: 1 << 20})
			require.ErrorIs(t, err, channelartifacts.ErrNotFound)
			_, err = e.svc.List(e.userID, dm)
			require.ErrorIs(t, err, channelartifacts.ErrNotFound)

			var rows int
			require.NoError(t, e.db.Get(&rows, `SELECT COUNT(*) FROM Agents_ChannelArtifacts WHERE ChannelID = $1`, dm))
			assert.Zero(t, rows)
		})
	}
}

type serviceEnv struct {
	db        *sqlx.DB
	store     *channelartifacts.Store
	svc       *channelartifacts.Service
	mm        *mocks.MockClient
	channelID string
	botID     string
	userID    string
	postID    string
	fileID    string
}

// newServiceEnv wires a real database, a bot that posted testHTML as one file,
// and a user who may do everything in the channel.
func newServiceEnv(t *testing.T) *serviceEnv {
	t.Helper()
	db := testDB(t)
	env := &serviceEnv{
		mm:        mocks.NewMockClient(t),
		channelID: model.NewId(),
		botID:     model.NewId(),
		userID:    model.NewId(),
		postID:    model.NewId(),
		fileID:    model.NewId(),
	}
	seedSource(t, db, env.postID, env.fileID)
	env.db = db
	env.store = channelartifacts.NewStore(db)
	env.svc = channelartifacts.NewService(env.store, env.mm, testBots{env.botID: "Helper"})

	env.mm.EXPECT().GetChannel(env.channelID).Return(&model.Channel{Id: env.channelID, Type: model.ChannelTypeOpen}, nil).Maybe()
	env.mm.EXPECT().HasPermissionToChannel(env.userID, env.channelID, mock.Anything).Return(true).Maybe()
	return env
}

func (e *serviceEnv) expectSource(authorID string, info *model.FileInfo, postChannel string, fileIDs []string) {
	e.mm.EXPECT().GetFileInfo(e.fileID).Return(info, nil).Maybe()
	e.mm.EXPECT().GetPost(e.postID).Return(&model.Post{Id: e.postID, UserId: authorID, ChannelId: postChannel, FileIds: fileIDs}, nil).Maybe()
	e.mm.EXPECT().GetFile(e.fileID).RunAndReturn(func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(testHTML)), nil
	}).Maybe()
}

func (e *serviceEnv) htmlInfo() *model.FileInfo {
	return &model.FileInfo{Id: e.fileID, PostId: e.postID, Name: "dashboard.html", Extension: "html", Size: int64(len(testHTML))}
}

func TestServiceSaveChecksTheSource(t *testing.T) {
	tests := []struct {
		name    string
		author  func(e *serviceEnv) string
		info    func(e *serviceEnv) *model.FileInfo
		channel func(e *serviceEnv) string
		files   func(e *serviceEnv) []string
		maxSize int64
		wantErr error
	}{
		{name: "agent post", wantErr: nil},
		{name: "person's post", author: func(e *serviceEnv) string { return e.userID }, wantErr: channelartifacts.ErrIneligible},
		{
			name: "not an html file",
			info: func(e *serviceEnv) *model.FileInfo {
				i := e.htmlInfo()
				i.Extension, i.Name = "txt", "notes.txt"
				return i
			},
			wantErr: channelartifacts.ErrIneligible,
		},
		{name: "post in another channel", channel: func(*serviceEnv) string { return model.NewId() }, wantErr: channelartifacts.ErrIneligible},
		{name: "file not on the post", files: func(*serviceEnv) []string { return []string{model.NewId()} }, wantErr: channelartifacts.ErrIneligible},
		{
			name: "file deleted",
			info: func(e *serviceEnv) *model.FileInfo {
				i := e.htmlInfo()
				i.DeleteAt = 1
				return i
			},
			wantErr: channelartifacts.ErrNotFound,
		},
		{name: "larger than the limit", maxSize: 5, wantErr: channelartifacts.ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newServiceEnv(t)
			author, info, channel, files := e.botID, e.htmlInfo(), e.channelID, []string{e.fileID}
			if tt.author != nil {
				author = tt.author(e)
			}
			if tt.info != nil {
				info = tt.info(e)
			}
			if tt.channel != nil {
				channel = tt.channel(e)
			}
			if tt.files != nil {
				files = tt.files(e)
			}
			e.expectSource(author, info, channel, files)
			maxSize := tt.maxSize
			if maxSize == 0 {
				maxSize = 1 << 20
			}

			entry, err := e.svc.Save(e.userID, e.mm, channelartifacts.SaveRequest{ChannelID: e.channelID, PostID: e.postID, FileID: e.fileID}, channelartifacts.Limits{MaxSize: maxSize})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				list, listErr := e.svc.List(e.userID, e.channelID)
				require.NoError(t, listErr)
				assert.Empty(t, list.Entries, "a rejected file leaves no row")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "dashboard", entry.Title)
			assert.Equal(t, hashHex([]byte(testHTML)), entry.SHA256)
			assert.Equal(t, e.fileID, entry.SourceFileID)
			assert.Equal(t, "Helper", entry.SourceAgentDisplayName)
			assert.True(t, entry.Available)
		})
	}
}

func TestServiceSaveRequiresThePermissionForTheOperation(t *testing.T) {
	e := newServiceEnv(t)
	stranger := model.NewId()
	e.mm.EXPECT().HasPermissionToChannel(stranger, e.channelID, mock.Anything).RunAndReturn(
		func(_, _ string, p *model.Permission) bool { return p == model.PermissionReadChannel }).Maybe()
	e.expectSource(e.botID, e.htmlInfo(), e.channelID, []string{e.fileID})

	_, err := e.svc.Save(stranger, e.mm, channelartifacts.SaveRequest{ChannelID: e.channelID, PostID: e.postID, FileID: e.fileID}, channelartifacts.Limits{MaxSize: 1 << 20})
	require.ErrorIs(t, err, channelartifacts.ErrForbidden, "a reader without the add permission is told so")

	outsider := model.NewId()
	e.mm.EXPECT().HasPermissionToChannel(outsider, e.channelID, mock.Anything).Return(false).Maybe()
	_, err = e.svc.Save(outsider, e.mm, channelartifacts.SaveRequest{ChannelID: e.channelID, PostID: e.postID, FileID: e.fileID}, channelartifacts.Limits{MaxSize: 1 << 20})
	require.ErrorIs(t, err, channelartifacts.ErrNotFound, "someone who cannot read the channel learns nothing")
}

func TestServiceReadContentFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, e *serviceEnv, entry *channelartifacts.Entry)
		wantErr error
	}{
		{
			name: "allowed",
			setup: func(t *testing.T, e *serviceEnv, _ *channelartifacts.Entry) {
				e.mm.EXPECT().HasPermissionToFileAction("sess", e.fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true)
			},
		},
		{
			name: "download policy denies the session",
			setup: func(t *testing.T, e *serviceEnv, _ *channelartifacts.Entry) {
				e.mm.EXPECT().HasPermissionToFileAction("sess", e.fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(false)
			},
			wantErr: channelartifacts.ErrNotFound,
		},
		{
			name: "source file deleted",
			setup: func(t *testing.T, e *serviceEnv, entry *channelartifacts.Entry) {
				_, err := e.db.Exec(`UPDATE FileInfo SET DeleteAt = 1 WHERE Id = $1`, e.fileID)
				require.NoError(t, err)
				entry.Available = false
			},
			wantErr: channelartifacts.ErrUnavailable,
		},
		{
			name: "content no longer matches the recorded hash",
			setup: func(t *testing.T, e *serviceEnv, entry *channelartifacts.Entry) {
				entry.SHA256 = hashHex([]byte("something else"))
				e.mm.EXPECT().HasPermissionToFileAction("sess", e.fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true)
			},
			wantErr: channelartifacts.ErrUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newServiceEnv(t)
			e.expectSource(e.botID, e.htmlInfo(), e.channelID, []string{e.fileID})
			entry, err := e.svc.Save(e.userID, e.mm, channelartifacts.SaveRequest{ChannelID: e.channelID, PostID: e.postID, FileID: e.fileID}, channelartifacts.Limits{MaxSize: 1 << 20})
			require.NoError(t, err)
			tt.setup(t, e, entry)

			body, err := e.svc.ReadContent("sess", entry, 0, 1<<20)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testHTML, string(body))
		})
	}

	t.Run("no session fails closed", func(t *testing.T) {
		e := newServiceEnv(t)
		e.expectSource(e.botID, e.htmlInfo(), e.channelID, []string{e.fileID})
		entry, err := e.svc.Save(e.userID, e.mm, channelartifacts.SaveRequest{ChannelID: e.channelID, PostID: e.postID, FileID: e.fileID}, channelartifacts.Limits{MaxSize: 1 << 20})
		require.NoError(t, err)
		e.mm.EXPECT().HasPermissionToFileAction("", e.fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(true)
		_, err = e.svc.ReadContent("", entry, 0, 1<<20)
		require.ErrorIs(t, err, channelartifacts.ErrNotFound)
	})
}

func TestServiceCheckReadableAppliesTheFilePolicy(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		allowed   bool
		breakFile bool
		wantErr   error
	}{
		{name: "allowed", sessionID: "sess", allowed: true},
		{name: "download policy denies the session", sessionID: "sess", allowed: false, wantErr: channelartifacts.ErrNotFound},
		{name: "no session fails closed", sessionID: "", allowed: false, wantErr: channelartifacts.ErrNotFound},
		{name: "source file deleted", sessionID: "sess", allowed: true, breakFile: true, wantErr: channelartifacts.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newServiceEnv(t)
			e.expectSource(e.botID, e.htmlInfo(), e.channelID, []string{e.fileID})
			entry, err := e.svc.Save(e.userID, e.mm, channelartifacts.SaveRequest{ChannelID: e.channelID, PostID: e.postID, FileID: e.fileID}, channelartifacts.Limits{MaxSize: 1 << 20})
			require.NoError(t, err)
			if tt.breakFile {
				_, execErr := e.db.Exec(`UPDATE FileInfo SET DeleteAt = 1 WHERE Id = $1`, e.fileID)
				require.NoError(t, execErr)
				entry.Available = false
			}
			e.mm.EXPECT().HasPermissionToFileAction(tt.sessionID, e.fileID, model.AccessControlPolicyActionDownloadFileAttachment).Return(tt.allowed).Maybe()

			err = e.svc.CheckReadable(tt.sessionID, entry, 0)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestServiceRollbackRefusesAnUnavailableVersion(t *testing.T) {
	e := newServiceEnv(t)
	e.expectSource(e.botID, e.htmlInfo(), e.channelID, []string{e.fileID})
	entry, err := e.svc.Save(e.userID, e.mm, channelartifacts.SaveRequest{ChannelID: e.channelID, PostID: e.postID, FileID: e.fileID}, channelartifacts.Limits{MaxSize: 1 << 20})
	require.NoError(t, err)

	secondPost, secondFile := model.NewId(), model.NewId()
	seedSource(t, e.db, secondPost, secondFile)
	_, err = e.db.Exec(`UPDATE Posts SET DeleteAt = 0 WHERE Id = $1`, secondPost)
	require.NoError(t, err)
	require.NoError(t, e.store.AddVersion(entry.ID, channelartifacts.NewVersion{
		PostID: secondPost, FileID: secondFile, SHA256: hashHex([]byte("v2")), Size: 2,
		SourceBotUserID: e.botID, UserID: e.userID, At: model.GetMillis(),
	}, 5))

	_, err = e.db.Exec(`UPDATE FileInfo SET DeleteAt = 1 WHERE Id = $1`, e.fileID)
	require.NoError(t, err)

	_, err = e.svc.Rollback(e.userID, entry.ID, 1)
	require.ErrorIs(t, err, channelartifacts.ErrUnavailable)
	current, err := e.svc.GetReadable(e.userID, entry.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, current.Version, "a refused rollback leaves the current version alone")

	versions, err := e.svc.Versions(e.userID, entry.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.True(t, versions[0].Available)
	assert.False(t, versions[1].Available)
}

func TestServiceVersionOfNamesTheSourceAgentForEveryVersion(t *testing.T) {
	e := newServiceEnv(t)
	e.expectSource(e.botID, e.htmlInfo(), e.channelID, []string{e.fileID})
	entry, err := e.svc.Save(e.userID, e.mm, channelartifacts.SaveRequest{ChannelID: e.channelID, PostID: e.postID, FileID: e.fileID}, channelartifacts.Limits{MaxSize: 1 << 20})
	require.NoError(t, err)

	secondPost, secondFile := model.NewId(), model.NewId()
	seedSource(t, e.db, secondPost, secondFile)
	require.NoError(t, e.store.AddVersion(entry.ID, channelartifacts.NewVersion{
		PostID: secondPost, FileID: secondFile, SHA256: hashHex([]byte("v2")), Size: 2,
		SourceBotUserID: e.botID, UserID: e.userID, At: model.GetMillis(),
	}, 5))
	current, err := e.svc.GetReadable(e.userID, entry.ID)
	require.NoError(t, err)

	for _, version := range []int{0, 1, 2} {
		rec, recErr := e.svc.VersionOf(current, version)
		require.NoError(t, recErr)
		assert.Equal(t, "Helper", rec.SourceAgentDisplayName, "version %d", version)
	}
}

func TestServiceVersionOfReportsWhoSavedEachVersionAndWhen(t *testing.T) {
	e := newServiceEnv(t)
	e.expectSource(e.botID, e.htmlInfo(), e.channelID, []string{e.fileID})
	entry, err := e.svc.Save(e.userID, e.mm, channelartifacts.SaveRequest{ChannelID: e.channelID, PostID: e.postID, FileID: e.fileID}, channelartifacts.Limits{MaxSize: 1 << 20})
	require.NoError(t, err)

	secondPost, secondFile, saver := model.NewId(), model.NewId(), model.NewId()
	seedSource(t, e.db, secondPost, secondFile)
	savedAt := model.GetMillis() + 5000
	require.NoError(t, e.store.AddVersion(entry.ID, channelartifacts.NewVersion{
		PostID: secondPost, FileID: secondFile, SHA256: hashHex([]byte("v2")), Size: 2,
		SourceBotUserID: e.botID, UserID: saver, At: savedAt,
	}, 5))
	current, err := e.svc.GetReadable(e.userID, entry.ID)
	require.NoError(t, err)

	tests := []struct {
		name         string
		version      int
		wantVersion  int
		wantSavedBy  string
		wantSavedAt  int64
		wantIsHead   bool
		wantSourceID string
	}{
		{name: "current version by default", version: 0, wantVersion: 2, wantSavedBy: saver, wantSavedAt: savedAt, wantIsHead: true, wantSourceID: secondFile},
		{name: "current version by number", version: 2, wantVersion: 2, wantSavedBy: saver, wantSavedAt: savedAt, wantIsHead: true, wantSourceID: secondFile},
		{name: "first version", version: 1, wantVersion: 1, wantSavedBy: e.userID, wantSavedAt: entry.CreateAt, wantSourceID: e.fileID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := e.svc.VersionOf(current, tt.version)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVersion, rec.Version)
			assert.Equal(t, tt.wantSavedBy, rec.CreatedBy, "the version's own author, not the artifact's creator")
			assert.Equal(t, tt.wantSavedAt, rec.CreateAt)
			assert.Equal(t, tt.wantIsHead, rec.Head)
			assert.Equal(t, tt.wantSourceID, rec.SourceFileID)
		})
	}
}
