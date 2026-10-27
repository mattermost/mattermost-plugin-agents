// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const attachmentFileID = "attachedfile000000000000id"

// attachmentConfig is an agent that loads MCP tools dynamically.
func attachmentConfig(configure func(*llm.BotConfig)) llm.BotConfig {
	cfg := policyBotConfig()
	cfg.MCPDynamicToolLoading = true
	if configure != nil {
		configure(&cfg)
	}
	return cfg
}

// setupAttachmentEnv is a policy environment whose embedded MCP server offers
// a read_file tool that counts its executions.
func setupAttachmentEnv(t *testing.T, cfg llm.BotConfig, llmResponses ...*llm.TextStreamResult) (*policyEnv, *atomic.Int32) {
	t.Helper()
	env := setupPolicyEnv(t, []llm.BotConfig{cfg}, llmResponses...)

	reads := &atomic.Int32{}
	readFile := embeddedTestTool("read_file", "file contents")
	readFile.Resolver = func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) {
		reads.Add(1)
		return "file contents", nil
	}
	env.mcpMgr.tools = append(env.mcpMgr.tools, readFile)
	env.checker.setPolicy(mcp.EmbeddedClientKey, "read_file", mcp.ToolPolicyAsk)
	env.mmClient.fileInfos = map[string]*model.FileInfo{
		attachmentFileID: {Id: attachmentFileID, Name: "report.pdf", MimeType: "application/pdf", Size: 512 * 1024},
	}
	return env, reads
}

func attachedFiles(attached bool) []string {
	if attached {
		return []string{attachmentFileID}
	}
	return nil
}

// readFileTools returns the read_file tools the model was handed, and whether
// it can call any of them.
func readFileTools(llmCtx *llm.Context) (names []string, callable bool) {
	for _, tool := range llmCtx.Tools.GetTools() {
		if llm.BareMCPToolName(tool.Name) != "read_file" {
			continue
		}
		names = append(names, tool.Name)
		callable = callable || llmCtx.IsToolCallable(tool.Name)
	}
	return names, callable
}

// TestAttachmentsPreloadReadFileOnNewTurns pins that an agent loading MCP tools
// dynamically gets read_file up front when the post that called it has files,
// so it can read them without a load_tool round trip, and that the agent's
// allowlist and the channel's tool calling setting still decide whether it may
// be called.
func TestAttachmentsPreloadReadFileOnNewTurns(t *testing.T) {
	tests := []struct {
		name               string
		configure          func(*llm.BotConfig)
		channelMention     bool
		channelToolCalling bool
		attached           bool
		wantTools          int
		wantCallable       bool
	}{
		{name: "DM with a file", attached: true, wantTools: 1, wantCallable: true},
		{name: "DM without a file"},
		{
			name:      "full MCP catalog already has it once",
			configure: func(cfg *llm.BotConfig) { cfg.MCPDynamicToolLoading = false },
			attached:  true, wantTools: 1, wantCallable: true,
		},
		{
			name: "allowlist without read_file",
			configure: func(cfg *llm.BotConfig) {
				cfg.AutoEnableNewMCPTools = false
				cfg.EnabledMCPTools = []llm.EnabledMCPTool{{ServerOrigin: policyToolOrigin, ToolName: policyToolName}}
			},
			attached: true,
		},
		{name: "channel mention with a file", channelMention: true, channelToolCalling: true, attached: true, wantTools: 1, wantCallable: true},
		{name: "channel mention with tool calling off", channelMention: true, attached: true, wantTools: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, _ := setupAttachmentEnv(t, attachmentConfig(tc.configure), dmMakeTextStream("answer"))
			env.config.enableChannelMentionToolCalling = tc.channelToolCalling

			var post *model.Post
			if tc.channelMention {
				post = env.rootPost(autoReplyUserID, "@"+autoReplyBotUsername+" summarize the report")
			} else {
				post = dmMessage(env, "summarize the report")
			}
			post.FileIds = attachedFiles(tc.attached)

			env.conversations.MessageHasBeenPosted(nil, post)
			env.waitForStream(t)

			requests := env.llmRequests()
			require.Len(t, requests, 1)
			names, callable := readFileTools(requests[0].Context)
			assert.Len(t, names, tc.wantTools, "read_file tools handed to the model: %v", names)
			assert.Equal(t, tc.wantCallable, callable)
		})
	}
}

