// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/auth"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
	"github.com/mattermost/mattermost/server/public/model"
)

type artifactToolEnv struct {
	*libraryFixture
	tools    *mmtools.ChannelArtifactTools
	llmCtx   *llm.Context
	audits   []string
	uploads  int
	postedBy []string
	posted   []string
	deleted  []string

	uploadNames []string
	fileNames   map[string]string
}

// newArtifactToolEnv wires the agent's library tools to the fixture. Uploads
// and posts create the same rows Mattermost would, so what the tool saves can
// be read back through the real service.
func newArtifactToolEnv(t *testing.T) *artifactToolEnv {
	t.Helper()
	f := newLibraryFixture(t)
	env := &artifactToolEnv{libraryFixture: f, fileNames: map[string]string{}}
	env.tools = &mmtools.ChannelArtifactTools{
		Library: channelartifacts.NewLibrary(f.svc, func() channelartifacts.Limits {
			return channelartifacts.Limits{MaxSize: 1 << 20, MaxPerChannel: 5, VersionsToKeep: 5}
		}),
		Enabled: f.e.config.EnableChannelArtifacts,
		Audit: func(event, status, _, _, artifactID string) {
			env.audits = append(env.audits, event+":"+status+":"+artifactID)
		},
	}
	env.llmCtx = &llm.Context{
		Channel:        &model.Channel{Id: f.channelID, Type: model.ChannelTypeOpen},
		RequestingUser: &model.User{Id: testUserID},
		BotUserID:      f.botID,
	}

	f.mm.EXPECT().GetConfig().Return(&model.Config{}).Maybe()
	f.mm.EXPECT().UploadFile(mock.Anything, mock.Anything, f.channelID).RunAndReturn(
		func(r io.Reader, name, channelID string) (*model.FileInfo, error) {
			env.uploads++
			env.uploadNames = append(env.uploadNames, name)
			body, err := io.ReadAll(r)
			require.NoError(t, err)
			fileID := model.NewId()
			f.mu.Lock()
			f.content[fileID] = string(body)
			f.mu.Unlock()
			env.fileNames[fileID] = name
			return &model.FileInfo{Id: fileID, Name: name, Extension: strings.TrimPrefix(filepath.Ext(name), "."), ChannelId: channelID, Size: int64(len(body))}, nil
		}).Maybe()
	f.mm.EXPECT().CreatePost(mock.Anything).RunAndReturn(func(post *model.Post) error {
		post.Id = model.NewId()
		env.postedBy = append(env.postedBy, post.UserId)
		env.posted = append(env.posted, post.Id)
		_, err := f.db.Exec(`INSERT INTO Posts (Id, ChannelId) VALUES ($1, $2)`, post.Id, post.ChannelId)
		require.NoError(t, err)
		for _, fileID := range post.FileIds {
			_, err = f.db.Exec(`INSERT INTO FileInfo (Id, PostId) VALUES ($1, $2)`, fileID, post.Id)
			require.NoError(t, err)
			f.mm.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
				Id: fileID, PostId: post.Id, Name: env.fileNames[fileID], Extension: strings.TrimPrefix(filepath.Ext(env.fileNames[fileID]), "."), Size: int64(len(f.content[fileID])),
			}, nil).Maybe()
		}
		f.mm.EXPECT().GetPost(post.Id).Return(&model.Post{
			Id: post.Id, UserId: post.UserId, ChannelId: post.ChannelId, FileIds: post.FileIds,
		}, nil).Maybe()
		return nil
	}).Maybe()
	f.mm.EXPECT().DeletePost(mock.Anything).RunAndReturn(func(postID string) error {
		env.deleted = append(env.deleted, postID)
		return nil
	}).Maybe()
	return env
}

func (e *artifactToolEnv) call(t *testing.T, toolName string, args any) (string, error) {
	t.Helper()
	return e.callIn(auth.WithSessionID(t.Context(), artifactSession), t, toolName, args)
}

// callIn runs the tool with the given context, which decides whether the
// requester's session is available to it.
func (e *artifactToolEnv) callIn(ctx context.Context, t *testing.T, toolName string, args any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	for _, tool := range e.tools.Tools(e.llmCtx) {
		if tool.Name == toolName {
			return tool.Resolver(ctx, e.llmCtx, func(v any) error { return json.Unmarshal(raw, v) })
		}
	}
	require.Failf(t, "tool not offered", "%s", toolName)
	return "", nil
}

