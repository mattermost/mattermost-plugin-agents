// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type capturingLLM struct {
	last llm.CompletionRequest
}

func (c *capturingLLM) ChatCompletion(_ context.Context, request llm.CompletionRequest, _ ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	c.last = request
	return nil, nil
}

func (c *capturingLLM) ChatCompletionNoStream(_ context.Context, request llm.CompletionRequest, _ ...llm.LanguageModelOption) (string, error) {
	c.last = request
	return "", nil
}

func (c *capturingLLM) CountTokens(_ context.Context, request llm.CompletionRequest, _ ...llm.LanguageModelOption) (int, error) {
	c.last = request
	return 0, llm.ErrUnsupportedTokenCount
}

func (c *capturingLLM) InputTokenLimit() int  { return 4096 }
func (c *capturingLLM) OutputTokenLimit() int { return 4096 }

// TestBuildLLMFallbackAccessOnlyForAgents pins that the per-user fallback
// trimming is part of the agent chain only. A direct service call carries
// user_id for attribution, so a denied fallback must not change its request.
func TestBuildLLMFallbackAccessOnlyForAgents(t *testing.T) {
	userID := model.NewId()
	primaryID := model.NewId()
	fallbackID := model.NewId()

	tests := []struct {
		name         string
		botConfig    *llm.BotConfig
		wantRestrict bool
	}{
		{
			name:         "agent call trims the chain for the requesting user",
			botConfig:    &llm.BotConfig{Name: "agent"},
			wantRestrict: true,
		},
		{
			name:         "service call leaves the chain untouched",
			botConfig:    nil,
			wantRestrict: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupABACTestEnvironment(t, abacStubClient{perID: map[string]*model.AccessDecision{fallbackID: abacDeny()}})
			defer e.Cleanup(t)
			primary := openAIService(primaryID, fallbackID)
			fallback := openAIService(fallbackID, "")
			e.bots.config = &mockConfig{services: []llm.ServiceConfig{primary, fallback}}

			inner := &capturingLLM{}
			e.bots.SetBaseLLMBuilderForTest(func(llm.ServiceConfig, llm.BotConfig, []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
				return inner, func() {}, nil
			})

			built, shutdown, err := e.bots.buildLLM(primary, tc.botConfig, []llm.ServiceConfig{fallback})
			require.NoError(t, err)
			defer shutdown()

			_, err = built.ChatCompletion(context.Background(), llm.CompletionRequest{
				Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
				Context: &llm.Context{RequestingUser: &model.User{Id: userID}},
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantRestrict, inner.last.RestrictFallbacks)
			require.Empty(t, inner.last.AllowedFallbackServiceIDs)
		})
	}
}

func TestFallbackAccessLLMStampsPrefix(t *testing.T) {
	userID := model.NewId()
	primaryID := model.NewId()
	fallbackID := model.NewId()
	fallback2ID := model.NewId()

	tests := []struct {
		name           string
		perID          map[string]*model.AccessDecision
		services       []llm.ServiceConfig
		requestingUser bool
		wantRestrict   bool
		wantIDs        []string
		cycle          bool
	}{
		{
			name: "no requesting user leaves chain unrestricted",
			services: []llm.ServiceConfig{
				openAIService(primaryID, fallbackID),
				openAIService(fallbackID, ""),
			},
		},
		{
			name: "first fallback denied yields empty prefix",
			perID: map[string]*model.AccessDecision{
				fallbackID: abacDeny(),
			},
			services: []llm.ServiceConfig{
				openAIService(primaryID, fallbackID),
				openAIService(fallbackID, fallback2ID),
				openAIService(fallback2ID, ""),
			},
			requestingUser: true,
			wantRestrict:   true,
		},
		{
			name: "second fallback denied keeps first hop",
			perID: map[string]*model.AccessDecision{
				fallback2ID: abacDeny(),
			},
			services: []llm.ServiceConfig{
				openAIService(primaryID, fallbackID),
				openAIService(fallbackID, fallback2ID),
				openAIService(fallback2ID, ""),
			},
			requestingUser: true,
			wantRestrict:   true,
			wantIDs:        []string{fallbackID},
		},
		{
			name: "cycle drops all fallbacks",
			services: []llm.ServiceConfig{
				openAIService(primaryID, fallbackID),
				{ID: fallbackID, Type: llm.ServiceTypeOpenAI, APIKey: "sk-test", FallbackServiceID: primaryID},
			},
			requestingUser: true,
			wantRestrict:   true,
			cycle:          true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupABACTestEnvironment(t, abacStubClient{perID: tc.perID})
			defer e.Cleanup(t)
			e.bots.config = &mockConfig{services: tc.services}
			if tc.cycle {
				e.mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
			}

			inner := &capturingLLM{}
			wrapped := newFallbackAccessLLM(inner, e.bots, primaryID)

			req := llm.CompletionRequest{}
			if tc.requestingUser {
				req.Context = &llm.Context{RequestingUser: &model.User{Id: userID}}
			}

			_, err := wrapped.ChatCompletion(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, tc.wantRestrict, inner.last.RestrictFallbacks)
			require.Equal(t, tc.wantIDs, inner.last.AllowedFallbackServiceIDs)
		})
	}
}
