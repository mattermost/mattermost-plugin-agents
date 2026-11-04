// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"

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
			e.bots.SetBaseLLMBuilderForTest(func(llm.ServiceConfig, []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
				return inner, func() {}, nil
			})

			built := buildTestLLM(t, e.bots, primary, tc.botConfig, []llm.ServiceConfig{fallback})

			_, err := built.ChatCompletion(context.Background(), llm.CompletionRequest{
				Posts:   []llm.Post{{Role: llm.PostRoleUser, Message: "hi"}},
				Context: &llm.Context{RequestingUser: &model.User{Id: userID}},
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantRestrict, inner.last.RestrictFallbacks)
			require.Empty(t, inner.last.AllowedFallbackServiceIDs)
		})
	}
}

// TestBuildLLMDescribesReachableFallbackModels pins that an agent's system
// prompt names only the fallback models this request can actually reach, and
// never the services' names, URLs, or credentials.
func TestBuildLLMDescribesReachableFallbackModels(t *testing.T) {
	const systemPrompt = "You are a helpful assistant."
	userID := model.NewId()
	primaryID := model.NewId()
	fallbackID := model.NewId()
	fallback2ID := model.NewId()

	service := func(id, fallbackID, name, defaultModel string) llm.ServiceConfig {
		svc := openAIService(id, fallbackID)
		svc.Name = name
		svc.DefaultModel = defaultModel
		svc.APIURL = "https://" + name + ".internal.example.com/v1"
		svc.APIKey = "sk-secret-" + name
		return svc
	}
	chain := []llm.ServiceConfig{
		service(primaryID, fallbackID, "primary-svc", "gpt-primary"),
		service(fallbackID, fallback2ID, "first-fallback-svc", "fb-model-one"),
		service(fallback2ID, "", "second-fallback-svc", "fb-model-two"),
	}
	systemPost := llm.Post{Role: llm.PostRoleSystem, Message: systemPrompt}
	userPost := llm.Post{Role: llm.PostRoleUser, Message: "which model are you?"}

	tests := []struct {
		name           string
		belowAdvanced  bool
		perID          map[string]*model.AccessDecision
		services       []llm.ServiceConfig
		botModel       string
		noUser         bool
		noSystemPost   bool
		cycle          bool
		wantFallbacks  []string
		wantNoFallback bool
	}{
		{
			name:          "every hop allowed lists the whole chain",
			services:      chain,
			wantFallbacks: []string{"fb-model-one", "fb-model-two"},
		},
		{
			name:          "second hop denied lists only the first",
			perID:         map[string]*model.AccessDecision{fallback2ID: abacDeny()},
			services:      chain,
			wantFallbacks: []string{"fb-model-one"},
		},
		{
			name:           "first hop denied lists nothing",
			perID:          map[string]*model.AccessDecision{fallbackID: abacDeny()},
			services:       chain,
			wantNoFallback: true,
		},
		{
			name:           "license without fallback chains lists nothing",
			belowAdvanced:  true,
			services:       chain,
			wantNoFallback: true,
		},
		{
			name: "fallback on the agent's own model is not listed",
			services: []llm.ServiceConfig{
				chain[0],
				service(fallbackID, fallback2ID, "first-fallback-svc", "agent-model"),
				service(fallback2ID, "", "second-fallback-svc", "fb-model-two"),
			},
			botModel:      "agent-model",
			wantFallbacks: []string{"fb-model-two"},
		},
		{
			name: "fallbacks on the same model are listed once",
			services: []llm.ServiceConfig{
				chain[0],
				service(fallbackID, fallback2ID, "first-fallback-svc", "fb-model-one"),
				service(fallback2ID, "", "second-fallback-svc", "fb-model-one"),
			},
			wantFallbacks: []string{"fb-model-one"},
		},
		{
			name:          "request without a requesting user lists the whole chain",
			services:      chain,
			noUser:        true,
			wantFallbacks: []string{"fb-model-one", "fb-model-two"},
		},
		{
			name: "chain that no longer resolves lists nothing",
			services: []llm.ServiceConfig{
				chain[0],
				service(fallbackID, primaryID, "first-fallback-svc", "fb-model-one"),
			},
			cycle:          true,
			wantNoFallback: true,
		},
		{
			name:           "request without a system post is unchanged",
			services:       chain,
			noSystemPost:   true,
			wantNoFallback: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			level := enterprise.LevelEnterpriseAdvanced
			if tc.belowAdvanced {
				level = enterprise.LevelEnterprise
			}
			e := setupABACTestEnvironmentAtLevel(t, abacStubClient{perID: tc.perID}, level)
			defer e.Cleanup(t)
			if tc.cycle {
				e.mockAPI.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
			}
			e.bots.config = &mockConfig{services: tc.services}

			inner := &capturingLLM{}
			e.bots.SetBaseLLMBuilderForTest(func(llm.ServiceConfig, []llm.ServiceConfig) (llm.LanguageModel, func(), error) {
				return inner, func() {}, nil
			})
			// A cycle cannot be built, so that case models a config change
			// after the client was built with the original chain.
			builtWith := tc.services[1:]
			if tc.cycle {
				builtWith = chain[1:]
			}
			built := buildTestLLM(t, e.bots, tc.services[0], &llm.BotConfig{Name: "agent", Model: tc.botModel}, builtWith)

			posts := []llm.Post{systemPost, userPost}
			if tc.noSystemPost {
				posts = []llm.Post{userPost}
			}
			sent := slices.Clone(posts)
			req := llm.CompletionRequest{Posts: posts, Context: &llm.Context{}}
			if !tc.noUser {
				req.Context.RequestingUser = &model.User{Id: userID}
			}

			_, err := built.ChatCompletion(context.Background(), req)
			require.NoError(t, err)

			require.Equal(t, sent, posts, "the caller's posts must not be modified")
			got := inner.last.Posts
			require.Len(t, got, len(posts))
			require.Equal(t, userPost, got[len(got)-1])
			if tc.noSystemPost {
				return
			}

			if tc.wantNoFallback {
				require.Equal(t, systemPrompt, got[0].Message)
				return
			}
			require.Equal(t, systemPrompt+"\n\n"+fmt.Sprintf(fallbackModelsNote, strings.Join(tc.wantFallbacks, ", ")), got[0].Message)
			for _, svc := range tc.services {
				require.NotContains(t, got[0].Message, svc.Name)
				require.NotContains(t, got[0].Message, svc.APIURL)
				require.NotContains(t, got[0].Message, svc.APIKey)
			}
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
			wrapped := newFallbackAccessLLM(inner, e.bots, primaryID, "", nil)

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
