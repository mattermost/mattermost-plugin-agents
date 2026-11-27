// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reconcileFakeClient struct {
	mmapi.Client
	posts        map[string]*model.Post
	postErrors   map[string]error
	getPostCalls map[string]int
	fileInfos    map[string]*model.FileInfo
	readableChan map[string]bool
}

func newReconcileFakeClient() *reconcileFakeClient {
	return &reconcileFakeClient{
		posts:        make(map[string]*model.Post),
		postErrors:   make(map[string]error),
		getPostCalls: make(map[string]int),
		fileInfos:    make(map[string]*model.FileInfo),
		readableChan: make(map[string]bool),
	}
}

func (c *reconcileFakeClient) GetPost(postID string) (*model.Post, error) {
	c.getPostCalls[postID]++
	if err, ok := c.postErrors[postID]; ok {
		return nil, err
	}
	if p, ok := c.posts[postID]; ok {
		return p.Clone(), nil
	}
	return nil, pluginapi.ErrNotFound
}

func (c *reconcileFakeClient) GetFileInfo(fileID string) (*model.FileInfo, error) {
	if fi, ok := c.fileInfos[fileID]; ok {
		return fi, nil
	}
	return nil, pluginapi.ErrNotFound
}

func (c *reconcileFakeClient) HasPermissionToFileAction(_, _, _ string) bool {
	return true
}

func (c *reconcileFakeClient) HasPermissionToChannel(_, channelID string, _ *model.Permission) bool {
	return c.readableChan[channelID]
}

func (c *reconcileFakeClient) LogError(_ string, _ ...any) {}

type countingBatchGetter struct {
	inner PostBatchGetter
	calls int
	err   error
}

func (c *countingBatchGetter) GetPostsByIDs(postIDs []string) (map[string]mmapi.LivePost, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.inner.GetPostsByIDs(postIDs)
}

func mustJSONBlocks(t *testing.T, blocks []ContentBlock) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(blocks)
	require.NoError(t, err)
	return b
}

func TestWithoutPostContent(t *testing.T) {
	tests := []struct {
		name      string
		blocks    []ContentBlock
		text      string
		wantTypes []string
		wantText  string
	}{
		{
			name:      "nil input and empty text returns nil",
			blocks:    nil,
			text:      "",
			wantTypes: nil,
			wantText:  "",
		},
		{
			name: "drops attachments, thinking, annotations, server tools, and unknown blocks while keeping tool pairs",
			blocks: []ContentBlock{
				{Type: BlockTypeThinking, Text: "secret reasoning", Signature: "sig"},
				{Type: BlockTypeServerToolUse, ServerTool: &llm.ServerToolUse{ID: "s1", Tool: "web_search", Query: "secret"}},
				{Type: BlockTypeText, Text: "old text", Citations: []Citation{{URL: "https://example.com"}}},
				{Type: BlockTypeFile, FileID: "f1", Filename: "secret.txt"},
				{Type: BlockTypeImage, FileID: "i1", Filename: "secret.png"},
				{Type: BlockTypeAnnotations, WebSearchContext: &WebSearchContext{Count: 1}},
				{Type: "future_block", Text: "future"},
				{Type: BlockTypeToolUse, ID: "tc1", Name: "search", Input: json.RawMessage(`{"q":"x"}`), Shared: new(true)},
				{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "ok", Shared: new(true)},
			},
			text:      DeletedPostPlaceholder,
			wantTypes: []string{BlockTypeText, BlockTypeToolUse, BlockTypeToolResult},
			wantText:  DeletedPostPlaceholder,
		},
		{
			name: "prepends text block when no text block existed and replacement is non-empty",
			blocks: []ContentBlock{
				{Type: BlockTypeFile, FileID: "f1", Filename: "secret.txt"},
			},
			text:      DeletedPostPlaceholder,
			wantTypes: []string{BlockTypeText},
			wantText:  DeletedPostPlaceholder,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithoutPostContent(tt.blocks, tt.text)
			if tt.wantTypes == nil {
				assert.Nil(t, got)
				return
			}
			var gotTypes []string
			for _, b := range got {
				gotTypes = append(gotTypes, b.Type)
				assert.Empty(t, b.Citations)
			}
			assert.Equal(t, tt.wantTypes, gotTypes)
			assert.Equal(t, tt.wantText, TextContent(got))
		})
	}
}

