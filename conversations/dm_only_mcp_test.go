// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations_test

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDMOnlyMCPServerIsOfferedOnlyInDM drives real posts through
// MessageHasBeenPosted and checks the request sent to the model: a DM-only
// server's tools reach the model only in the requester's DM with the agent,
// and elsewhere the prompt names the server so the model can point to a DM.
func TestDMOnlyMCPServerIsOfferedOnlyInDM(t *testing.T) {
	const (
		salesforceOrigin = "https://salesforce.example.com"
		docsOrigin       = "https://docs.example.com"
		dmOnlySection    = "limited these integrations to Direct Messages"
		salesforceLine   = "- Salesforce: CRM accounts and opportunities"
		dmChannelID      = "ardm-channel-id"
	)
	resolver := func(context.Context, *llm.Context, llm.ToolArgumentGetter) (string, error) { return "", nil }
	mcpTools := []llm.Tool{
		{Name: "salesforce__query_accounts", Description: "Query accounts", ServerOrigin: salesforceOrigin, Resolver: resolver},
		{Name: "docs__search", Description: "Search docs", ServerOrigin: docsOrigin, Resolver: resolver},
	}
	mention := "@" + autoReplyBotUsername + " what are our biggest accounts?"

	tests := []struct {
		name               string
		channelToolCalling bool
		post               func(*autoReplyTestEnv) *model.Post
		wantSalesforce     bool
		wantSection        bool
	}{
		{
			name:               "channel mention leaves the server out and points to a DM",
			channelToolCalling: true,
			post: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, mention)
			},
			wantSection: true,
		},
		{
			name: "channel mention with channel tool calling off still points to a DM",
			post: func(env *autoReplyTestEnv) *model.Post {
				return env.rootPost(autoReplyUserID, mention)
			},
			wantSection: true,
		},
		{
			name:               "follow-up in a channel thread leaves the server out",
			channelToolCalling: true,
			post: func(env *autoReplyTestEnv) *model.Post {
				return env.threadReply(autoReplyUserID, mention, true)
			},
			wantSection: true,
		},
		{
			name:               "DM with the agent offers the server",
			channelToolCalling: true,
			post: func(env *autoReplyTestEnv) *model.Post {
				env.mmClient.channels[dmChannelID] = &model.Channel{
					Id:   dmChannelID,
					Type: model.ChannelTypeDirect,
					Name: model.GetDMNameFromIds(autoReplyUserID, autoReplyBotUserID),
				}
				post := &model.Post{Id: "ardm-post-id", ChannelId: dmChannelID, UserId: autoReplyUserID, CreateAt: 300, Message: "what are our biggest accounts?"}
				env.setThread(post.Id, post)
				return post
			},
			wantSalesforce: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			botConfig := autoReplyBotConfig()
			botConfig.AutoEnableNewMCPTools = true
			env := setupAutoReplyTestEnvWithTools(t,
				[]llm.BotConfig{botConfig},
				nil,
				&testToolCallingConfig{enableChannelMentionToolCalling: tt.channelToolCalling},
				dmMakeTextStream("done"),
			)
			env.mcpMgr.tools = mcpTools
			env.mcpMgr.dmOnly = []llm.DMOnlyMCPServer{{Name: "Salesforce", Description: "CRM accounts and opportunities"}}
			env.mcpMgr.dmOnlyOrigins = map[string]string{"Salesforce": salesforceOrigin}

			env.conversations.MessageHasBeenPosted(nil, tt.post(env))

			require.NotEmpty(t, env.fakeLLM.requests, "the post must reach the model")
			request := env.fakeLLM.requests[0]
			require.NotNil(t, request.Context.Tools)
			assert.NotNil(t, request.Context.Tools.GetTool("docs__search"), "servers that are not DM-only stay available")
			if tt.wantSalesforce {
				assert.NotNil(t, request.Context.Tools.GetTool("salesforce__query_accounts"))
			} else {
				assert.Nil(t, request.Context.Tools.GetTool("salesforce__query_accounts"), "a DM-only server's tools must not reach the model outside a DM")
				for _, disabled := range request.Context.DisabledTools {
					assert.NotEqual(t, "salesforce__query_accounts", disabled.Name, "a DM-only server's tools must not be listed outside a DM")
				}
			}

			systemPost := request.Posts[0]
			require.Equal(t, llm.PostRoleSystem, systemPost.Role)
			if tt.wantSection {
				assert.Contains(t, systemPost.Message, dmOnlySection)
				assert.Contains(t, systemPost.Message, salesforceLine)
			} else {
				assert.NotContains(t, systemPost.Message, dmOnlySection)
			}
		})
	}
}
