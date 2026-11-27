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

func TestAnchoredPostResolver(t *testing.T) {
	chanA := "chan-a"
	chanB := "chan-b"
	rootID := "root-post"

	t.Run("caches per post ID and enforces channel scope", func(t *testing.T) {
		client := newReconcileFakeClient()
		client.posts["p-live"] = &model.Post{Id: "p-live", ChannelId: chanA, Message: "hello", EditAt: 50}
		client.posts["p-deleted"] = &model.Post{Id: "p-deleted", ChannelId: chanA, Message: "gone", DeleteAt: 100}
		client.posts["p-other-chan"] = &model.Post{Id: "p-other-chan", ChannelId: chanB, Message: "secret"}
		client.postErrors["p-err"] = errors.New("db down")

		resolver := NewAnchoredPostResolver(client, &store.Conversation{ChannelID: &chanA})

		anchor, known, err := resolver.Lookup("p-live")
		require.NoError(t, err)
		assert.True(t, known)
		assert.True(t, anchor.Live)
		assert.Equal(t, "hello", anchor.Message)
		assert.Equal(t, int64(50), anchor.EditAt)

		// Second lookup uses the cache.
		_, _, err = resolver.Lookup("p-live")
		require.NoError(t, err)
		assert.Equal(t, 1, client.getPostCalls["p-live"])

		for _, id := range []string{"p-deleted", "p-other-chan", "p-missing"} {
			a, k, lookupErr := resolver.Lookup(id)
			require.NoError(t, lookupErr)
			assert.True(t, k, id)
			assert.False(t, a.Live, id)
		}

		_, _, err = resolver.Lookup("p-err")
		require.Error(t, err)
	})

	t.Run("threadless conversation scopes anchored posts to its root post channel and caches dead root", func(t *testing.T) {
		client := newReconcileFakeClient()
		client.posts[rootID] = &model.Post{Id: rootID, ChannelId: chanA, Message: "summary"}
		client.posts["reply-ok"] = &model.Post{Id: "reply-ok", ChannelId: chanA, Message: "follow-up"}
		client.posts["reply-other"] = &model.Post{Id: "reply-other", ChannelId: chanB, Message: "leaked"}

		resolver := NewAnchoredPostResolver(client, &store.Conversation{RootPostID: &rootID})

		a, known, err := resolver.Lookup(rootID)
		require.NoError(t, err)
		assert.True(t, known)
		assert.True(t, a.Live)
		assert.Equal(t, 1, client.getPostCalls[rootID], "root lookup must not be issued twice")

		a, known, err = resolver.Lookup("reply-ok")
		require.NoError(t, err)
		assert.True(t, known)
		assert.True(t, a.Live)

		a, known, err = resolver.Lookup("reply-other")
		require.NoError(t, err)
		assert.True(t, known)
		assert.False(t, a.Live)

		// Deleted root marks every lookup dead across repeated calls.
		deadClient := newReconcileFakeClient()
		deadClient.posts[rootID] = &model.Post{Id: rootID, ChannelId: chanA, DeleteAt: 999}
		deadClient.posts["reply-ok"] = &model.Post{Id: "reply-ok", ChannelId: chanA, Message: "still live"}
		deadResolver := NewAnchoredPostResolver(deadClient, &store.Conversation{RootPostID: &rootID})

		for range 2 {
			da, dk, derr := deadResolver.Lookup("reply-ok")
			require.NoError(t, derr)
			assert.True(t, dk)
			assert.False(t, da.Live)
		}
		assert.Equal(t, 0, deadClient.getPostCalls["reply-ok"])
	})
}

