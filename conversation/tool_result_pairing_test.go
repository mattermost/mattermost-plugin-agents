// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type seededTurn struct {
	role   string
	postID string
	blocks []ContentBlock
}

func toolUseTurn(postID string, status string, ids ...string) seededTurn {
	blocks := make([]ContentBlock, 0, len(ids))
	for _, id := range ids {
		blocks = append(blocks, ContentBlock{
			Type:   BlockTypeToolUse,
			ID:     id,
			Name:   "lookup",
			Input:  json.RawMessage(`{"key":"` + id + `"}`),
			Status: status,
			Shared: new(true),
		})
	}
	return seededTurn{role: "assistant", postID: postID, blocks: blocks}
}

func toolResultTurn(ids ...string) seededTurn {
	blocks := make([]ContentBlock, 0, len(ids))
	for _, id := range ids {
		blocks = append(blocks, ContentBlock{
			Type:      BlockTypeToolResult,
			ToolUseID: id,
			Content:   "result " + id,
			Status:    StatusSuccess,
			Shared:    new(true),
		})
	}
	return seededTurn{role: "tool_result", blocks: blocks}
}

func userTurn(postID, text string) seededTurn {
	return seededTurn{role: "user", postID: postID, blocks: []ContentBlock{{Type: BlockTypeText, Text: text}}}
}

func assistantTextTurn(postID, text string) seededTurn {
	var blocks []ContentBlock
	if text != "" {
		blocks = []ContentBlock{{Type: BlockTypeText, Text: text}}
	}
	return seededTurn{role: "assistant", postID: postID, blocks: blocks}
}

// TestBuildRequestsPairToolResultsWithTheirToolUse covers stored layouts in
// which a tool_result turn is not adjacent to the assistant turn holding its
// tool_use — what channel approvals produce when an older post is approved
// after a newer bot post exists. Both request builders must replay every
// tool_use with its own result, and nothing may be replayed with blank content.
func TestBuildRequestsPairToolResultsWithTheirToolUse(t *testing.T) {
	tests := []struct {
		name        string
		turns       []seededTurn
		threadPosts []string
		// wantResults maps each tool_use ID to the result it must replay with.
		wantResults map[string]string
	}{
		{
			name: "older post approved after the newer post",
			turns: []seededTurn{
				userTurn("u1", "first question"),
				toolUseTurn("a", StatusSuccess, "a1", "a2"),
				userTurn("u2", "second question"),
				toolUseTurn("b", StatusSuccess, "b1", "b2"),
				toolResultTurn("b1", "b2"),
				toolResultTurn("a1", "a2"),
			},
			threadPosts: []string{"u1", "a", "u2", "b"},
			wantResults: map[string]string{"a1": "result a1", "a2": "result a2", "b1": "result b1", "b2": "result b2"},
		},
		{
			name: "thread poisoned by failed follow-ups, then a new message",
			// Each failed follow-up demoted the approval anchor and finalized
			// an empty anchor on the same post.
			turns: []seededTurn{
				userTurn("u1", "first question"),
				toolUseTurn("", StatusSuccess, "a1", "a2"),
				userTurn("u2", "second question"),
				toolUseTurn("", StatusSuccess, "b1", "b2"),
				toolResultTurn("b1", "b2"),
				assistantTextTurn("b", ""),
				toolResultTurn("a1", "a2"),
				assistantTextTurn("a", ""),
				userTurn("u3", "are you there?"),
			},
			threadPosts: []string{"u1", "a", "u2", "b", "u3"},
			wantResults: map[string]string{"a1": "result a1", "a2": "result a2", "b1": "result b1", "b2": "result b2"},
		},
		{
			name: "older post still pending when a new message arrives",
			turns: []seededTurn{
				userTurn("u1", "first question"),
				toolUseTurn("a", StatusPending, "a1"),
				userTurn("u2", "second question"),
				toolUseTurn("b", StatusSuccess, "b1"),
				toolResultTurn("b1"),
				userTurn("u3", "and now?"),
			},
			threadPosts: []string{"u1", "a", "u2", "b", "u3"},
			wantResults: map[string]string{"a1": NotExecutedToolResult, "b1": "result b1"},
		},
		{
			name: "tool result whose tool_use no longer exists",
			turns: []seededTurn{
				userTurn("u1", "first question"),
				toolResultTurn("gone"),
				assistantTextTurn("a", "answer"),
				userTurn("u2", "second question"),
			},
			threadPosts: []string{"u1", "a", "u2"},
			wantResults: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, s := setupTestService(t)
			botID := model.NewId()
			userID := model.NewId()
			rootPostID := "root"

			conv := &store.Conversation{
				ID:           model.NewId(),
				UserID:       userID,
				BotID:        botID,
				RootPostID:   &rootPostID,
				SystemPrompt: "system",
				Operation:    "conversation",
			}
			require.NoError(t, s.CreateConversation(conv))

			postText := map[string]string{}
			for _, seeded := range tt.turns {
				content, err := json.Marshal(seeded.blocks)
				require.NoError(t, err)
				turn := &store.Turn{
					ID:             model.NewId(),
					ConversationID: conv.ID,
					Role:           seeded.role,
					Content:        content,
				}
				if seeded.postID != "" {
					turn.PostID = new(seeded.postID)
					if seeded.role == "user" {
						postText[seeded.postID] = seeded.blocks[0].Text
					}
				}
				require.NoError(t, s.CreateTurnAutoSequence(turn))
			}

			threadData := &mmapi.ThreadData{UsersByID: map[string]*model.User{
				userID: {Id: userID, Username: "alice"},
				botID:  {Id: botID, Username: "bot"},
			}}
			for i, postID := range tt.threadPosts {
				authorID := botID
				if _, isUserPost := postText[postID]; isUserPost {
					authorID = userID
				}
				threadData.Posts = append(threadData.Posts, &model.Post{
					Id: postID, UserId: authorID, CreateAt: int64(1000 * (i + 1)), Message: postText[postID],
				})
			}

			completionReq, err := svc.BuildCompletionRequest(conv, &llm.Context{})
			require.NoError(t, err)
			mentionReq, err := svc.BuildChannelMentionRequest(conv, &llm.Context{}, threadData)
			require.NoError(t, err)

			for name, req := range map[string]*llm.CompletionRequest{
				"BuildCompletionRequest":     completionReq,
				"BuildChannelMentionRequest": mentionReq,
			} {
				gotResults := map[string]string{}
				for i, post := range req.Posts {
					if post.Role != llm.PostRoleBot {
						assert.Empty(t, post.ToolUse, "%s: posts[%d] (role %v) must not carry tool calls", name, i, post.Role)
					}
					for _, tc := range post.ToolUse {
						assert.NotEmpty(t, tc.Name, "%s: posts[%d] replays a tool result with no tool_use (id %s)", name, i, tc.ID)
						assert.NotEmpty(t, strings.TrimSpace(tc.Result), "%s: posts[%d] replays tool_use %s with a blank result", name, i, tc.ID)
						assert.NotContains(t, gotResults, tc.ID, "%s: tool_use %s replayed twice", name, tc.ID)
						gotResults[tc.ID] = tc.Result
					}
				}
				assert.Equal(t, tt.wantResults, gotResults, "%s must pair every tool_use with its own result", name)
			}
		})
	}
}