// TestAttachmentReadFileSurvivesResume pins that resuming a run whose
// initiating post had files rebuilds the same tool set: regenerating offers
// read_file again, and approving a read_file call runs it instead of failing
// as an unloaded tool.
func TestAttachmentReadFileSurvivesResume(t *testing.T) {
	pendingRead := func() []conversation.ContentBlock {
		return []conversation.ContentBlock{{
			Type:         conversation.BlockTypeToolUse,
			ID:           "tc-read",
			Name:         "read_file",
			ServerOrigin: mcp.EmbeddedClientKey,
			Input:        json.RawMessage(`{"file_id":"` + attachmentFileID + `"}`),
			Status:       conversation.StatusPending,
			Shared:       new(false),
		}}
	}

	tests := []struct {
		name      string
		attached  bool
		wantReads int32
	}{
		{name: "initiating post had a file", attached: true, wantReads: 1},
		{name: "initiating post had no file"},
	}

	for _, tc := range tests {
		t.Run("approve in a channel/"+tc.name, func(t *testing.T) {
			env, reads := setupAttachmentEnv(t, attachmentConfig(nil))
			env.config.enableChannelMentionToolCalling = true

			initiator := humanPost("summarize the report")
			initiator.FileIds = attachedFiles(tc.attached)
			botPost := env.seedRun(t, env.channel, nil, initiator, pendingRead(), nil)

			require.NoError(t, env.conversations.HandleToolCall(context.Background(), autoReplyUserID, botPost, env.channel, []string{"tc-read"}, nil))

			assert.Equal(t, tc.wantReads, reads.Load())
		})

		t.Run("regenerate in a DM/"+tc.name, func(t *testing.T) {
			env, _ := setupAttachmentEnv(t, attachmentConfig(nil), dmMakeTextStream("answer"))

			initiator := humanPost("summarize the report")
			initiator.FileIds = attachedFiles(tc.attached)
			botPost := env.seedRun(t, env.dm, nil, initiator, earlierAnswer(), nil)

			require.NoError(t, env.conversations.HandleRegenerate(context.Background(), autoReplyUserID, botPost, env.dm))
			env.waitForStream(t)

			requests := env.llmRequests()
			require.Len(t, requests, 1)
			names, callable := readFileTools(requests[0].Context)
			assert.Equal(t, tc.attached, len(names) == 1 && callable, "read_file tools handed to the model: %v", names)
		})
	}
}

// TestAttachmentPreloadKeepsSearchTools pins that preloading read_file for a
// file attached to a search thread reply adds to the search tools that thread
// already preloads instead of replacing them.
func TestAttachmentPreloadKeepsSearchTools(t *testing.T) {
	env, _ := setupSearchTestEnv(t, append(embeddedSearchTestTools(), embeddedTestTool("read_file", "file contents")), nil, dmMakeTextStream("answer"))
	env.mmClient.fileInfos = map[string]*model.FileInfo{
		attachmentFileID: {Id: attachmentFileID, Name: "report.pdf", MimeType: "application/pdf", Size: 512 * 1024},
	}

	rootPostID := model.NewId()
	channelID := env.channelID
	env.mmClient.posts = map[string]*model.Post{rootPostID: {Id: rootPostID, UserId: env.userID, ChannelId: channelID, Message: "first question"}}
	_, err := env.convService.CreateConversation(conversation.CreateConversationParams{
		UserID:       env.userID,
		BotID:        env.botID,
		ChannelID:    &channelID,
		RootPostID:   &rootPostID,
		Operation:    llm.OperationSearch,
		SystemPrompt: "system",
		UserMessage:  "first question",
	})
	require.NoError(t, err)

	env.conversations.MessageHasBeenPosted(nil, &model.Post{
		Id:        model.NewId(),
		UserId:    env.userID,
		ChannelId: env.channelID,
		RootId:    rootPostID,
		Message:   "what does the attached report say?",
		FileIds:   []string{attachmentFileID},
	})
	waitForStreamFinished(t, env)

	env.fakeLLM.mu.Lock()
	require.Len(t, env.fakeLLM.requests, 1)
	llmCtx := env.fakeLLM.requests[0].Context
	env.fakeLLM.mu.Unlock()

	assert.Subset(t, visibleToolNames(llmCtx), []string{"search_posts", "read_post", "read_channel", "read_file"})
}
