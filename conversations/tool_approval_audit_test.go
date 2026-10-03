// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/streaming"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// plantedToolArgument is a distinctive tool argument value planted in the
// fixtures: if it ever shows up in a marshaled audit record, tool arguments
// leaked into the audit trail.
const plantedToolArgument = "SECRET-ARGUMENT-c2VjcmV0LXZhbHVl"

// TestHandleToolCallAuditRecordsDecision proves HandleToolCall enriches an
// audit record carried in ctx with the agent and the tool names it resolved:
// clicked accepts and auto-executions land in accepted_tools, everything
// still pending lands in rejected_tools, previously resolved blocks appear in
// neither, and tool arguments never enter the record.
func TestHandleToolCallAuditRecordsDecision(t *testing.T) {
	tests := []struct {
		name          string
		blocks        []conversation.ContentBlock
		acceptedIDs   []string
		policyChecker mapPolicyChecker
		wantAccepted  []string
		wantRejected  []string
	}{
		{
			name: "clicked accept and reject resolve to tool names, prior statuses excluded",
			blocks: []conversation.ContentBlock{
				{
					Type:   conversation.BlockTypeToolUse,
					ID:     "tool-use-1",
					Name:   "jira__get_issue",
					Input:  json.RawMessage(`{"issue_key":"` + plantedToolArgument + `"}`),
					Status: conversation.StatusPending,
				},
				{
					Type:   conversation.BlockTypeToolUse,
					ID:     "tool-use-2",
					Name:   "jira__transition_issue",
					Input:  json.RawMessage(`{}`),
					Status: conversation.StatusPending,
				},
				{
					Type:   conversation.BlockTypeToolUse,
					ID:     "tool-use-3",
					Name:   "jira__previously_resolved",
					Input:  json.RawMessage(`{}`),
					Status: conversation.StatusAutoApproved,
				},
			},
			acceptedIDs:  []string{"tool-use-1"},
			wantAccepted: []string{"jira__get_issue"},
			wantRejected: []string{"jira__transition_issue"},
		},
		{
			name: "tool auto-executed this call is recorded as accepted",
			blocks: []conversation.ContentBlock{{
				Type:             conversation.BlockTypeToolUse,
				ID:               "tool-use-1",
				Name:             "jira__get_issue",
				Input:            json.RawMessage(`{"issue_key":"` + plantedToolArgument + `"}`),
				Status:           conversation.StatusPending,
				WouldAutoExecute: true,
			}},
			acceptedIDs: []string{},
			policyChecker: mapPolicyChecker{
				"https://jira.example.com": {"get_issue": {policy: mcp.ToolPolicyAutoRunEverywhere, enabled: true}},
			},
			wantAccepted: []string{"jira__get_issue"},
			wantRejected: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			convStore, conv := loadedStateConversationStore()
			nextSeq := 1
			seedLoadToolPair(t, convStore, conv.ID, "load-1", "jira__get_issue", &nextSeq)

			content, err := json.Marshal(tc.blocks)
			require.NoError(t, err)
			approvalPostID := "approval-post-id"
			require.NoError(t, convStore.CreateTurn(&store.Turn{
				ID:             "assistant-turn",
				ConversationID: conv.ID,
				PostID:         &approvalPostID,
				Role:           "assistant",
				Content:        content,
				Sequence:       nextSeq,
			}))

			mockAPI := &plugintest.API{}
			pluginAPI := pluginapi.NewClient(mockAPI, nil)
			licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
			botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
			lm := &loadedStateLLM{}
			botsService.SetBotsForTesting([]*bots.Bot{loadedStateBot(lm)})

			mmClient := mocks.NewMockClient(t)
			mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()
			mmClient.On("GetUser", "user-id").Maybe().Return(&model.User{Id: "user-id", Username: "user"}, nil)
			mmClient.On("KVGet", mock.Anything, mock.Anything).Maybe().Return(nil)
			mmClient.On("GetConfig").Maybe().Return(&model.Config{})

			streamingService := &loadedStateStreamingService{}
			c := &Conversations{
				mmClient:          mmClient,
				contextBuilder:    loadedStateBuilder(t),
				bots:              botsService,
				convService:       conversation.NewService(convStore, nil, nil, nil),
				streamingService:  streamingService,
				toolPolicyChecker: tc.policyChecker,
			}

			approvalPost := &model.Post{Id: approvalPostID, UserId: "bot-id"}
			approvalPost.AddProp(streaming.ConversationIDProp, conv.ID)
			channel := &model.Channel{Id: "channel-id", TeamId: "team-id", Type: model.ChannelTypeOpen}

			rec := plugin.MakeAuditRecord("toolCallApproval", model.AuditStatusFail)
			ctx := audit.WithRecord(context.Background(), rec)

			require.NoError(t, c.HandleToolCall(ctx, "user-id", approvalPost, channel, tc.acceptedIDs, nil))
			streamingService.waitForStreaming()

			assert.Equal(t, "bot-id", rec.EventData.Parameters[audit.KeyAgentID])
			assert.Equal(t, tc.wantAccepted, rec.EventData.Parameters["accepted_tools"])
			assert.Equal(t, tc.wantRejected, rec.EventData.Parameters["rejected_tools"])

			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), plantedToolArgument,
				"tool arguments must never enter the audit record")
			assert.NotContains(t, string(raw), "restored-result",
				"tool results must never enter the audit record")
		})
	}
}

