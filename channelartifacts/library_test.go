// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts_test

import (
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts/librarytest"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	libraryMaxSize       = 4096
	libraryMaxPerChannel = 3
	librarySession       = "abcdefghijklmnopqrstuvwxyz"
)

// postFixture is Mattermost as the post-backed library sees it: channels,
// permissions, and the uploads and posts the library creates, kept in memory
// and in the database tables the library queries.
type postFixture struct {
	db  *sqlx.DB
	mm  *mocks.MockClient
	lib channelartifacts.Manager
	svc *channelartifacts.Service

	botID, writerID, readerID, outsiderID string
	channel, otherChannel                 string

	mu       sync.Mutex
	content  map[string]string
	infos    map[string]*model.FileInfo
	posts    map[string]*model.Post
	events   []string
	deleted  []string
	uploads  int
	created  int
	filePass bool
}

func newPostFixture(t *testing.T) *postFixture {
	t.Helper()
	f := &postFixture{
		db: testDB(t), mm: mocks.NewMockClient(t),
		botID: model.NewId(), writerID: model.NewId(), readerID: model.NewId(), outsiderID: model.NewId(),
		channel: model.NewId(), otherChannel: model.NewId(),
		content: map[string]string{}, infos: map[string]*model.FileInfo{}, posts: map[string]*model.Post{}, filePass: true,
	}
	f.svc = channelartifacts.NewService(channelartifacts.NewStore(f.db), f.mm, testBots{f.botID: "Helper"})
	f.lib = channelartifacts.NewLibrary(f.svc, func() channelartifacts.Limits {
		return channelartifacts.Limits{MaxSize: libraryMaxSize, MaxPerChannel: libraryMaxPerChannel, VersionsToKeep: 5}
	})

	f.mm.EXPECT().GetChannel(mock.Anything).RunAndReturn(func(id string) (*model.Channel, error) {
		return &model.Channel{Id: id, Type: model.ChannelTypeOpen}, nil
	}).Maybe()
	f.mm.EXPECT().HasPermissionToChannel(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(user, _ string, p *model.Permission) bool {
			switch user {
			case f.writerID, f.botID:
				return true
			case f.readerID:
				return p == model.PermissionReadChannel
			default:
				return false
			}
		}).Maybe()
	f.mm.EXPECT().HasPermissionToFileAction(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(session, _, _ string) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.filePass && session != ""
		}).Maybe()
	f.mm.EXPECT().GetConfig().Return(&model.Config{}).Maybe()
	f.mm.EXPECT().PublishWebSocketEvent(mock.Anything, mock.Anything, mock.Anything).Run(func(event string, _ map[string]any, _ *model.WebsocketBroadcast) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.events = append(f.events, event)
	}).Return().Maybe()
	f.mm.EXPECT().UploadFile(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(f.upload).Maybe()
	f.mm.EXPECT().CreatePost(mock.Anything).RunAndReturn(f.createPost).Maybe()
	f.mm.EXPECT().DeletePost(mock.Anything).RunAndReturn(f.deletePost).Maybe()
	f.mm.EXPECT().GetPost(mock.Anything).RunAndReturn(func(id string) (*model.Post, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if post, ok := f.posts[id]; ok {
			return post, nil
		}
		return nil, fmt.Errorf("no post %s", id)
	}).Maybe()
	f.mm.EXPECT().GetFileInfo(mock.Anything).RunAndReturn(func(id string) (*model.FileInfo, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if info, ok := f.infos[id]; ok {
			return info, nil
		}
		return nil, fmt.Errorf("no file %s", id)
	}).Maybe()
	f.mm.EXPECT().GetFile(mock.Anything).RunAndReturn(func(id string) (io.ReadCloser, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		body, ok := f.content[id]
		if !ok {
			return nil, fmt.Errorf("no file %s", id)
		}
		return io.NopCloser(strings.NewReader(body)), nil
	}).Maybe()
	f.mm.EXPECT().LogError(mock.Anything, mock.Anything).Maybe()
	return f
}

