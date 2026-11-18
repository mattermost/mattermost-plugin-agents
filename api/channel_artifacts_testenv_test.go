// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

var (
	artifactDBOnce sync.Once
	artifactDBConn string
	artifactDBErr  error
)

// artifactTestDB returns a database in its own schema with the plugin's real
// migrations applied and stubs of the Mattermost tables the library reads. The
// Postgres container starts on first use, so tests that never touch the
// library do not pay for it; testcontainers' reaper removes it at exit.
func artifactTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	artifactDBOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("testdb"), tcpostgres.WithUsername("testuser"), tcpostgres.WithPassword("testpass"),
			tcpostgres.BasicWaitStrategies())
		if err != nil {
			artifactDBErr = err
			return
		}
		artifactDBConn, artifactDBErr = container.ConnectionString(ctx, "sslmode=disable")
	})
	require.NoError(t, artifactDBErr)

	setup, err := sqlx.Connect("postgres", artifactDBConn)
	require.NoError(t, err)
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	_, err = setup.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	setup.Close()

	db, err := sqlx.Connect("postgres", artifactDBConn+"&search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE")
		db.Close()
	})
	for _, ddl := range []string{
		`CREATE TABLE Posts (Id VARCHAR(26) PRIMARY KEY, ChannelId VARCHAR(26), DeleteAt BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE FileInfo (Id VARCHAR(26) PRIMARY KEY, PostId VARCHAR(26), DeleteAt BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE Channels (Id VARCHAR(26) PRIMARY KEY)`,
	} {
		_, err = db.Exec(ddl)
		require.NoError(t, err)
	}
	require.NoError(t, store.New(db).RunMigrations())
	return db
}

type artifactBots map[string]string

func (b artifactBots) IsAnyBot(userID string) bool           { _, ok := b[userID]; return ok }
func (b artifactBots) AgentDisplayName(userID string) string { return b[userID] }

const artifactSession = "abcdefghijklmnopqrstuvwxyz"

// libraryFixture is an enabled channel library backed by a real database, with
// Mattermost's channel, post and file lookups served from a mock.
type libraryFixture struct {
	e         *TestEnvironment
	db        *sqlx.DB
	mm        *mocks.MockClient
	svc       *channelartifacts.Service
	channelID string
	botID     string

	mu      *sync.Mutex
	denied  map[*model.Permission]bool
	policy  bool
	content map[string]string
}

func newLibraryFixture(t *testing.T) *libraryFixture {
	t.Helper()
	e := SetupTestEnvironment(t)
	t.Cleanup(func() { e.Cleanup(t) })
	e.config.enableHTMLArtifacts = true
	e.config.enableChannelArtifacts = true

	f := &libraryFixture{
		e: e, db: artifactTestDB(t), mm: mocks.NewMockClient(t),
		channelID: model.NewId(), botID: model.NewId(),
		mu: &sync.Mutex{}, denied: map[*model.Permission]bool{}, policy: true, content: map[string]string{},
	}
	f.svc = channelartifacts.NewService(channelartifacts.NewStore(f.db), f.mm, artifactBots{f.botID: "Helper"})
	e.api.SetChannelArtifacts(f.svc)
	e.api.mmClient = f.mm

	f.mm.EXPECT().GetChannel(mock.Anything).RunAndReturn(func(id string) (*model.Channel, error) {
		return &model.Channel{Id: id, Type: model.ChannelTypeOpen}, nil
	}).Maybe()
	f.mm.EXPECT().HasPermissionToChannel(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_, _ string, p *model.Permission) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return !f.denied[p]
		}).Maybe()
	f.mm.EXPECT().HasPermissionToFileAction(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(session, _, _ string) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.policy && session != ""
		}).Maybe()
	f.mm.EXPECT().GetConfig().Return(&model.Config{}).Maybe()
	f.mm.EXPECT().PublishWebSocketEvent(mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	f.mm.EXPECT().GetFile(mock.Anything).RunAndReturn(func(id string) (io.ReadCloser, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		body, ok := f.content[id]
		if !ok {
			return nil, fmt.Errorf("no file %s", id)
		}
		return io.NopCloser(strings.NewReader(body)), nil
	}).Maybe()
	return f
}

// addSource creates an HTML file attached to a post by authorID in the
// fixture's channel and returns the post and file IDs.
func (f *libraryFixture) addSource(t *testing.T, authorID, html string) (postID, fileID string) {
	t.Helper()
	postID, fileID = model.NewId(), model.NewId()
	_, err := f.db.Exec(`INSERT INTO Posts (Id, ChannelId) VALUES ($1, $2)`, postID, f.channelID)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO FileInfo (Id, PostId) VALUES ($1, $2)`, fileID, postID)
	require.NoError(t, err)
	f.mu.Lock()
	f.content[fileID] = html
	f.mu.Unlock()
	f.mm.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
		Id: fileID, PostId: postID, Name: "dashboard.html", Extension: "html", Size: int64(len(html)),
	}, nil).Maybe()
	f.mm.EXPECT().GetPost(postID).Return(&model.Post{
		Id: postID, UserId: authorID, ChannelId: f.channelID, FileIds: model.StringArray{fileID},
	}, nil).Maybe()
	return postID, fileID
}

// save registers a new agent-posted file through the service.
func (f *libraryFixture) save(t *testing.T, title, html string) *channelartifacts.Entry {
	t.Helper()
	postID, fileID := f.addSource(t, f.botID, html)
	entry, err := f.svc.Save(testUserID, f.mm, channelartifacts.SaveRequest{
		ChannelID: f.channelID, PostID: postID, FileID: fileID, Title: title,
	}, channelartifacts.Limits{MaxSize: 1 << 20})
	require.NoError(t, err)
	return entry
}

func (f *libraryFixture) denyPermission(p *model.Permission) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.denied[p] = true
}

func (f *libraryFixture) setFilePolicy(allowed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policy = allowed
}

func (f *libraryFixture) breakSource(t *testing.T, fileID string) {
	t.Helper()
	_, err := f.db.Exec(`UPDATE FileInfo SET DeleteAt = 1 WHERE Id = $1`, fileID)
	require.NoError(t, err)
}

// do sends a request to the plugin with the test user and an authenticated session.
func (f *libraryFixture) do(method, path, body string, headers ...string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Mattermost-User-Id", testUserID)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	f.e.api.ServeHTTP(&plugin.Context{SessionId: artifactSession}, rec, req)
	return rec
}

func (f *libraryFixture) createBody(postID, fileID, extra string) string {
	return fmt.Sprintf(`{"channel_id":%q,"post_id":%q,"file_id":%q%s}`, f.channelID, postID, fileID, extra)
}
