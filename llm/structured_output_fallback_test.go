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

func (f *fakeLLMForFallback) CountTokens(_ context.Context, _ CompletionRequest, _ ...LanguageModelOption) (int, error) {
	return 0, ErrUnsupportedTokenCount
}
func (f *fakeLLMForFallback) InputTokenLimit() int  { return 4096 }
func (f *fakeLLMForFallback) OutputTokenLimit() int { return 4096 }

// fallbackTargetWithPolicy builds a single-service target carrying the policy.
func fallbackTargetWithPolicy(id string, policy StructuredOutputPolicy, model string) StructuredOutputTarget {
	return StructuredOutputTarget{
		Service: ServiceConfig{
			ID:                     id,
			Type:                   ServiceTypeOpenAI,
			APIKey:                 "key",
			DefaultModel:           model,
			StructuredOutputPolicy: policy,
		},
		Model: model,
	}
}

// resolverReturning answers every auto lookup with the given capability and
// records what it was asked about.
func resolverReturning(capability StructuredOutputCapability, seen *[]StructuredOutputTarget) StructuredOutputCapabilityResolver {
	return func(svc ServiceConfig, model string) StructuredOutputCapability {
		if seen != nil {
			*seen = append(*seen, StructuredOutputTarget{Service: svc, Model: model})
		}
		return capability
	}
}