func (f *postFixture) upload(r io.Reader, name, channelID string) (*model.FileInfo, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	info := &model.FileInfo{
		Id: model.NewId(), Name: name, Extension: strings.TrimPrefix(path.Ext(name), "."), ChannelId: channelID, Size: int64(len(body)),
	}
	f.content[info.Id] = string(body)
	f.infos[info.Id] = info
	f.uploads++
	return info, nil
}

func (f *postFixture) createPost(post *model.Post) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	post.Id = model.NewId()
	f.created++
	f.posts[post.Id] = post.Clone()
	if _, err := f.db.Exec(`INSERT INTO Posts (Id, ChannelId) VALUES ($1, $2)`, post.Id, post.ChannelId); err != nil {
		return err
	}
	for _, fileID := range post.FileIds {
		f.infos[fileID].PostId = post.Id
		if _, err := f.db.Exec(`INSERT INTO FileInfo (Id, PostId) VALUES ($1, $2)`, fileID, post.Id); err != nil {
			return err
		}
	}
	return nil
}

func (f *postFixture) deletePost(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	delete(f.posts, id)
	_, err := f.db.Exec(`UPDATE Posts SET DeleteAt = 1 WHERE Id = $1`, id)
	return err
}

func (f *postFixture) setFilePolicy(allowed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filePass = allowed
}

func (f *postFixture) eventNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *postFixture) actors() (writer, unattended, reader, outsider channelartifacts.Actor) {
	return channelartifacts.Actor{UserID: f.writerID, BotUserID: f.botID, SessionID: librarySession},
		channelartifacts.Actor{UserID: f.writerID, BotUserID: f.botID},
		channelartifacts.Actor{UserID: f.readerID, SessionID: librarySession},
		channelartifacts.Actor{UserID: f.outsiderID, SessionID: librarySession}
}

func TestPostBackedLibraryMeetsTheContract(t *testing.T) {
	librarytest.Run(t, func(t *testing.T) *librarytest.Harness {
		f := newPostFixture(t)
		writer, unattended, reader, outsider := f.actors()
		return &librarytest.Harness{
			Library: f.lib, Channel: f.channel, OtherChannel: f.otherChannel,
			Writer: writer, Unattended: unattended, Reader: reader, Outsider: outsider,
			MaxSize: libraryMaxSize, MaxPerChannel: libraryMaxPerChannel,
		}
	})
}

func TestPostBackedLibraryPublishesAndPosts(t *testing.T) {
	f := newPostFixture(t)
	_, unattended, _, _ := f.actors()
	ctx := t.Context()

	entry, err := f.lib.Write(ctx, unattended, channelartifacts.WriteRequest{
		ChannelID: f.channel, Title: "Wake note", Format: channelartifacts.FormatMarkdown,
		Body: []byte("# Note"),
	})
	require.NoError(t, err)

	post := f.posts[entry.SourcePostID]
	require.NotNil(t, post)
	assert.Equal(t, f.botID, post.UserId, "the message the library keeps is the agent's")
	assert.Equal(t, f.channel, post.ChannelId)
	assert.Equal(t, f.botID, entry.SourceBotUserID)
	assert.Equal(t, f.writerID, entry.CreatedBy, "the library records who the write was made for")
	assert.Equal(t, "Wake note.md", f.infos[entry.SourceFileID].Name)

	_, err = f.lib.Write(ctx, unattended, channelartifacts.WriteRequest{
		ChannelID: f.channel, ArtifactID: entry.ID,
		Body: []byte("# Note 2"),
	})
	require.NoError(t, err)
	_, err = f.lib.Rename(ctx, unattended, entry.ID, "Renamed")
	require.NoError(t, err)
	_, err = f.lib.Rollback(ctx, unattended, entry.ID, 1)
	require.NoError(t, err)
	_, err = f.lib.RemoveVersion(ctx, unattended, entry.ID, 2)
	require.NoError(t, err)
	_, err = f.lib.Remove(ctx, unattended, entry.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{
		channelartifacts.EventCreated, channelartifacts.EventUpdated, channelartifacts.EventUpdated,
		channelartifacts.EventUpdated, channelartifacts.EventUpdated, channelartifacts.EventDeleted,
	}, f.eventNames())
}