func TestResolveAnchoredPostsWithSQLBatch(t *testing.T) {
	_, s := setupTestService(t)
	_, err := s.DB().Exec(`CREATE TABLE Posts (
		Id VARCHAR(26) PRIMARY KEY,
		ChannelId VARCHAR(26) NOT NULL,
		Message TEXT NOT NULL DEFAULT '',
		EditAt BIGINT NOT NULL DEFAULT 0,
		DeleteAt BIGINT NOT NULL DEFAULT 0
	)`)
	require.NoError(t, err)

	const (
		chanA = "chan-a"
		chanB = "chan-b"
	)
	_, err = s.DB().Exec(`INSERT INTO Posts (Id, ChannelId, Message, EditAt, DeleteAt) VALUES
		('p-live', $1, 'edited hello', 50, 0),
		('p-deleted', $1, 'gone secret', 0, 100),
		('p-other-chan', $2, 'foreign secret', 0, 0),
		('p-root', $1, 'summary root', 0, 0),
		('p-dead-root', $1, 'dead root', 0, 999)
	`, chanA, chanB)
	require.NoError(t, err)

	dbClient := mmapi.NewTestDBClient(s.DB())

	t.Run("fetches all turn PostIDs and RunPostIDs in a single SQL query without calling GetPost", func(t *testing.T) {
		batch := &countingBatchGetter{inner: dbClient}
		fallback := newReconcileFakeClient()
		ch := chanA
		turns := []store.Turn{
			{PostID: new("p-live")},
			{RunPostID: new("p-deleted")},
			{PostID: new("p-other-chan")},
			{PostID: new("p-missing")},
			{PostID: new("p-live")}, // duplicate ID must be deduplicated
		}

		anchored, resolveErr := ResolveAnchoredPosts(&store.Conversation{ChannelID: &ch}, turns, batch, fallback)
		require.NoError(t, resolveErr)
		assert.Equal(t, 1, batch.calls, "must issue exactly one batched SQL query")
		assert.Empty(t, fallback.getPostCalls, "must not call per-post GetPost when batch getter is wired")

		live, known := anchored.Get("p-live")
		assert.True(t, known)
		assert.True(t, live.Live)
		assert.Equal(t, "edited hello", live.Message)
		assert.Equal(t, int64(50), live.EditAt)

		for _, id := range []string{"p-deleted", "p-other-chan", "p-missing"} {
			a, k := anchored.Get(id)
			assert.True(t, k, id)
			assert.False(t, a.Live, id)
		}
	})

	t.Run("threadless conversation scopes anchored posts to its root post channel and fails closed when root is dead", func(t *testing.T) {
		rootID := "p-root"
		turns := []store.Turn{
			{PostID: new("p-live")},
			{PostID: new("p-other-chan")},
		}
		anchored, resolveErr := ResolveAnchoredPosts(&store.Conversation{RootPostID: &rootID}, turns, dbClient, nil)
		require.NoError(t, resolveErr)

		live, known := anchored.Get("p-live")
		assert.True(t, known && live.Live)
		foreign, known := anchored.Get("p-other-chan")
		assert.True(t, known && !foreign.Live)

		deadRoot := "p-dead-root"
		deadAnchored, resolveErr := ResolveAnchoredPosts(&store.Conversation{RootPostID: &deadRoot}, turns, dbClient, nil)
		require.NoError(t, resolveErr)
		a, k := deadAnchored.Get("p-live")
		assert.True(t, k && !a.Live)
	})

	t.Run("batch SQL error fails closed", func(t *testing.T) {
		batch := &countingBatchGetter{err: errors.New("db down")}
		_, resolveErr := ResolveAnchoredPosts(&store.Conversation{}, []store.Turn{{PostID: new("p-live")}}, batch, nil)
		require.Error(t, resolveErr)
	})
}