func TestBuildRequestsReconcileAnchoredPosts(t *testing.T) {
	const (
		chanID    = "chan-1"
		userID    = "user-1"
		botID     = "bot-1"
		botName   = "agent"
		rootID    = "post-u1"
		botPostID = "post-b1"
		u2PostID  = "post-u2"
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
		setup    func(c *reconcileFakeClient)
		opts     BuildOptions
		wantErr  bool
		validate func(t *testing.T, mode string, req *llm.CompletionRequest)
	}{
		{
			name: "edited user post sends live edited text instead of stored text",
			turns: []store.Turn{
				{ID: "t1", PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "SECRET_ORIGINAL"},
				})},
				{ID: "t2", PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "answer"},
				})},
				{ID: "t3", PostID: new(u2PostID), Role: "user", Sequence: 3, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
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
				assert.Equal(t, "answer", req.Posts[2].Message, mode)
				assert.Equal(t, "follow up", req.Posts[3].Message, mode)
			},
		},
		{
			name: "edited assistant post drops server tool activity while keeping tool_use/tool_result pairing",
			turns: []store.Turn{
				{ID: "t1", PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "question"},
				})},
				{ID: "t2", PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeServerToolUse, ServerTool: &llm.ServerToolUse{ID: "srv1", Tool: "web_search", Status: "completed", Query: "SECRET_QUERY"}},
					{Type: BlockTypeText, Text: "SECRET_ANSWER"},
					{Type: BlockTypeToolUse, ID: "tc1", Name: "lookup", Input: json.RawMessage(`{"id":"1"}`), Status: StatusSuccess, Shared: new(true)},
				})},
				{ID: "t3", Role: "tool_result", Sequence: 3, CreatedAt: 201, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "tool output", Status: StatusSuccess, Shared: new(true)},
				})},
				{ID: "t4", PostID: new(u2PostID), Role: "user", Sequence: 4, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
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
				assert.Equal(t, "tc1", req.Posts[2].ToolUse[0].ID, mode)
				assert.Equal(t, "tool output", req.Posts[2].ToolUse[0].Result, mode)
			},
		},
		{
			name: "edited-to-empty user post without attachments uses placeholder",
			turns: []store.Turn{
				{ID: "t1", PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "SECRET_TEXT"},
				})},
				{ID: "t2", PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "reply"},
				})},
				{ID: "t3", PostID: new(u2PostID), Role: "user", Sequence: 3, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "follow up"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, EditAt: 150, Message: "   "},
				{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, Message: "reply"},
				{Id: u2PostID, UserId: userID, ChannelId: chanID, RootId: rootID, CreateAt: 300, Message: "follow up"},
			},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 4, mode)
				assert.Equal(t, EmptyEditedPostPlaceholder, req.Posts[1].Message, mode)
			},
		},
		{
			name: "edited-to-empty user post whose attachment is unreadable still uses placeholder",
			turns: []store.Turn{
				{ID: "t1", PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "SECRET_TEXT"},
					{Type: BlockTypeFile, FileID: fileID, Filename: "notes.txt", MimeType: "text/plain"},
				})},
				{ID: "t2", PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "reply"},
				})},
				{ID: "t3", PostID: new(u2PostID), Role: "user", Sequence: 3, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
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
			name: "deleted mid-thread assistant post keeps placeholder, preserves role order, and keeps tool rounds paired",
			turns: []store.Turn{
				{ID: "t1", PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "question"},
				})},
				{ID: "t2", PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "SECRET_BOT_REPLY"},
					{Type: BlockTypeToolUse, ID: "tc1", Name: "lookup", Input: json.RawMessage(`{"id":"1"}`), Status: StatusSuccess, Shared: new(true)},
				})},
				{ID: "t3", Role: "tool_result", Sequence: 3, CreatedAt: 201, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeToolResult, ToolUseID: "tc1", Content: "tool output", Status: StatusSuccess, Shared: new(true)},
				})},
				{ID: "t4", PostID: new(u2PostID), Role: "user", Sequence: 4, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "next question"},
				})},
			},
			// botPostID is deleted, so GetPostThread omits it and GetPost reports DeleteAt > 0.
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, Message: "question"},
				{Id: u2PostID, UserId: userID, ChannelId: chanID, RootId: rootID, CreateAt: 300, Message: "next question"},
			},
			setup: func(c *reconcileFakeClient) {
				c.posts[botPostID] = &model.Post{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, DeleteAt: 250, Message: "SECRET_BOT_REPLY"}
			},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 4, mode)
				assert.Equal(t, llm.PostRoleUser, req.Posts[1].Role, mode)
				assert.Equal(t, "question", req.Posts[1].Message, mode)
				assert.Equal(t, llm.PostRoleBot, req.Posts[2].Role, mode)
				assert.Equal(t, DeletedPostPlaceholder, req.Posts[2].Message, mode)
				require.Len(t, req.Posts[2].ToolUse, 1, mode)
				assert.Equal(t, "tool output", req.Posts[2].ToolUse[0].Result, mode)
				assert.Equal(t, llm.PostRoleUser, req.Posts[3].Role, mode)
				assert.Equal(t, "next question", req.Posts[3].Message, mode)
			},
		},
		{
			name: "deleted user post with attachment drops the attachment and uses placeholder",
			turns: []store.Turn{
				{ID: "t1", PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "question"},
				})},
				{ID: "t2", PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "answer"},
				})},
				{ID: "t3", PostID: new(u2PostID), Role: "user", Sequence: 3, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
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
				assert.NotContains(t, req.Posts[3].Message, "secret.txt", mode)
			},
		},
		{
			name: "unchanged auto-reply mention keeps stored text without cache busting",
			turns: []store.Turn{
				{ID: "t1", PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "@agent help me"},
				})},
				{ID: "t2", PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
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
			name: "unattended run ignores post-cutoff edits but still applies deletions",
			turns: []store.Turn{
				{ID: "t1", PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "vetted question"},
				})},
				{ID: "t2", PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "SECRET_DELETED_REPLY"},
				})},
				{ID: "t3", PostID: new(u2PostID), Role: "user", Sequence: 3, CreatedAt: 300, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "latest vetted turn"},
				})},
			},
			posts: []*model.Post{
				{Id: rootID, UserId: userID, ChannelId: chanID, CreateAt: 100, EditAt: 500, UpdateAt: 500, Message: "PROMPT_INJECTION_EDIT"},
				{Id: u2PostID, UserId: userID, ChannelId: chanID, RootId: rootID, CreateAt: 300, Message: "latest vetted turn"},
			},
			setup: func(c *reconcileFakeClient) {
				c.posts[botPostID] = &model.Post{Id: botPostID, UserId: botID, ChannelId: chanID, RootId: rootID, CreateAt: 200, DeleteAt: 600, Message: "SECRET_DELETED_REPLY"}
			},
			opts: BuildOptions{SkipThreadPostsAfterLatestUserTurn: true},
			validate: func(t *testing.T, mode string, req *llm.CompletionRequest) {
				require.Len(t, req.Posts, 4, mode)
				assert.Equal(t, "vetted question", req.Posts[1].Message, mode)
				assert.NotContains(t, req.Posts[1].Message, "PROMPT_INJECTION_EDIT", mode)
				assert.Equal(t, DeletedPostPlaceholder, req.Posts[2].Message, mode)
				assert.NotContains(t, req.Posts[2].Message, "SECRET_DELETED_REPLY", mode)
				assert.Equal(t, "latest vetted turn", req.Posts[3].Message, mode)
			},
		},
		{
			name: "post lookup error fails closed",
			turns: []store.Turn{
				{ID: "t1", PostID: new(rootID), Role: "user", Sequence: 1, CreatedAt: 100, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "question"},
				})},
				{ID: "t2", PostID: new(botPostID), Role: "assistant", Sequence: 2, CreatedAt: 200, Content: mustJSONBlocks(t, []ContentBlock{
					{Type: BlockTypeText, Text: "answer"},
				})},
			},
			// Omit botPostID from threadData so BuildChannelMentionRequest also hits GetPost.
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

			threadData := &mmapi.ThreadData{Posts: tt.posts, UsersByID: map[string]*model.User{}}
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