func TestPostBackedLibraryChecksAReplacementBeforeItUploadsAnything(t *testing.T) {
	f := newPostFixture(t)
	writer, unattended, reader, outsider := f.actors()
	existing, err := f.lib.Write(t.Context(), writer, channelartifacts.WriteRequest{
		SaveRequest: channelartifacts.SaveRequest{ChannelID: f.channel, Title: "Charter", Format: channelartifacts.FormatMarkdown},
		Body:        []byte("# Charter"),
	})
	require.NoError(t, err)
	uploads, created := f.uploads, f.created

	tests := []struct {
		name  string
		actor channelartifacts.Actor
		req   channelartifacts.SaveRequest
		want  error
	}{
		{"an artifact in another channel", unattended, channelartifacts.SaveRequest{ChannelID: f.otherChannel, ArtifactID: existing.ID}, channelartifacts.ErrNotFound},
		{"an artifact that does not exist", unattended, channelartifacts.SaveRequest{ChannelID: f.channel, ArtifactID: model.NewId()}, channelartifacts.ErrNotFound},
		{"an id that is not an id", unattended, channelartifacts.SaveRequest{ChannelID: f.channel, ArtifactID: "nope"}, channelartifacts.ErrInvalidInput},
		{"another format than the artifact's", unattended, channelartifacts.SaveRequest{ChannelID: f.channel, ArtifactID: existing.ID, Format: channelartifacts.FormatHTML}, channelartifacts.ErrInvalidInput},
		{"someone who may only read", reader, channelartifacts.SaveRequest{ChannelID: f.channel, ArtifactID: existing.ID}, channelartifacts.ErrForbidden},
		{"someone who cannot read the channel", outsider, channelartifacts.SaveRequest{ChannelID: f.channel, ArtifactID: existing.ID}, channelartifacts.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.lib.Write(t.Context(), tt.actor, channelartifacts.WriteRequest{SaveRequest: tt.req, Body: []byte("# New")})
			require.ErrorIs(t, err, tt.want)
			assert.Equal(t, uploads, f.uploads, "nothing is uploaded for a refused replacement")
			assert.Equal(t, created, f.created, "no message is posted for a refused replacement")
			assert.Empty(t, f.deleted, "so there is nothing to clean up")
		})
	}

	versions, err := f.lib.Versions(t.Context(), writer, existing.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 1)
}

func TestPostBackedLibraryRemovesTheMessageOfARefusedWrite(t *testing.T) {
	f := newPostFixture(t)
	_, unattended, _, _ := f.actors()
	req := channelartifacts.WriteRequest{
		ChannelID: f.channel, Title: "One",
		Body: []byte("<html>same</html>"),
	}
	_, err := f.lib.Write(t.Context(), unattended, req)
	require.NoError(t, err)
	assert.Empty(t, f.deleted, "a saved artifact keeps its message")

	req.Title = "Two"
	_, err = f.lib.Write(t.Context(), unattended, req)
	require.ErrorIs(t, err, channelartifacts.ErrDuplicate)
	assert.Len(t, f.deleted, 1, "the message posted for the refused write is removed")
	assert.Len(t, f.events, 1, "a refused write publishes nothing")
}

func TestPostBackedLibraryAppliesTheFilePolicyWhenTheActorHasASession(t *testing.T) {
	f := newPostFixture(t)
	writer, unattended, _, _ := f.actors()
	req := func(body string) channelartifacts.WriteRequest {
		return channelartifacts.WriteRequest{
			ChannelID: f.channel, Title: "t" + body,
			Body: []byte(body),
		}
	}

	f.setFilePolicy(false)
	_, err := f.lib.Write(t.Context(), writer, req("<html>a</html>"))
	require.ErrorIs(t, err, channelartifacts.ErrNotFound, "a session that cannot download the file cannot register it")
	assert.Len(t, f.deleted, 1)

	_, err = f.lib.Write(t.Context(), unattended, req("<html>b</html>"))
	require.NoError(t, err, "without a session the agent's own content needs no file policy")
}