func TestAgentSavesAnArtifactAsABotPost(t *testing.T) {
	e := newArtifactToolEnv(t)

	out, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Pricing", Content: "<html>v1</html>"})
	require.NoError(t, err)
	var result mmtools.ChannelArtifactResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, 1, result.Version)
	assert.Equal(t, []string{e.botID}, e.postedBy, "the post the library references is authored by the agent")

	entry, err := e.svc.GetReadable(testUserID, result.ArtifactID)
	require.NoError(t, err)
	assert.Equal(t, result.PostID, entry.SourcePostID)
	assert.Equal(t, e.botID, entry.SourceBotUserID)

	out, err = e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{ArtifactID: result.ArtifactID, Content: "<html>v2</html>"})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, 2, result.Version)

	assert.Equal(t, []string{
		"saveChannelArtifactByAgent:success:" + entry.ID,
		"saveChannelArtifactByAgent:success:" + entry.ID,
	}, e.audits)
}

func TestAgentSavePublishesTheMatchingEvent(t *testing.T) {
	e := newArtifactToolEnv(t)
	out, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Pricing", Content: "<html>v1</html>"})
	require.NoError(t, err)
	var result mmtools.ChannelArtifactResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	_, err = e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{ArtifactID: result.ArtifactID, Content: "<html>v2</html>"})
	require.NoError(t, err)
	_, err = e.call(t, mmtools.RemoveChannelArtifactToolName, mmtools.RemoveChannelArtifactArgs{ArtifactID: result.ArtifactID})
	require.NoError(t, err)

	e.mu.Lock()
	defer e.mu.Unlock()
	assert.Equal(t, []string{channelartifacts.EventCreated, channelartifacts.EventUpdated, channelartifacts.EventDeleted}, e.events)
}

func TestAgentSaveReadsTheFileUnderTheRequestersFilePolicy(t *testing.T) {
	t.Run("a file needs the session to check its policy", func(t *testing.T) {
		e := newArtifactToolEnv(t)
		_, filePost := e.addSource(t, e.botID, "<html>mine</html>")
		out, err := e.callIn(t.Context(), t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "x", FileID: filePost})
		require.Error(t, err)
		assert.Contains(t, out, "session")
		list, listErr := e.svc.List(testUserID, e.channelID)
		require.NoError(t, listErr)
		assert.Empty(t, list.Entries)
	})

	t.Run("a file the requester may not download is not registered", func(t *testing.T) {
		e := newArtifactToolEnv(t)
		_, fileID := e.addSource(t, e.botID, "<html>restricted</html>")
		e.setFilePolicy(false)
		out, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Restricted", FileID: fileID})
		require.Error(t, err)
		assert.Contains(t, out, "download")
		list, listErr := e.svc.List(testUserID, e.channelID)
		require.NoError(t, listErr)
		assert.Empty(t, list.Entries)
	})

	t.Run("content the requester cannot download is not saved either", func(t *testing.T) {
		e := newArtifactToolEnv(t)
		e.setFilePolicy(false)
		_, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Denied", Content: "<html>x</html>"})
		require.Error(t, err)
		list, listErr := e.svc.List(testUserID, e.channelID)
		require.NoError(t, listErr)
		assert.Empty(t, list.Entries)
	})
}

func TestAgentSavesWithoutASession(t *testing.T) {
	e := newArtifactToolEnv(t)

	out, err := e.callIn(t.Context(), t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Wake note", Content: "<html>unattended</html>"})
	require.NoError(t, err, "agent-authored content needs no file policy, so unattended turns can save it")
	var result mmtools.ChannelArtifactResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, []string{e.botID}, e.postedBy)

	entry, err := e.svc.GetReadable(testUserID, result.ArtifactID)
	require.NoError(t, err)
	assert.Equal(t, e.botID, entry.SourceBotUserID)
	assert.Equal(t, channelartifacts.FormatHTML, entry.Format)
}

func TestAgentSavesMarkdown(t *testing.T) {
	e := newArtifactToolEnv(t)

	out, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Charter", Format: "Markdown", Content: "# Goals\n\n- ship"})
	require.NoError(t, err)
	var result mmtools.ChannelArtifactResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "markdown", result.Format)
	assert.Equal(t, []string{"Charter.md"}, e.uploadNames)

	entry, err := e.svc.GetReadable(testUserID, result.ArtifactID)
	require.NoError(t, err)
	assert.Equal(t, channelartifacts.FormatMarkdown, entry.Format)

	out, err = e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{ArtifactID: entry.ID, Format: "html", Content: "<html></html>"})
	require.Error(t, err, "an artifact keeps its format")
	assert.Contains(t, out, "format")

	_, err = e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Bad", Format: "pdf", Content: "x"})
	require.Error(t, err)
}

