// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLLMForFallback struct {
	response       string
	capturedPosts  []Post
	capturedConfig LanguageModelConfig
}

// jsonSchema is the schema every test in this file requests: a single-field
// object, so the marshaled instruction stays small and easy to assert on.
var jsonSchema = NewJSONSchemaFromStruct[struct {
	Name string `json:"name"`
}]()

func (f *fakeLLMForFallback) capture(request CompletionRequest, opts []LanguageModelOption) {
	f.capturedPosts = request.Posts
	var cfg LanguageModelConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	f.capturedConfig = cfg
}

func (f *fakeLLMForFallback) ChatCompletion(_ context.Context, request CompletionRequest, opts ...LanguageModelOption) (*TextStreamResult, error) {
	f.capture(request, opts)
	return nil, nil
}

func (f *fakeLLMForFallback) ChatCompletionNoStream(_ context.Context, request CompletionRequest, opts ...LanguageModelOption) (string, error) {
	f.capture(request, opts)
	return f.response, nil
}

func (f *fakeLLMForFallback) CountTokens(_ context.Context, request CompletionRequest, opts ...LanguageModelOption) (int, error) {
	f.capture(request, opts)
	return 0, ErrUnsupportedTokenCount
}
func (f *fakeLLMForFallback) InputTokenLimit() int  { return 4096 }
func (f *fakeLLMForFallback) OutputTokenLimit() int { return 4096 }

// serviceWithPolicy builds a service carrying the policy, whose default model
// is the one an attempt against it would run.
func serviceWithPolicy(id string, policy StructuredOutputPolicy, model string) ServiceConfig {
	return ServiceConfig{
		ID:                     id,
		Type:                   ServiceTypeOpenAI,
		APIKey:                 "key",
		DefaultModel:           model,
		StructuredOutputPolicy: policy,
	}
}

// resolvedTarget is one question the resolver was asked.
type resolvedTarget struct {
	serviceID string
	model     string
}

// resolverReturning answers every auto lookup with the given verdict and
// records what it was asked about.
func resolverReturning(capable bool, seen *[]resolvedTarget) StructuredOutputCapabilityResolver {
	return func(svc ServiceConfig, model string) bool {
		if seen != nil {
			*seen = append(*seen, resolvedTarget{serviceID: svc.ID, model: model})
		}
		return capable
	}
}