func TestPostBackedLibraryThreadsTheMessageWithTheResponse(t *testing.T) {
	f := newPostFixture(t)
	_, unattended, _, _ := f.actors()
	root := model.NewId()
	response := &model.Post{Id: model.NewId(), RootId: root, ChannelId: f.channel}
	f.posts[response.Id] = response

	entry, err := f.lib.Write(t.Context(), unattended, channelartifacts.WriteRequest{
		ChannelID: f.channel, Title: "Threaded",
		Body:           []byte("<html>t</html>"),
		ThreadOfPostID: response.Id,
	})
	require.NoError(t, err)
	assert.Equal(t, root, f.posts[entry.SourcePostID].RootId)
}

func TestPostBackedLibraryRegistersMarkdownFiles(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		format    channelartifacts.Format
		want      channelartifacts.Format
		wantErr   error
		botPoster bool
	}{
		{name: "markdown by extension", file: "charter.md", want: channelartifacts.FormatMarkdown, botPoster: true},
		{name: "markdown extension variant", file: "charter.markdown", want: channelartifacts.FormatMarkdown, botPoster: true},
		{name: "stated format matches", file: "charter.md", format: channelartifacts.FormatMarkdown, want: channelartifacts.FormatMarkdown, botPoster: true},
		{name: "stated format does not match", file: "charter.md", format: channelartifacts.FormatHTML, wantErr: channelartifacts.ErrIneligible, botPoster: true},
		{name: "html by extension", file: "page.html", want: channelartifacts.FormatHTML, botPoster: true},
		{name: "other file types", file: "notes.txt", wantErr: channelartifacts.ErrIneligible, botPoster: true},
		{name: "a person's file", file: "charter.md", wantErr: channelartifacts.ErrIneligible},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPostFixture(t)
			writer, _, _, _ := f.actors()
			author := f.writerID
			if tt.botPoster {
				author = f.botID
			}
			info, err := f.upload(strings.NewReader("# Charter"), tt.file, f.channel)
			require.NoError(t, err)
			post := &model.Post{UserId: author, ChannelId: f.channel, FileIds: model.StringArray{info.Id}}
			require.NoError(t, f.createPost(post))

			entry, err := f.lib.Write(t.Context(), writer, channelartifacts.WriteRequest{
				ChannelID: f.channel, FileID: info.Id, Format: tt.format,
			})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, entry.Format)
			assert.Equal(t, post.Id, entry.SourcePostID, "the post comes from the file when it is not given")
		})
	}
}

func TestFormatOfFile(t *testing.T) {
	tests := []struct {
		info   model.FileInfo
		want   channelartifacts.Format
		wantOK bool
	}{
		{info: model.FileInfo{Extension: "html"}, want: channelartifacts.FormatHTML, wantOK: true},
		{info: model.FileInfo{Extension: ".HTM"}, want: channelartifacts.FormatHTML, wantOK: true},
		{info: model.FileInfo{Name: "a.md"}, want: channelartifacts.FormatMarkdown, wantOK: true},
		{info: model.FileInfo{Name: "a.Markdown"}, want: channelartifacts.FormatMarkdown, wantOK: true},
		{info: model.FileInfo{Name: "a.txt"}},
		{info: model.FileInfo{Name: "md"}},
	}
	for _, tt := range tests {
		got, ok := channelartifacts.FormatOfFile(&tt.info)
		assert.Equal(t, tt.wantOK, ok, "%+v", tt.info)
		assert.Equal(t, tt.want, got, "%+v", tt.info)
	}
}

func TestMigratedRowsAreHTML(t *testing.T) {
	db := testDB(t)
	id, channel := model.NewId(), model.NewId()
	_, err := db.Exec(`INSERT INTO Agents_ChannelArtifacts (ID, ChannelID, Title, HeadVersion, CreatedBy, CreateAt, UpdatedBy, UpdateAt) VALUES ($1, $2, 'Old', 1, 'u', 1, 'u', 1)`, id, channel)
	require.NoError(t, err)
	var format string
	require.NoError(t, db.Get(&format, `SELECT Format FROM Agents_ChannelArtifacts WHERE ID = $1`, id))
	assert.Equal(t, "html", format, "artifacts saved before Markdown existed are HTML")
}