func TestStructuredOutputFallbackWrapperFenceStripping(t *testing.T) {
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()
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
				fallbackTargetWithPolicy("primary", tt.policy, "gpt-4o"),
				nil,
				nil,
			)
			result, err := wrapper.ChatCompletionNoStream(context.Background(), CompletionRequest{}, tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStructuredOutputFallbackWrapperSchemaGating(t *testing.T) {
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()
	withSchema := func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	}

	systemPost := Post{Role: PostRoleSystem, Message: "You are a helpful assistant."}
	userPost := Post{Role: PostRoleUser, Message: "Summarize the channel."}

	type gatingCase struct {
		name                 string
		primary              StructuredOutputTarget
		fallbacks            []StructuredOutputTarget
		resolver             StructuredOutputCapabilityResolver
		opts                 []LanguageModelOption
		posts                []Post
		wantSchemaDownstream bool
		wantInstruction      bool
	}

	tests := []gatingCase{
		{
			name:                 "native policy with schema: schema forwarded, posts untouched",
			primary:              fallbackTargetWithPolicy("primary", StructuredOutputPolicyNative, "gpt-4o"),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: true,
			wantInstruction:      false,
		},
		{
			name:                 "prompt fallback with schema, first post system: schema stripped, instruction appended",
			primary:              fallbackTargetWithPolicy("primary", StructuredOutputPolicyPromptFallback, "gpt-4o"),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:                 "prompt fallback with schema, no system post: schema stripped, system post prepended",
			primary:              fallbackTargetWithPolicy("primary", StructuredOutputPolicyPromptFallback, "gpt-4o"),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:                 "prompt fallback without schema: pass-through",
			primary:              fallbackTargetWithPolicy("primary", StructuredOutputPolicyPromptFallback, "gpt-4o"),
			opts:                 nil,
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      false,
		},
		{
			name:                 "native policy without schema: pass-through",
			primary:              fallbackTargetWithPolicy("primary", StructuredOutputPolicyNative, "gpt-4o"),
			opts:                 nil,
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      false,
		},
		{
			name:                 "auto policy resolved supported: schema forwarded",
			primary:              fallbackTargetWithPolicy("primary", StructuredOutputPolicyAuto, "gpt-4o"),
			resolver:             resolverReturning(StructuredOutputCapabilitySupported, nil),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: true,
			wantInstruction:      false,
		},
		{
			name:                 "auto policy resolved unknown: prompt fallback",
			primary:              fallbackTargetWithPolicy("primary", StructuredOutputPolicyAuto, "mystery-model"),
			resolver:             resolverReturning(StructuredOutputCapabilityUnknown, nil),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:                 "auto policy resolved unsupported: prompt fallback",
			primary:              fallbackTargetWithPolicy("primary", StructuredOutputPolicyAuto, "scaled-model"),
			resolver:             resolverReturning(StructuredOutputCapabilityUnsupported, nil),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:                 "auto policy without resolver: prompt fallback",
			primary:              fallbackTargetWithPolicy("primary", StructuredOutputPolicyAuto, "gpt-4o"),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:                 "invalid stored policy: prompt fallback",
			primary:              fallbackTargetWithPolicy("primary", StructuredOutputPolicy("sometimes"), "gpt-4o"),
			resolver:             resolverReturning(StructuredOutputCapabilitySupported, nil),
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:    "native primary with prompt-fallback fallback: whole chain uses prompt fallback",
			primary: fallbackTargetWithPolicy("primary", StructuredOutputPolicyNative, "gpt-4o"),
			fallbacks: []StructuredOutputTarget{
				fallbackTargetWithPolicy("backup", StructuredOutputPolicyPromptFallback, "local-model"),
			},
			opts:                 []LanguageModelOption{withSchema},
			posts:                []Post{systemPost, userPost},
			wantSchemaDownstream: false,
			wantInstruction:      true,
		},
		{
			name:    "native primary with native fallbacks: schema forwarded",
			primary: fallbackTargetWithPolicy("primary", StructuredOutputPolicyNative, "gpt-4o"),
			fallbacks: []StructuredOutputTarget{
				fallbackTargetWithPolicy("backup", StructuredOutputPolicyNative, "gpt-4.1"),
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

	for _, tt := range tests {
		t.Run("no stream: "+tt.name, func(t *testing.T) {
			fake := &fakeLLMForFallback{response: `{"name": "test"}`}
			wrapper := NewStructuredOutputFallbackWrapper(fake, tt.primary, tt.fallbacks, tt.resolver)

			callerPosts := make([]Post, len(tt.posts))
			copy(callerPosts, tt.posts)
			request := CompletionRequest{Posts: callerPosts}

			_, err := wrapper.ChatCompletionNoStream(context.Background(), request, tt.opts...)
			require.NoError(t, err)
			assertGating(t, tt, fake, callerPosts)
		})

		t.Run("streaming: "+tt.name, func(t *testing.T) {
			fake := &fakeLLMForFallback{}
			wrapper := NewStructuredOutputFallbackWrapper(fake, tt.primary, tt.fallbacks, tt.resolver)

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
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()

	primary := fallbackTargetWithPolicy("primary", StructuredOutputPolicyAuto, "primary-model")
	fallbacks := []StructuredOutputTarget{
		fallbackTargetWithPolicy("backup-1", StructuredOutputPolicyAuto, "backup-1-model"),
		fallbackTargetWithPolicy("backup-2", StructuredOutputPolicyAuto, "backup-2-model"),
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
			var seen []StructuredOutputTarget
			fake := &fakeLLMForFallback{response: `{"name": "test"}`}
			wrapper := NewStructuredOutputFallbackWrapper(fake, primary, fallbacks,
				resolverReturning(StructuredOutputCapabilitySupported, &seen))

			opts := append([]LanguageModelOption{func(cfg *LanguageModelConfig) {
				cfg.JSONOutputFormat = jsonSchema
			}}, tt.opts...)

			_, err := wrapper.ChatCompletionNoStream(context.Background(), CompletionRequest{}, opts...)
			require.NoError(t, err)

			require.Len(t, seen, 3, "every possible provider attempt must be resolved")
			assert.Equal(t, "primary", seen[0].Service.ID)
			assert.Equal(t, tt.wantPrimaryModel, seen[0].Model)
			assert.Equal(t, "backup-1", seen[1].Service.ID)
			assert.Equal(t, "backup-1-model", seen[1].Model, "a fallback always runs its own model")
			assert.Equal(t, "backup-2", seen[2].Service.ID)
			assert.Equal(t, "backup-2-model", seen[2].Model)
		})
	}
}

func TestStructuredOutputFallbackWrapperMixedChain(t *testing.T) {
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()

	capabilities := map[string]StructuredOutputCapability{
		"primary": StructuredOutputCapabilitySupported,
		"backup":  StructuredOutputCapabilityUnknown,
	}
	resolver := func(svc ServiceConfig, _ string) StructuredOutputCapability {
		return capabilities[svc.ID]
	}

	fake := &fakeLLMForFallback{response: `{"name": "test"}`}
	wrapper := NewStructuredOutputFallbackWrapper(fake,
		fallbackTargetWithPolicy("primary", StructuredOutputPolicyAuto, "gpt-4o"),
		[]StructuredOutputTarget{fallbackTargetWithPolicy("backup", StructuredOutputPolicyAuto, "mystery")},
		resolver,
	)

	_, err := wrapper.ChatCompletionNoStream(context.Background(), CompletionRequest{}, func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	})
	require.NoError(t, err)
	assert.Nil(t, fake.capturedConfig.JSONOutputFormat, "an incapable fallback must keep the whole chain on prompt fallback")
}

func TestStructuredOutputFallbackWrapperCountTokensAppliesTransformation(t *testing.T) {
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()

	fake := &fakeLLMForFallback{}
	wrapper := NewStructuredOutputFallbackWrapper(fake,
		fallbackTargetWithPolicy("primary", StructuredOutputPolicyPromptFallback, "gpt-4o"), nil, nil)

	_, err := wrapper.CountTokens(context.Background(), CompletionRequest{
		Posts: []Post{{Role: PostRoleUser, Message: "Summarize the channel."}},
	}, func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	})
	require.ErrorIs(t, err, ErrUnsupportedTokenCount)

	assert.Equal(t, 4096, wrapper.InputTokenLimit())
	assert.Equal(t, 4096, wrapper.OutputTokenLimit())
}

func TestStructuredOutputFallbackWrapperPromptFallbackStripsFenceAndSchemaTogether(t *testing.T) {
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()
	fake := &fakeLLMForFallback{response: "```json\n{\"name\": \"test\"}\n```"}
	wrapper := NewStructuredOutputFallbackWrapper(fake,
		fallbackTargetWithPolicy("primary", StructuredOutputPolicyPromptFallback, "gpt-4o"), nil, nil)

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