func TestStructuredOutputFallbackWrapperFenceStripping(t *testing.T) {
	withSchema := func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	}

	tests := []struct {
		name     string
		response string
		policy   StructuredOutputPolicy
		opts     []LanguageModelOption
		expected string
	}{
		{
			name:     "schema requested, prompt fallback: strips fencing",
			response: "```json\n{\"name\": \"test\"}\n```",
			policy:   StructuredOutputPolicyPromptFallback,
			opts:     []LanguageModelOption{withSchema},
			expected: `{"name": "test"}`,
		},
		{
			name:     "schema requested, native policy: untouched",
			response: "```json\n{\"name\": \"test\"}\n```",
			policy:   StructuredOutputPolicyNative,
			opts:     []LanguageModelOption{withSchema},
			expected: "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:     "no schema, prompt fallback: untouched",
			response: "```json\n{\"name\": \"test\"}\n```",
			policy:   StructuredOutputPolicyPromptFallback,
			opts:     nil,
			expected: "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:     "no schema, native policy: untouched",
			response: "```json\n{\"name\": \"test\"}\n```",
			policy:   StructuredOutputPolicyNative,
			opts:     nil,
			expected: "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:     "no fencing, schema requested, prompt fallback: untouched",
			response: `{"name": "test"}`,
			policy:   StructuredOutputPolicyPromptFallback,
			opts:     []LanguageModelOption{withSchema},
			expected: `{"name": "test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapper := NewStructuredOutputFallbackWrapper(
				&fakeLLMForFallback{response: tt.response},
				NewNativeStructuredOutputDecision(serviceWithPolicy("primary", tt.policy, "gpt-4o"), "gpt-4o", nil, nil),
			)
			result, err := wrapper.ChatCompletionNoStream(context.Background(), CompletionRequest{}, tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStructuredOutputFallbackWrapperSchemaGating(t *testing.T) {
	withSchema := func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	}

	systemPost := Post{Role: PostRoleSystem, Message: "You are a helpful assistant."}
	userPost := Post{Role: PostRoleUser, Message: "Summarize the channel."}

	type gatingCase struct {
		name                 string
		primary              ServiceConfig
		fallbacks            []ServiceConfig
		resolver             StructuredOutputCapabilityResolver
		opts                 []LanguageModelOption
		posts                []Post
		wantSchemaDownstream bool
		wantInstruction      bool
	}

	tests := []gatingCase{
		{
			name:                 "native policy with schema: schema forwarded, posts untouched",
			primary:              serviceWithPolicy("primary", StructuredOutputPolicyNative, "gpt-4o"),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: true,
			wantInstruction:      false,
		},
		{
			name:                 "prompt fallback with schema, first post system: schema stripped, instruction appended",
			primary:              serviceWithPolicy("primary", StructuredOutputPolicyPromptFallback, "gpt-4o"),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:                 "prompt fallback with schema, no system post: schema stripped, system post prepended",
			primary:              serviceWithPolicy("primary", StructuredOutputPolicyPromptFallback, "gpt-4o"),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:                 "prompt fallback without schema: pass-through",
			primary:              serviceWithPolicy("primary", StructuredOutputPolicyPromptFallback, "gpt-4o"),
			opts:                 nil,
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      false,
		},
		{
			name:                 "native policy without schema: pass-through",
			primary:              serviceWithPolicy("primary", StructuredOutputPolicyNative, "gpt-4o"),
			opts:                 nil,
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      false,
		},
		{
			name:                 "auto policy resolved supported: schema forwarded",
			primary:              serviceWithPolicy("primary", StructuredOutputPolicyAuto, "gpt-4o"),
			resolver:             resolverReturning(true, nil),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: true,
			wantInstruction:      false,
		},
		{
			name:                 "auto policy resolved not capable: prompt fallback",
			primary:              serviceWithPolicy("primary", StructuredOutputPolicyAuto, "mystery-model"),
			resolver:             resolverReturning(false, nil),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:                 "auto policy without resolver: prompt fallback",
			primary:              serviceWithPolicy("primary", StructuredOutputPolicyAuto, "gpt-4o"),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:                 "invalid stored policy: prompt fallback",
			primary:              serviceWithPolicy("primary", StructuredOutputPolicy("sometimes"), "gpt-4o"),
			resolver:             resolverReturning(true, nil),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:    "native primary with prompt-fallback fallback: whole chain uses prompt fallback",
			primary: serviceWithPolicy("primary", StructuredOutputPolicyNative, "gpt-4o"),
			fallbacks: []ServiceConfig{
				serviceWithPolicy("backup", StructuredOutputPolicyPromptFallback, "local-model"),
			},
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:    "native primary with native fallbacks: schema forwarded",
			primary: serviceWithPolicy("primary", StructuredOutputPolicyNative, "gpt-4o"),
			fallbacks: []ServiceConfig{
				serviceWithPolicy("backup", StructuredOutputPolicyNative, "gpt-4.1"),
			},
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: true,
			wantInstruction:      false,
		},
	}

	assertGating := func(t *testing.T, tt gatingCase, fake *fakeLLMForFallback, callerPosts []Post) {
		t.Helper()

		assert.Equal(t, tt.posts, callerPosts, "caller's posts must not be mutated")

		if tt.wantSchemaDownstream {
			assert.Equal(t, jsonSchema, fake.capturedConfig.JSONOutputFormat, "schema should reach the wrapped model")
		} else {
			assert.Nil(t, fake.capturedConfig.JSONOutputFormat, "schema should not reach the wrapped model")
		}

		if !tt.wantInstruction {
			assert.Equal(t, tt.posts, fake.capturedPosts, "posts should pass through unmodified")
			return
		}

		firstPostWasSystem := len(tt.posts) > 0 && tt.posts[0].Role == PostRoleSystem
		if firstPostWasSystem {
			require.Len(t, fake.capturedPosts, len(tt.posts))
			assert.Contains(t, fake.capturedPosts[0].Message, tt.posts[0].Message, "original system message should be preserved")
		} else {
			require.Len(t, fake.capturedPosts, len(tt.posts)+1)
			assert.Equal(t, tt.posts, fake.capturedPosts[1:], "original posts should follow the injected system post")
		}
		require.Equal(t, PostRoleSystem, fake.capturedPosts[0].Role)
		assert.Contains(t, fake.capturedPosts[0].Message, "start with { and end with }", "instruction should demand raw JSON")
		assert.Contains(t, fake.capturedPosts[0].Message, `"name"`, "instruction should contain the marshaled schema")
	}

	decisionFor := func(tt gatingCase) func(string) bool {
		return NewNativeStructuredOutputDecision(tt.primary, tt.primary.DefaultModel, tt.fallbacks, tt.resolver)
	}

	for _, tt := range tests {
		t.Run("no stream: "+tt.name, func(t *testing.T) {
			fake := &fakeLLMForFallback{response: `{"name": "test"}`}
			wrapper := NewStructuredOutputFallbackWrapper(fake, decisionFor(tt))

			callerPosts := make([]Post, len(tt.posts))
			copy(callerPosts, tt.posts)
			request := CompletionRequest{Posts: callerPosts}

			_, err := wrapper.ChatCompletionNoStream(context.Background(), request, tt.opts...)
			require.NoError(t, err)
			assertGating(t, tt, fake, callerPosts)
		})

		t.Run("streaming: "+tt.name, func(t *testing.T) {
			fake := &fakeLLMForFallback{}
			wrapper := NewStructuredOutputFallbackWrapper(fake, decisionFor(tt))

			callerPosts := make([]Post, len(tt.posts))
			copy(callerPosts, tt.posts)
			request := CompletionRequest{Posts: callerPosts}

			_, err := wrapper.ChatCompletion(context.Background(), request, tt.opts...)
			require.NoError(t, err)
			assertGating(t, tt, fake, callerPosts)
		})
	}
}

func TestStructuredOutputFallbackWrapperResolvesEveryTarget(t *testing.T) {
	primary := serviceWithPolicy("primary", StructuredOutputPolicyAuto, "primary-model")
	fallbacks := []ServiceConfig{
		serviceWithPolicy("backup-1", StructuredOutputPolicyAuto, "backup-1-model"),
		serviceWithPolicy("backup-2", StructuredOutputPolicyAuto, "backup-2-model"),
	}

	tests := []struct {
		name             string
		opts             []LanguageModelOption
		wantPrimaryModel string
	}{
		{
			name:             "uses the target model when no per-call override",
			wantPrimaryModel: "primary-model",
		},
		{
			name:             "per-call model override wins for the primary",
			opts:             []LanguageModelOption{WithModel("override-model")},
			wantPrimaryModel: "override-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen []resolvedTarget
			fake := &fakeLLMForFallback{response: `{"name": "test"}`}
			wrapper := NewStructuredOutputFallbackWrapper(fake, NewNativeStructuredOutputDecision(
				primary, primary.DefaultModel, fallbacks, resolverReturning(true, &seen)))

			opts := append([]LanguageModelOption{func(cfg *LanguageModelConfig) {
				cfg.JSONOutputFormat = jsonSchema
			}}, tt.opts...)

			_, err := wrapper.ChatCompletionNoStream(context.Background(), CompletionRequest{}, opts...)
			require.NoError(t, err)

			// The fallbacks are resolved once when the decision is built; the
			// primary is resolved per call, because only its model can change.
			assert.Equal(t, []resolvedTarget{
				{serviceID: "backup-1", model: "backup-1-model"},
				{serviceID: "backup-2", model: "backup-2-model"},
				{serviceID: "primary", model: tt.wantPrimaryModel},
			}, seen, "every possible provider attempt must be resolved")
		})
	}
}

func TestStructuredOutputFallbackWrapperCountTokensAppliesTransformation(t *testing.T) {
	fake := &fakeLLMForFallback{}
	wrapper := NewStructuredOutputFallbackWrapper(fake, NewNativeStructuredOutputDecision(
		serviceWithPolicy("primary", StructuredOutputPolicyPromptFallback, "gpt-4o"), "gpt-4o", nil, nil))

	_, err := wrapper.CountTokens(context.Background(), CompletionRequest{
		Posts: []Post{{Role: PostRoleUser, Message: "Summarize the channel."}},
	}, func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	})
	require.ErrorIs(t, err, ErrUnsupportedTokenCount)

	// The count must reflect the request actually sent: schema stripped and
	// the prompt instruction injected as a leading system post.
	assert.Nil(t, fake.capturedConfig.JSONOutputFormat, "schema must be stripped before the token count")
	require.Len(t, fake.capturedPosts, 2)
	assert.Equal(t, PostRoleSystem, fake.capturedPosts[0].Role, "the injected prompt instruction must be counted")
	assert.Equal(t, PostRoleUser, fake.capturedPosts[1].Role)

	assert.Equal(t, 4096, wrapper.InputTokenLimit())
	assert.Equal(t, 4096, wrapper.OutputTokenLimit())
}

func TestStructuredOutputFallbackWrapperPromptFallbackStripsFenceAndSchemaTogether(t *testing.T) {
	fake := &fakeLLMForFallback{response: "```json\n{\"name\": \"test\"}\n```"}
	wrapper := NewStructuredOutputFallbackWrapper(fake, NewNativeStructuredOutputDecision(
		serviceWithPolicy("primary", StructuredOutputPolicyPromptFallback, "gpt-4o"), "gpt-4o", nil, nil))

	request := CompletionRequest{Posts: []Post{
		{Role: PostRoleSystem, Message: "You are a helpful assistant."},
		{Role: PostRoleUser, Message: "Summarize the channel."},
	}}

	result, err := wrapper.ChatCompletionNoStream(context.Background(), request, func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	})
	require.NoError(t, err)

	assert.Equal(t, `{"name": "test"}`, result, "fenced response should still be stripped")
	assert.Nil(t, fake.capturedConfig.JSONOutputFormat, "schema should not reach the wrapped model")
}
