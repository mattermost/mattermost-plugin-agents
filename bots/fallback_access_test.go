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