func TestAgentSaveAutoExecutesOnlyWhereTheHookAllows(t *testing.T) {
	e := newArtifactToolEnv(t)
	autoRun := func(tools []llm.Tool) map[string]bool {
		out := map[string]bool{}
		for _, tool := range tools {
			out[tool.Name] = tool.AutoExecute
		}
		return out
	}

	assert.Equal(t, map[string]bool{mmtools.SaveChannelArtifactToolName: false, mmtools.RemoveChannelArtifactToolName: false},
		autoRun(e.tools.Tools(e.llmCtx)), "without a hook every call asks")

	e.tools.AutoExecuteFor = func(llmCtx *llm.Context, toolName string) bool {
		return llmCtx.Channel.Id == e.channelID && toolName == mmtools.SaveChannelArtifactToolName
	}
	assert.Equal(t, map[string]bool{mmtools.SaveChannelArtifactToolName: true, mmtools.RemoveChannelArtifactToolName: false}, autoRun(e.tools.Tools(e.llmCtx)))

	elsewhere := *e.llmCtx
	elsewhere.Channel = &model.Channel{Id: model.NewId(), Type: model.ChannelTypeOpen}
	assert.Equal(t, map[string]bool{mmtools.SaveChannelArtifactToolName: false, mmtools.RemoveChannelArtifactToolName: false}, autoRun(e.tools.Tools(&elsewhere)))

	scopedOffered := func(tools []llm.Tool) map[string]bool {
		out := map[string]bool{}
		for _, tool := range llm.FilterScopedOptInTools(tools, nil) {
			out[tool.Name] = !tool.AlwaysAsk
		}
		return out
	}
	assert.Equal(t, map[string]bool{mmtools.SaveChannelArtifactToolName: true}, scopedOffered(e.tools.Tools(e.llmCtx)),
		"a scoped turn, which runs everything unasked, offers only the write the hook auto-runs")
	assert.Empty(t, scopedOffered(e.tools.Tools(&elsewhere)), "other scoped agents don't get the writes unless the admin names them")
	named := llm.FilterScopedOptInTools(e.tools.Tools(&elsewhere), []llm.EnabledMCPTool{{ServerOrigin: llm.BuiltinToolsOrigin, ToolName: mmtools.SaveChannelArtifactToolName}})
	require.Len(t, named, 1)
	assert.True(t, named[0].AlwaysAsk, "a named write still asks in a scoped turn")
}

func TestAgentSaveCanActAsTheAgent(t *testing.T) {
	e := newArtifactToolEnv(t)
	noRequester := *e.llmCtx
	noRequester.RequestingUser = nil
	assert.Empty(t, e.tools.Tools(&noRequester))

	e.tools.ActorFor = func(_ context.Context, llmCtx *llm.Context) (channelartifacts.Actor, bool) {
		return channelartifacts.Actor{UserID: llmCtx.BotUserID, BotUserID: llmCtx.BotUserID}, true
	}
	e.e.config.enableChannelArtifacts = true
	e.llmCtx = &noRequester
	out, err := e.callIn(t.Context(), t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Project charter", Format: "markdown", Content: "# Charter"})
	require.NoError(t, err)
	var result mmtools.ChannelArtifactResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	entry, err := e.svc.GetReadable(testUserID, result.ArtifactID)
	require.NoError(t, err)
	assert.Equal(t, e.botID, entry.CreatedBy, "the library records the agent as the writer")
}

func TestAgentSaveRemovesItsPostWhenTheLibraryRefusesTheSave(t *testing.T) {
	e := newArtifactToolEnv(t)
	_, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Pricing", Content: "<html>same</html>"})
	require.NoError(t, err)
	require.Len(t, e.posted, 1)
	assert.Empty(t, e.deleted, "a saved artifact keeps its message")

	out, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Copy", Content: "<html>same</html>"})
	require.Error(t, err)
	assert.Contains(t, out, "identical content")
	require.Len(t, e.posted, 2)
	assert.Equal(t, e.posted[1:], e.deleted, "the message posted for the refused save is removed")

	list, err := e.svc.List(testUserID, e.channelID)
	require.NoError(t, err)
	assert.Len(t, list.Entries, 1)
}

func TestAgentRegistersAFileAlreadyOnItsPost(t *testing.T) {
	e := newArtifactToolEnv(t)
	botPost, botFile := e.addSource(t, e.botID, "<html>mine</html>")
	personPost, personFile := e.addSource(t, testUserID, "<html>theirs</html>")
	_, _ = botPost, personPost

	_, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Mine", FileID: botFile})
	require.NoError(t, err)
	out, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "Theirs", FileID: personFile})
	require.Error(t, err)
	assert.Contains(t, out, "attached to one of your posts")
	assert.Zero(t, e.uploads)
}