func TestBuildRequestsReconcileAnchoredPosts(t *testing.T) {
	const (
		chanID    = "chan-1"
		userID    = "user-1"
		peerID    = "peer-1"
		botID     = "bot-1"
		botName   = "agent"
		rootID    = "post-u1"
		botPostID = "post-b1"
		u2PostID  = "post-u2"
		peerPost  = "post-peer"
		fileID    = "file-1"
	)

	makeConv := func(t *testing.T, s *store.Store, turns []store.Turn) *store.Conversation {
		t.Helper()
		ch := chanID
		root := rootID
		conv := &store.Conversation{
			ID:           model.NewId(),
			UserID:       userID,
			BotID:        botID,
			ChannelID:    &ch,
			RootPostID:   &root,
			SystemPrompt: "Be helpful.",
			Operation:    "conversation",
		}
		require.NoError(t, s.CreateConversation(conv))
		for i := range turns {
			turns[i].ID = model.NewId()
			turns[i].ConversationID = conv.ID
			require.NoError(t, s.CreateTurn(&turns[i]))
		}
		return conv
	}

	tests := []struct {
		name     string
		turns    []store.Turn
		posts    []*model.Post
		users    map[string]*model.User
		setup    func(c *reconcileFakeClient)
		opts     BuildOptions
		wantErr  bool
		validate func(t *testing.T, mode string, req *llm.CompletionRequest)
	}{
		{
			name: "edited user post sends live edited text instead of stored text",
			turns: []store.Turn{
				{PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "SECRET_ORIGINAL"},
				})},
				{PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "answer"},
				})},
				{PostID: new(u2PostID), Role: "user", Sequence: 3, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "follow up"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, EditAt: 150, Message: "redacted question"},
				{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, Message: "answer"},
				{Id: u2PostID, UserId: userID, ChannelId: chanID, RootId: rootID, CreateAt: 300, Message: "follow up"},
			},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 4, mode)
				assert.Equal(t, "redacted question", req.Posts[1].Message, mode)
				assert.NotContains(t, req.Posts[1].Message, "SECRET_ORIGINAL", mode)
			},
		},
		{
			name: "edited assistant post drops server tool activity while keeping tool_use/tool_result pairing",
			turns: []store.Turn{
				{PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "question"},
				})},
				{PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeServerToolUse, ServerTool: &llm.ServerToolUse{ID: "srv1", Tool: "web_search", Status: "completed", Query: "SECRET_QUERY"}},
					{Type: BlockTypeText, Text: "SECRET_ANSWER"},
					{Type: BlockTypeToolUse, ID: "tc1", Name: "lookup", Input: json.RawMessage(`{"id":"1"}`), Status: StatusSuccess, Shared: new(true)},
				})},
				{Role: "tool_result", Sequence: 3, CreatedAt: 201, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "tool output", Status: StatusSuccess, Shared: new(true)},
				})},
				{PostID: new(u2PostID), Role: "user", Sequence: 4, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "next"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, Message: "question"},
				{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, EditAt: 250, Message: "edited answer"},
				{Id: u2PostID, UserId: userID, ChannelId: chanID, RootId: rootID, CreateAt: 300, Message: "next"},
			},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 4, mode)
				assert.Equal(t, "edited answer", req.Posts[2].Message, mode)
				assert.Empty(t, req.Posts[2].ServerTools, mode)
				require.Len(t, req.Posts[2].ToolUse, 1, mode)
				assert.Equal(t, "tool output", req.Posts[2].ToolUse[0].Result, mode)
			},
		},
		{
			name: "edited-to-empty user post without readable attachments uses placeholder",
			turns: []store.Turn{
				{PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "SECRET_TEXT"},
					{Type: BlockTypeFile, FileID: fileID, Filename: "notes.txt", MimeType: "text/plain"},
				})},
				{PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "reply"},
				})},
				{PostID: new(u2PostID), Role: "user", Sequence: 3, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "follow up"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, EditAt: 150, Message: ""},
				{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, Message: "reply"},
				{Id: u2PostID, UserId: userID, ChannelId: chanID, RootId: rootID, CreateAt: 300, Message: "follow up"},
			},
			setup: func(c *reconcileFakeClient) {
				c.fileInfos[fileID] = &model.FileInfo{Id: fileID, ChannelId: "revoked-chan", Name: "notes.txt", MimeType: "text/plain", Size: 10}
				c.readableChan["revoked-chan"] = false
			},
			opts: BuildOptions{SessionID: "sess"},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 4, mode)
				assert.Equal(t, EmptyEditedPostPlaceholder, req.Posts[1].Message, mode)
			},
		},
		{
			name: "deleted mid-thread assistant post strips RunPostID tool-round preamble while keeping tool calls paired",
			turns: []store.Turn{
				{PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "question"},
				})},
				{RunPostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 190, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "PREAMBLE_SECRET"},
					{Type: BlockTypeToolUse, ID: "tc1", Name: "lookup", Input: json.RawMessage(`{"id":"1"}`), Status: StatusSuccess, Shared: new(true)},
				})},
				{RunPostID: new(botPostID), Role: "tool_result", Sequence: 3, CreatedAt: 195, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "tool output", Status: StatusSuccess, Shared: new(true)},
				})},
				{PostID: new(botPostID), Role: "assistant", Sequence: 4, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "SECRET_BOT_REPLY"},
				})},
				{PostID: new(u2PostID), Role: "user", Sequence: 5, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "next question"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, Message: "question"},
				{Id: u2PostID, UserId: userID, ChannelId: chanID, RootId: rootID, CreateAt: 300, Message: "next question"},
			},
			setup: func(c *reconcileFakeClient) {
				c.posts[botPostID] = &model.Post{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, DeleteAt: 250, Message: "SECRET_BOT_REPLY"}
			},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 5, mode)
				assert.Empty(t, req.Posts[2].Message, mode)
				assert.NotContains(t, req.Posts[2].Message, "PREAMBLE_SECRET", mode)
				require.Len(t, req.Posts[2].ToolUse, 1, mode)
				assert.Equal(t, "tool output", req.Posts[2].ToolUse[0].Result, mode)
				assert.Equal(t, DeletedPostPlaceholder, req.Posts[3].Message, mode)
				assert.Equal(t, "next question", req.Posts[4].Message, mode)
			},
		},
		{
			name: "deleted latest assistant post does not truncate trailing non-turn channel thread posts",
			turns: []store.Turn{
				{PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "question"},
				})},
				{PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "deleted reply"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, Message: "question"},
				{Id: peerPost, UserId: peerID, ChannelId: chanID, RootId: rootID, CreateAt: 300, Message: "peer follow up"},
			},
			users: map[string]*model.User{peerID: {Id: peerID, Username: "alice"}},
			setup: func(c *reconcileFakeClient) {
				c.posts[botPostID] = &model.Post{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, DeleteAt: 250, Message: "deleted reply"}
			},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				if mode == "BuildChannelMentionRequest" {
					require.Len(t, req.Posts, 4, mode)
					assert.Equal(t, DeletedPostPlaceholder, req.Posts[2].Message, mode)
					assert.Contains(t, req.Posts[3].Message, "peer follow up", mode)
				} else {
					require.Len(t, req.Posts, 3, mode)
					assert.Equal(t, DeletedPostPlaceholder, req.Posts[2].Message, mode)
				}
			},
		},
		{
			name: "deleted user post with attachment drops the attachment and uses placeholder",
			turns: []store.Turn{
				{PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "question"},
				})},
				{PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "answer"},
				})},
				{PostID: new(u2PostID), Role: "user", Sequence: 3, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "SECRET_REPLY"},
					{Type: BlockTypeFile, FileID: fileID, Filename: "secret.txt", MimeType: "text/plain"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, Message: "question"},
				{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, Message: "answer"},
			},
			setup: func(c *reconcileFakeClient) {
				c.posts[u2PostID] = &model.Post{Id: u2PostID, UserId: userID, ChannelId: chanID, RootId: rootID, CreateAt: 300, DeleteAt: 350, Message: "SECRET_REPLY"}
				c.fileInfos[fileID] = &model.FileInfo{Id: fileID, ChannelId: chanID, Name: "secret.txt", MimeType: "text/plain", Content: "SECRET_FILE"}
				c.readableChan[chanID] = true
			},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 4, mode)
				assert.Equal(t, DeletedPostPlaceholder, req.Posts[3].Message, mode)
				assert.NotContains(t, req.Posts[3].Message, "SECRET_FILE", mode)
			},
		},
		{
			name: "unchanged auto-reply mention keeps stored text without cache busting",
			turns: []store.Turn{
				{PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "@agent help me"},
				})},
				{PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "sure"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, Message: "  help me\n"},
				{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, Message: "sure"},
			},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 3, mode)
				assert.Equal(t, "@agent help me", req.Posts[1].Message, mode)
			},
		},
		{
			name: "live unedited empty assistant post keeps stored answer (failed UpdatePost or settle race)",
			turns: []store.Turn{
				{PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "write long code"},
				})},
				{PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "long generated answer"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, Message: "write long code"},
				{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, EditAt: 0, Message: ""},
			},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 3, mode)
				assert.Equal(t, "long generated answer", req.Posts[2].Message, mode)
			},
		},
		{
			name: "unattended run still applies owner edits removing secrets while skipping unvetted peer posts",
			turns: []store.Turn{
				{PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "deploy with SECRET123"},
				})},
				{PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "waiting on job"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, EditAt: 500, UpdateAt: 500, Message: "deploy safely"},
				{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, Message: "waiting on job"},
				{Id: peerPost, UserId: peerID, ChannelId: chanID, RootId: rootID, CreateAt: 600, UpdateAt: 600, Message: "UNVETTED_PEER_INJECTION"},
			},
			users: map[string]*model.User{peerID: {Id: peerID, Username: "alice"}},
			opts:  BuildOptions{SkipThreadPostsAfterLatestUserTurn: true},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 3, mode)
				assert.Equal(t, "deploy safely", req.Posts[1].Message, mode)
				assert.NotContains(t, req.Posts[1].Message, "SECRET123", mode)
				for _, p := range req.Posts {
					assert.NotContains(t, p.Message, "UNVETTED_PEER_INJECTION", mode)
				}
			},
		},
		{
			name: "post lookup error fails closed",
			turns: []store.Turn{
				{PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "question"},
				})},
				{PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "answer"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, Message: "question"},
			},
			setup: func(c *reconcileFakeClient) {
				c.postErrors[botPostID] = errors.New("db unavailable")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, s := setupTestService(t)
			conv := makeConv(t, s, tt.turns)

			client := newReconcileFakeClient()
			for _, p := range tt.posts {
				client.posts[p.Id] = p
			}
			if tt.setup != nil {
				tt.setup(client)
			}

			svc := NewService(s, nil, client, &testBotLookup{})
			llmCtx := &llm.Context{BotUsername: botName}

			completionReq, err := svc.BuildCompletionRequest(conv, llmCtx, tt.opts)
			if tt.wantErr {
				require.Error(t, err, "BuildCompletionRequest")
			} else {
				require.NoError(t, err, "BuildCompletionRequest")
				tt.validate(t, "BuildCompletionRequest", completionReq)
			}

			users := tt.users
			if users == nil {
				users = map[string]*model.User{}
			}
			threadData := &mmapi.ThreadData{Posts: tt.posts, UsersByID: users}
			mentionReq, err := svc.BuildChannelMentionRequest(conv, llmCtx, threadData, tt.opts)
			if tt.wantErr {
				require.Error(t, err, "BuildChannelMentionRequest")
			} else {
				require.NoError(t, err, "BuildChannelMentionRequest")
				tt.validate(t, "BuildChannelMentionRequest", mentionReq)
			}
		})
	}
}

