// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/delegation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAIBotsServer(t *testing.T, bots []AIBotInfo) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/plugins/mattermost-ai/ai_bots", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AIBotsResponse{Bots: bots})
	})

	return httptest.NewServer(mux)
}

func TestListAgents(t *testing.T) {
	sampleBots := []AIBotInfo{
		{ID: "bot1id12345678901234567", DisplayName: "Otto", Username: "otto"},
		{ID: "bot2id12345678901234567", DisplayName: "Claude", Username: "claude"},
	}

	ts := newTestAIBotsServer(t, sampleBots)
	defer ts.Close()

	t.Run("marks self agent", func(t *testing.T) {
		provider := newTestProvider(t, ts.URL)
		mcpCtx := &MCPToolContext{BotUserID: "bot1id12345678901234567", Client: newTestClient(ts.URL)}

		result, err := provider.toolListAgents(mcpCtx, ListAgentsArgs{})
		require.NoError(t, err)
		assert.Contains(t, result, "This is YOU")
	})

	t.Run("unreachable server", func(t *testing.T) {
		provider := newTestProvider(t, "http://127.0.0.1:1")
		mcpCtx := &MCPToolContext{Client: newTestClient("http://127.0.0.1:1")}

		_, err := provider.toolListAgents(mcpCtx, ListAgentsArgs{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch agents")
	})
}

// fakeDelegationService records the request it received and returns canned results.
type fakeDelegationService struct {
	available bool
	result    string
	err       error
	gotReq    *delegation.Request
}

func (f *fakeDelegationService) Delegate(_ context.Context, req delegation.Request) (string, error) {
	f.gotReq = &req
	return f.result, f.err
}

func (f *fakeDelegationService) Available() bool {
	return f.available
}

func TestAskAgent(t *testing.T) {
	baseContext := func() *MCPToolContext {
		return &MCPToolContext{
			Ctx:              context.Background(),
			UserID:           "session-user-id",
			BotUserID:        "delegating-bot-id",
			ParentToolCallID: "toolcall-1",
		}
	}

	tests := []struct {
		name        string
		service     *fakeDelegationService
		mcpContext  *MCPToolContext
		args        AskAgentArgs
		wantResult  string
		wantErrPart string
		wantReq     *delegation.Request
	}{
		{
			name:       "happy path passes session identity, never args",
			service:    &fakeDelegationService{available: true, result: "the answer"},
			mcpContext: baseContext(),
			args:       AskAgentArgs{Agent: "projects", Task: "What shipped last sprint?"},
			wantResult: "the answer",
			wantReq: &delegation.Request{
				InitiatorUserID:     "session-user-id",
				DelegatingBotUserID: "delegating-bot-id",
				TargetAgent:         "projects",
				Task:                "What shipped last sprint?",
				ParentToolCallID:    "toolcall-1",
			},
		},
		{
			name:       "arguments are trimmed",
			service:    &fakeDelegationService{available: true, result: "ok"},
			mcpContext: baseContext(),
			args:       AskAgentArgs{Agent: "  @projects  ", Task: "  do it  "},
			wantResult: "ok",
			wantReq: &delegation.Request{
				InitiatorUserID:     "session-user-id",
				DelegatingBotUserID: "delegating-bot-id",
				TargetAgent:         "@projects",
				Task:                "do it",
				ParentToolCallID:    "toolcall-1",
			},
		},
		{
			name:        "nil delegation service",
			service:     nil,
			mcpContext:  baseContext(),
			args:        AskAgentArgs{Agent: "projects", Task: "task"},
			wantErrPart: "not available",
		},
		{
			name:        "unavailable delegation service",
			service:     &fakeDelegationService{available: false},
			mcpContext:  baseContext(),
			args:        AskAgentArgs{Agent: "projects", Task: "task"},
			wantErrPart: "not available",
		},
		{
			name:    "no authenticated session user",
			service: &fakeDelegationService{available: true},
			mcpContext: &MCPToolContext{
				Ctx:       context.Background(),
				BotUserID: "delegating-bot-id",
			},
			args:        AskAgentArgs{Agent: "projects", Task: "task"},
			wantErrPart: "authenticated user",
		},
		{
			name:    "no delegating agent identity",
			service: &fakeDelegationService{available: true},
			mcpContext: &MCPToolContext{
				Ctx:    context.Background(),
				UserID: "session-user-id",
			},
			args:        AskAgentArgs{Agent: "projects", Task: "task"},
			wantErrPart: "only available to agents",
		},
		{
			name:        "missing agent",
			service:     &fakeDelegationService{available: true},
			mcpContext:  baseContext(),
			args:        AskAgentArgs{Task: "task"},
			wantErrPart: "agent is required",
		},
		{
			name:        "missing task",
			service:     &fakeDelegationService{available: true},
			mcpContext:  baseContext(),
			args:        AskAgentArgs{Agent: "projects"},
			wantErrPart: "task is required",
		},
		{
			name:        "task too long",
			service:     &fakeDelegationService{available: true},
			mcpContext:  baseContext(),
			args:        AskAgentArgs{Agent: "projects", Task: strings.Repeat("x", maxDelegationTaskLength+1)},
			wantErrPart: "task is too long",
		},
		{
			name:        "delegation error text is model-visible",
			service:     &fakeDelegationService{available: true, err: errors.New("unknown agent: no agent named \"x\"")},
			mcpContext:  baseContext(),
			args:        AskAgentArgs{Agent: "x", Task: "task"},
			wantErrPart: "unknown agent",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := newTestProvider(t, "http://example.invalid")
			if tc.service != nil {
				provider.delegationService = tc.service
			}

			result, err := provider.toolAskAgent(tc.mcpContext, tc.args)

			if tc.wantErrPart != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrPart)
				if tc.service != nil && tc.service.err == nil {
					assert.Nil(t, tc.service.gotReq, "delegation must not run on validation failure")
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantResult, result)
			require.NotNil(t, tc.service.gotReq)
			assert.Equal(t, *tc.wantReq, *tc.service.gotReq)
		})
	}
}

// TestAskAgentIdentityFromSessionNotArgs pins the security property that the
// initiator identity can never be overridden through tool arguments: the
// args struct simply has no identity fields, and the resolver forwards only
// session-derived identity.
func TestAskAgentIdentityFromSessionNotArgs(t *testing.T) {
	service := &fakeDelegationService{available: true, result: "ok"}
	provider := newTestProvider(t, "http://example.invalid")
	provider.delegationService = service

	mcpCtx := &MCPToolContext{
		Ctx:              context.Background(),
		UserID:           "real-session-user",
		BotUserID:        "real-bot",
		ParentToolCallID: "real-toolcall",
	}

	// Simulate a malicious model injecting identity-looking fields: the typed
	// decoder ignores unknown fields, so they can never reach the service.
	var args AskAgentArgs
	raw := []byte(`{"agent":"projects","task":"t","initiator_user_id":"attacker","bot_user_id":"attacker-bot","parent_tool_call_id":"forged"}`)
	require.NoError(t, json.Unmarshal(raw, &args))

	_, err := provider.toolAskAgent(mcpCtx, args)
	require.NoError(t, err)

	require.NotNil(t, service.gotReq)
	assert.Equal(t, "real-session-user", service.gotReq.InitiatorUserID)
	assert.Equal(t, "real-bot", service.gotReq.DelegatingBotUserID)
	assert.Equal(t, "real-toolcall", service.gotReq.ParentToolCallID)
}