func TestAgentWritesAreBoundToTheRequestsPermissionsAndChannel(t *testing.T) {
	t.Run("without the add permission nothing is posted", func(t *testing.T) {
		e := newArtifactToolEnv(t)
		e.denyPermission(model.PermissionAddBookmarkPublicChannel)
		out, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{Title: "x", Content: "<html></html>"})
		require.Error(t, err)
		assert.Contains(t, out, "may not manage")
		assert.Zero(t, e.uploads)
		assert.Empty(t, e.postedBy)
	})

	t.Run("an artifact from another channel is refused before anything is posted", func(t *testing.T) {
		e := newArtifactToolEnv(t)
		other := newLibraryFixtureIn(t, e.libraryFixture)
		entry := other.save(t, "Elsewhere", "<html>other</html>")
		out, err := e.call(t, mmtools.SaveChannelArtifactToolName, mmtools.SaveChannelArtifactArgs{ArtifactID: entry.ID, Content: "<html>v2</html>"})
		require.Error(t, err)
		assert.Contains(t, out, "not found in this channel")
		assert.Zero(t, e.uploads)

		_, err = e.call(t, mmtools.RemoveChannelArtifactToolName, mmtools.RemoveChannelArtifactArgs{ArtifactID: entry.ID})
		require.Error(t, err)
		_, getErr := e.svc.GetReadable(testUserID, entry.ID)
		require.NoError(t, getErr, "the other channel's artifact is untouched")
	})

	t.Run("removing needs the delete permission and keeps the source", func(t *testing.T) {
		e := newArtifactToolEnv(t)
		entry := e.save(t, "Notes", "<html>v1</html>")
		e.denyPermission(model.PermissionDeleteBookmarkPublicChannel)
		_, err := e.call(t, mmtools.RemoveChannelArtifactToolName, mmtools.RemoveChannelArtifactArgs{ArtifactID: entry.ID})
		require.Error(t, err)

		e.mu.Lock()
		delete(e.denied, model.PermissionDeleteBookmarkPublicChannel)
		e.mu.Unlock()
		_, err = e.call(t, mmtools.RemoveChannelArtifactToolName, mmtools.RemoveChannelArtifactArgs{ArtifactID: entry.ID})
		require.NoError(t, err)
		_, getErr := e.svc.GetReadable(testUserID, entry.ID)
		require.ErrorIs(t, getErr, channelartifacts.ErrNotFound)
		var files int
		require.NoError(t, e.db.Get(&files, `SELECT COUNT(*) FROM FileInfo WHERE Id = $1`, entry.SourceFileID))
		assert.Equal(t, 1, files)
	})

	t.Run("arguments are validated", func(t *testing.T) {
		e := newArtifactToolEnv(t)
		for name, args := range map[string]mmtools.SaveChannelArtifactArgs{
			"neither content nor file": {Title: "x"},
			"both content and file":    {Title: "x", Content: "<html></html>", FileID: model.NewId()},
			"new artifact needs title": {Content: "<html></html>"},
		} {
			_, err := e.call(t, mmtools.SaveChannelArtifactToolName, args)
			assert.Error(t, err, name)
		}
		assert.Zero(t, e.uploads)
	})

	t.Run("tools are not offered in direct or group messages", func(t *testing.T) {
		for _, channelType := range []model.ChannelType{model.ChannelTypeDirect, model.ChannelTypeGroup} {
			e := newArtifactToolEnv(t)
			e.llmCtx.Channel.Type = channelType
			assert.Empty(t, e.tools.Tools(e.llmCtx), channelType)
		}
		e := newArtifactToolEnv(t)
		e.llmCtx.Channel.Type = model.ChannelTypePrivate
		assert.Len(t, e.tools.Tools(e.llmCtx), 2)
	})

	t.Run("tools are hidden while the library is off or without a requester", func(t *testing.T) {
		e := newArtifactToolEnv(t)
		assert.Len(t, e.tools.Tools(e.llmCtx), 2)
		e.e.config.enableChannelArtifacts = false
		assert.Empty(t, e.tools.Tools(e.llmCtx))
		e.e.config.enableChannelArtifacts = true
		noUser := *e.llmCtx
		noUser.RequestingUser = nil
		assert.Empty(t, e.tools.Tools(&noUser))
		assert.NotContains(t, strings.Join(toolNames(e.tools.Tools(e.llmCtx)), ","), "Propose")
	})
}

func toolNames(tools []llm.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// newLibraryFixtureIn returns a fixture that shares base's database and mocks
// but works in a different channel.
func newLibraryFixtureIn(t *testing.T, base *libraryFixture) *libraryFixture {
	t.Helper()
	other := *base
	other.channelID = model.NewId()
	return &other
}