func TestBuildRegenerateRequestAnchoredPosts(t *testing.T) {
	const (
		chanID = "chan-1"
		userID = "user-1"
		botID  = "bot-1"
		u1ID   = "u1"
		b1ID   = "b1"
		u2ID   = "u2"
		b2ID   = "b2"
	)

	seed := func(t *testing.T) (*store.Store, *store.Conversation) {
		t.Helper()
		_, s := setupTestService(t)
		ch := chanID
		root := u1ID
		conv := &store.Conversation{
			ID:           model.NewId(),
			UserID:       userID,
			BotID:        botID,
			ChannelID:    &ch,
			RootPostID:   &root,
			SystemPrompt: "Be helpful.",
			Operation:    "conversation",
		}
		require.NoError(t, s.CreateConversation(conv))
		turns := []store.Turn{
			{ID: model.NewId(), ConversationID: conv.ID, PostID: new(u1ID), Role: "user", Sequence: 1, Content: mustJSONBlocks(t, []ContentBlock{{Type: BlockTypeText, Text: "first question"}})},
			{ID: model.NewId(), ConversationID: conv.ID, PostID: new(b1ID), Role: "assistant", Sequence: 2, Content: mustJSONBlocks(t, []ContentBlock{{Type: BlockTypeText, Text: "first answer"}})},
			{ID: model.NewId(), ConversationID: conv.ID, PostID: new(u2ID), Role: "user", Sequence: 3, Content: mustJSONBlocks(t, []ContentBlock{{Type: BlockTypeText, Text: "second question"}})},
			{ID: model.NewId(), ConversationID: conv.ID, PostID: new(b2ID), Role: "assistant", Sequence: 4, Content: mustJSONBlocks(t, []ContentBlock{{Type: BlockTypeText, Text: "second answer"}})},
		}
		for i := range turns {
			require.NoError(t, s.CreateTurn(&turns[i]))
		}
		return s, conv
	}

	t.Run("regenerating against an edited initiating post sends the edited text", func(t *testing.T) {
		s, conv := seed(t)
		client := newReconcileFakeClient()
		client.posts[u1ID] = &model.Post{Id: u1ID, ChannelId: chanID, Message: "first question"}
		client.posts[b1ID] = &model.Post{Id: b1ID, ChannelId: chanID, Message: "first answer"}
		client.posts[u2ID] = &model.Post{Id: u2ID, ChannelId: chanID, Message: "edited second question", EditAt: 100}

		svc := NewService(s, nil, client, &testBotLookup{})
		req, err := svc.BuildRegenerateRequest(conv, &llm.Context{}, b2ID, BuildOptions{})
		require.NoError(t, err)
		require.Len(t, req.Posts, 4)
		assert.Equal(t, "edited second question", req.Posts[3].Message)
	})

	t.Run("regenerating when the initiating user post was deleted fails without scrubbing prior turns", func(t *testing.T) {
		s, conv := seed(t)
		client := newReconcileFakeClient()
		client.posts[u1ID] = &model.Post{Id: u1ID, ChannelId: chanID, Message: "first question"}
		client.posts[b1ID] = &model.Post{Id: b1ID, ChannelId: chanID, Message: "first answer"}
		client.posts[u2ID] = &model.Post{Id: u2ID, ChannelId: chanID, Message: "second question", DeleteAt: 500}

		svc := NewService(s, nil, client, &testBotLookup{})
		_, err := svc.BuildRegenerateRequest(conv, &llm.Context{}, b2ID, BuildOptions{})
		require.ErrorIs(t, err, ErrInitiatingPostDeleted)

		turns, getErr := s.GetTurnsForConversation(conv.ID)
		require.NoError(t, getErr)
		assert.Len(t, turns, 4, "prior response turns must not be scrubbed when regeneration is rejected")
	})

	t.Run("regenerating when an earlier non-initiating post was deleted replaces that earlier turn with placeholder", func(t *testing.T) {
		s, conv := seed(t)
		client := newReconcileFakeClient()
		client.posts[u1ID] = &model.Post{Id: u1ID, ChannelId: chanID, Message: "first question"}
		client.posts[b1ID] = &model.Post{Id: b1ID, ChannelId: chanID, Message: "first answer", DeleteAt: 250}
		client.posts[u2ID] = &model.Post{Id: u2ID, ChannelId: chanID, Message: "second question"}

		svc := NewService(s, nil, client, &testBotLookup{})
		req, err := svc.BuildRegenerateRequest(conv, &llm.Context{}, b2ID, BuildOptions{})
		require.NoError(t, err)
		require.Len(t, req.Posts, 4)
		assert.Equal(t, "first question", req.Posts[1].Message)
		assert.Equal(t, DeletedPostPlaceholder, req.Posts[2].Message)
		assert.Equal(t, "second question", req.Posts[3].Message)
	})
}
