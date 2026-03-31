// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-ai/i18n"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

type mockPolicyChecker struct {
	approveAll bool
	approved   map[string]bool
}

func (m *mockPolicyChecker) GetToolPolicy(serverBaseURL string, toolName string) (string, bool) {
	if m == nil {
		return "ask", false
	}
	if m.approveAll {
		// Still reject empty server origins (built-in tools)
		if serverBaseURL == "" {
			return "ask", false
		}
		return mcp.ToolPolicyAutoRun, true
	}
	if m.approved[toolName] {
		return mcp.ToolPolicyAutoRun, true
	}
	return "ask", true
}

func TestAreAllToolCallsAutoApprovable(t *testing.T) {
	tests := []struct {
		name      string
		toolCalls []llm.ToolCall
		checker   ToolPolicyChecker
		expected  bool
	}{
		{
			name:      "nil checker returns false",
			toolCalls: []llm.ToolCall{{Name: "test", ServerOrigin: "https://example.com"}},
			checker:   nil,
			expected:  false,
		},
		{
			name:      "empty tool calls returns false",
			toolCalls: []llm.ToolCall{},
			checker:   &mockPolicyChecker{approveAll: true},
			expected:  false,
		},
		{
			name: "all tools auto-approvable returns true",
			toolCalls: []llm.ToolCall{
				{Name: "get_issue", ServerOrigin: "https://api.github.com"},
				{Name: "list_repos", ServerOrigin: "https://api.github.com"},
			},
			checker:  &mockPolicyChecker{approveAll: true},
			expected: true,
		},
		{
			name: "one tool not auto-approvable returns false",
			toolCalls: []llm.ToolCall{
				{Name: "get_issue", ServerOrigin: "https://api.github.com"},
				{Name: "create_issue", ServerOrigin: "https://api.github.com"},
			},
			checker:  &mockPolicyChecker{approved: map[string]bool{"get_issue": true}},
			expected: false,
		},
		{
			name: "tool with empty server origin not auto-approvable",
			toolCalls: []llm.ToolCall{
				{Name: "builtin_tool", ServerOrigin: ""},
			},
			checker:  &mockPolicyChecker{approveAll: true},
			expected: false,
		},
		{
			name: "mixed servers - all approved",
			toolCalls: []llm.ToolCall{
				{Name: "get_issue", ServerOrigin: "https://api.github.com"},
				{Name: "getJiraIssue", ServerOrigin: "https://mcp.atlassian.com"},
			},
			checker:  &mockPolicyChecker{approveAll: true},
			expected: true,
		},
		{
			name: "single tool approved",
			toolCalls: []llm.ToolCall{
				{Name: "get_issue", ServerOrigin: "https://api.github.com"},
			},
			checker:  &mockPolicyChecker{approveAll: true},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := &MMPostStreamService{
				toolPolicyChecker: tc.checker,
			}
			result := service.areAllToolCallsAutoApprovable(tc.toolCalls)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestToolPolicyFunc(t *testing.T) {
	called := false
	fn := ToolPolicyFunc(func(serverBaseURL string, toolName string) (string, bool) {
		called = true
		if serverBaseURL == "https://example.com" && toolName == "read_tool" {
			return mcp.ToolPolicyAutoRun, true
		}
		return "ask", true
	})

	policy, enabled := fn.GetToolPolicy("https://example.com", "read_tool")
	require.True(t, called)
	require.Equal(t, mcp.ToolPolicyAutoRun, policy)
	require.True(t, enabled)

	policy, enabled = fn.GetToolPolicy("https://example.com", "write_tool")
	require.Equal(t, "ask", policy)
	require.True(t, enabled)

	policy, enabled = fn.GetToolPolicy("https://other.com", "read_tool")
	require.Equal(t, "ask", policy)
	require.True(t, enabled)
}

func TestStreamToPostAutoApproval(t *testing.T) {
	const (
		postID      = "post-id"
		channelID   = "channel-id"
		botID       = "bot-id"
		requesterID = "requester-id"
	)

	tests := []struct {
		name                   string
		toolCalls              []llm.ToolCall
		isDM                   bool
		policyChecker          ToolPolicyChecker
		expectAutoApprovedProp bool
		expectCallbackInvoked  bool
	}{
		{
			name: "all tools auto-approvable in channel - auto-approved",
			toolCalls: []llm.ToolCall{
				{ID: "tc-1", Name: "get_issue", ServerOrigin: "https://api.github.com"},
			},
			isDM:                   false,
			policyChecker:          &mockPolicyChecker{approveAll: true},
			expectAutoApprovedProp: true,
			expectCallbackInvoked:  true,
		},
		{
			name: "multiple tools all auto-approvable in channel",
			toolCalls: []llm.ToolCall{
				{ID: "tc-1", Name: "get_issue", ServerOrigin: "https://api.github.com"},
				{ID: "tc-2", Name: "list_repos", ServerOrigin: "https://api.github.com"},
			},
			isDM:                   false,
			policyChecker:          &mockPolicyChecker{approveAll: true},
			expectAutoApprovedProp: true,
			expectCallbackInvoked:  true,
		},
		{
			name: "pre-executed error in channel does not trigger callback loop",
			toolCalls: []llm.ToolCall{
				{
					ID:           "tc-err-1",
					Name:         "get_channel_info",
					ServerOrigin: "https://api.github.com",
					Status:       llm.ToolCallStatusError,
					Result:       "Error: insufficient parameters for channel lookup",
				},
			},
			isDM:                   false,
			policyChecker:          &mockPolicyChecker{approveAll: true},
			expectAutoApprovedProp: true,
			expectCallbackInvoked:  false,
		},
		{
			name: "not all tools auto-approvable - standard flow",
			toolCalls: []llm.ToolCall{
				{ID: "tc-1", Name: "get_issue", ServerOrigin: "https://api.github.com"},
				{ID: "tc-2", Name: "create_issue", ServerOrigin: "https://api.github.com"},
			},
			isDM:                   false,
			policyChecker:          &mockPolicyChecker{approved: map[string]bool{"get_issue": true}},
			expectAutoApprovedProp: false,
			expectCallbackInvoked:  false,
		},
		{
			name: "DM - shows tool progress with auto-approved prop when all auto-approvable",
			toolCalls: []llm.ToolCall{
				{ID: "tc-1", Name: "get_issue", ServerOrigin: "https://api.github.com"},
			},
			isDM:                   true,
			policyChecker:          &mockPolicyChecker{approveAll: true},
			expectAutoApprovedProp: true,
			expectCallbackInvoked:  false,
		},
		{
			name: "DM - does not set auto-approved prop when not all tools auto-approvable",
			toolCalls: []llm.ToolCall{
				{ID: "tc-1", Name: "get_issue", ServerOrigin: "https://api.github.com"},
				{ID: "tc-2", Name: "create_issue", ServerOrigin: "https://api.github.com"},
			},
			isDM:                   true,
			policyChecker:          &mockPolicyChecker{approved: map[string]bool{"get_issue": true}},
			expectAutoApprovedProp: false,
			expectCallbackInvoked:  false,
		},
		{
			name: "no auto-approver configured - standard flow",
			toolCalls: []llm.ToolCall{
				{ID: "tc-1", Name: "get_issue", ServerOrigin: "https://api.github.com"},
			},
			isDM:                   false,
			policyChecker:          nil,
			expectAutoApprovedProp: false,
			expectCallbackInvoked:  false,
		},
		{
			name: "built-in tool with no server origin - standard flow",
			toolCalls: []llm.ToolCall{
				{ID: "tc-1", Name: "builtin_tool", ServerOrigin: ""},
			},
			isDM:                   false,
			policyChecker:          &mockPolicyChecker{approveAll: true},
			expectAutoApprovedProp: false,
			expectCallbackInvoked:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var channel *model.Channel
			if tc.isDM {
				channel = &model.Channel{Id: channelID, Type: model.ChannelTypeDirect, Name: botID + "__" + requesterID}
			} else {
				channel = &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}
			}

			client := &fakeStreamingClient{
				channels: map[string]*model.Channel{
					channelID: channel,
				},
			}

			var callbackMu sync.Mutex
			callbackInvoked := false
			var callbackPostID, callbackRequesterID string

			service := NewMMPostStreamService(client, i18n.Init())
			service.SetToolPolicyChecker(tc.policyChecker)
			service.SetAutoExecuteCallback(func(pID string, rID string, _ []string) {
				callbackMu.Lock()
				defer callbackMu.Unlock()
				callbackInvoked = true
				callbackPostID = pID
				callbackRequesterID = rID
			})

			post := &model.Post{
				Id:        postID,
				ChannelId: channelID,
				UserId:    botID,
			}
			post.AddProp(LLMRequesterUserID, requesterID)

			streamChannel := make(chan llm.TextStreamEvent, 1)
			streamChannel <- llm.TextStreamEvent{
				Type:  llm.EventTypeToolCalls,
				Value: tc.toolCalls,
			}
			close(streamChannel)

			service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

			// Verify auto-approved prop
			if tc.expectAutoApprovedProp {
				require.Equal(t, "true", post.GetProp(AutoApprovedToolCallProp))
			} else {
				require.Nil(t, post.GetProp(AutoApprovedToolCallProp))
			}

			// Verify callback was (or wasn't) invoked
			// The callback runs in a goroutine, so wait briefly
			if tc.expectCallbackInvoked {
				require.Eventually(t, func() bool {
					callbackMu.Lock()
					defer callbackMu.Unlock()
					return callbackInvoked
				}, time.Second, 10*time.Millisecond)

				callbackMu.Lock()
				require.Equal(t, postID, callbackPostID)
				require.Equal(t, requesterID, callbackRequesterID)
				callbackMu.Unlock()
			} else {
				// Brief wait to ensure callback wasn't invoked
				time.Sleep(50 * time.Millisecond)
				callbackMu.Lock()
				require.False(t, callbackInvoked)
				callbackMu.Unlock()
			}

			// Verify post was updated
			require.GreaterOrEqual(t, len(client.updatedPosts), 1)

			// Verify tool calls are redacted for channel, unredacted for DM
			if !tc.isDM {
				require.Equal(t, "true", post.GetProp(ToolCallRedactedProp))
				toolCallProp, ok := post.GetProp(ToolCallProp).(string)
				require.True(t, ok)
				var storedCalls []llm.ToolCall
				require.NoError(t, json.Unmarshal([]byte(toolCallProp), &storedCalls))
				for _, call := range storedCalls {
					require.Equal(t, "{}", string(call.Arguments))
					require.Empty(t, call.Result)
				}
			}

			// Verify KV store has tool calls for channel
			if !tc.isDM {
				kvKey := ToolCallPrivateKVKey(postID, requesterID)
				storedKV, kvFound := client.kv[kvKey]
				require.True(t, kvFound)
				kvCalls, kvCallsOK := storedKV.([]llm.ToolCall)
				require.True(t, kvCallsOK)
				require.Len(t, kvCalls, len(tc.toolCalls))
			}
		})
	}
}

func TestHasAutoApprovedToolCallsTreatsErrorAsPreExecuted(t *testing.T) {
	require.False(t, llm.HasPreExecutedToolCalls(nil))
	require.False(t, llm.HasPreExecutedToolCalls([]llm.ToolCall{{Status: llm.ToolCallStatusPending}}))
	require.True(t, llm.HasPreExecutedToolCalls([]llm.ToolCall{{Status: llm.ToolCallStatusAutoApproved}}))
	require.True(t, llm.HasPreExecutedToolCalls([]llm.ToolCall{{Status: llm.ToolCallStatusError}}))
}

func TestStreamToPostDMPreservesAutoApprovedStatus(t *testing.T) {
	const (
		postID      = "post-id"
		channelID   = "channel-id"
		botID       = "bot-id"
		requesterID = "requester-id"
	)

	toolCalls := []llm.ToolCall{
		{
			ID:        "tool-1",
			Name:      "search",
			Arguments: json.RawMessage(`{"query":"test"}`),
			Result:    "search result",
			Status:    llm.ToolCallStatusAutoApproved,
		},
	}

	client := &fakeStreamingClient{
		channels: map[string]*model.Channel{
			channelID: {
				Id:   channelID,
				Type: model.ChannelTypeDirect,
				Name: botID + "__" + requesterID,
			},
		},
	}
	service := NewMMPostStreamService(client, i18n.Init())

	post := &model.Post{
		Id:        postID,
		ChannelId: channelID,
		UserId:    botID,
	}
	post.AddProp(LLMRequesterUserID, requesterID)

	streamChannel := make(chan llm.TextStreamEvent, 1)
	streamChannel <- llm.TextStreamEvent{
		Type:  llm.EventTypeToolCalls,
		Value: toolCalls,
	}
	close(streamChannel)

	service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

	require.GreaterOrEqual(t, len(client.updatedPosts), 1)

	toolCallProp, ok := post.GetProp(ToolCallProp).(string)
	require.True(t, ok)

	var storedCalls []llm.ToolCall
	require.NoError(t, json.Unmarshal([]byte(toolCallProp), &storedCalls))
	require.Len(t, storedCalls, 1)
	// DM path preserves AutoApproved status so UI can label per-tool auto-approval.
	require.Equal(t, llm.ToolCallStatusAutoApproved, storedCalls[0].Status)

	toolEvent, found := findToolCallEvent(client.events)
	require.True(t, found)
	toolCallPayload, ok := toolEvent.payload["tool_call"].(string)
	require.True(t, ok)
	var eventCalls []llm.ToolCall
	require.NoError(t, json.Unmarshal([]byte(toolCallPayload), &eventCalls))
	require.Len(t, eventCalls, 1)
	require.Equal(t, llm.ToolCallStatusAutoApproved, eventCalls[0].Status)
}

func TestStreamToPostDMMarksSuccessfulAutoApprovableToolsAsAutoApproved(t *testing.T) {
	const (
		postID      = "post-id"
		channelID   = "channel-id"
		botID       = "bot-id"
		requesterID = "requester-id"
	)

	toolCalls := []llm.ToolCall{
		{
			ID:           "tool-1",
			Name:         "get_channel_info",
			ServerOrigin: "https://api.github.com",
			Status:       llm.ToolCallStatusSuccess,
		},
		{
			ID:           "tool-2",
			Name:         "create_post",
			ServerOrigin: "https://api.github.com",
			Status:       llm.ToolCallStatusPending,
		},
	}

	client := &fakeStreamingClient{
		channels: map[string]*model.Channel{
			channelID: {
				Id:   channelID,
				Type: model.ChannelTypeDirect,
				Name: botID + "__" + requesterID,
			},
		},
	}
	service := NewMMPostStreamService(client, i18n.Init())
	service.SetToolPolicyChecker(&mockPolicyChecker{
		approved: map[string]bool{
			"get_channel_info": true,
		},
	})

	post := &model.Post{
		Id:        postID,
		ChannelId: channelID,
		UserId:    botID,
	}
	post.AddProp(LLMRequesterUserID, requesterID)

	streamChannel := make(chan llm.TextStreamEvent, 1)
	streamChannel <- llm.TextStreamEvent{
		Type:  llm.EventTypeToolCalls,
		Value: toolCalls,
	}
	close(streamChannel)

	service.StreamToPost(context.Background(), &llm.TextStreamResult{Stream: streamChannel}, post, "en")

	toolCallProp, ok := post.GetProp(ToolCallProp).(string)
	require.True(t, ok)

	var storedCalls []llm.ToolCall
	require.NoError(t, json.Unmarshal([]byte(toolCallProp), &storedCalls))
	require.Len(t, storedCalls, 2)
	require.Equal(t, llm.ToolCallStatusAutoApproved, storedCalls[0].Status)
	require.Equal(t, llm.ToolCallStatusPending, storedCalls[1].Status)
}