// TestHandleToolResultAuditRecordsDecision proves HandleToolResult records the
// share/keep-private decision by tool name — shared names in accepted_tools,
// kept-private names in rejected_tools — without result content, and that an
// idempotent repeat click does not claim a decision it did not make.
func TestHandleToolResultAuditRecordsDecision(t *testing.T) {
	const plantedResult = "SECRET-RESULT-dG9wLXNlY3JldA"

	convStore, conv := loadedStateConversationStore()
	clickedPostID := "clicked-post-id"

	assistantBlocks := []conversation.ContentBlock{
		{Type: conversation.BlockTypeToolUse, ID: "tool-use-a", Name: "jira__get_issue", Status: conversation.StatusSuccess},
		{Type: conversation.BlockTypeToolUse, ID: "tool-use-b", Name: "jira__transition_issue", Status: conversation.StatusSuccess},
	}
	resultBlocks := []conversation.ContentBlock{
		{Type: conversation.BlockTypeToolResult, ToolUseID: "tool-use-a", Content: plantedResult, Status: conversation.StatusSuccess},
		{Type: conversation.BlockTypeToolResult, ToolUseID: "tool-use-b", Content: "result-b", Status: conversation.StatusSuccess},
	}
	assistantContent, err := json.Marshal(assistantBlocks)
	require.NoError(t, err)
	resultContent, err := json.Marshal(resultBlocks)
	require.NoError(t, err)
	for _, turn := range []store.Turn{
		{ID: "assistant-turn", ConversationID: conv.ID, PostID: &clickedPostID, Role: "assistant", Content: assistantContent, Sequence: 1},
		{ID: "result-turn", ConversationID: conv.ID, Role: "tool_result", Content: resultContent, Sequence: 2},
	} {
		require.NoError(t, convStore.CreateTurn(&turn))
	}

	mockAPI := &plugintest.API{}
	pluginAPI := pluginapi.NewClient(mockAPI, nil)
	licenseChecker := enterprise.NewLicenseChecker(pluginAPI)
	botsService := bots.New(mockAPI, pluginAPI, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)
	botsService.SetBotsForTesting([]*bots.Bot{loadedStateBot(&loadedStateLLM{})})

	mmClient := mocks.NewMockClient(t)
	mmClient.On("LogDebug", mock.Anything, mock.Anything).Maybe().Return()

	c := &Conversations{
		mmClient:    mmClient,
		bots:        botsService,
		convService: conversation.NewService(convStore, nil, nil, nil),
	}

	clickedPost := &model.Post{Id: clickedPostID, UserId: "bot-id"}
	clickedPost.AddProp(streaming.ConversationIDProp, conv.ID)
	channel := &model.Channel{Id: "dm-channel", Type: model.ChannelTypeDirect, Name: "bot-id__user-id"}

	rec := plugin.MakeAuditRecord("toolResultApproval", model.AuditStatusFail)
	ctx := audit.WithRecord(context.Background(), rec)

	require.NoError(t, c.HandleToolResult(ctx, "user-id", clickedPost, channel, []string{"tool-use-a"}))

	assert.Equal(t, "bot-id", rec.EventData.Parameters[audit.KeyAgentID])
	assert.Equal(t, []string{"jira__get_issue"}, rec.EventData.Parameters["accepted_tools"])
	assert.Equal(t, []string{"jira__transition_issue"}, rec.EventData.Parameters["rejected_tools"])

	raw, err := json.Marshal(rec)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), plantedResult,
		"tool result content must never enter the audit record")

	// A repeat click is a no-op (every result already carries DecidedAt), so
	// its record must not claim a share resolution that did not happen.
	repeatRec := plugin.MakeAuditRecord("toolResultApproval", model.AuditStatusFail)
	repeatCtx := audit.WithRecord(context.Background(), repeatRec)
	require.NoError(t, c.HandleToolResult(repeatCtx, "user-id", clickedPost, channel, []string{"tool-use-a"}))

	assert.Equal(t, "bot-id", repeatRec.EventData.Parameters[audit.KeyAgentID])
	assert.NotContains(t, repeatRec.EventData.Parameters, "accepted_tools")
	assert.NotContains(t, repeatRec.EventData.Parameters, "rejected_tools")
}
