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

func TestStructuredOutputFallbackWrapperFenceStripping(t *testing.T) {
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()
	withSchema := func(cfg *LanguageModelConfig) {
		cfg.JSONOutputFormat = jsonSchema
	}

	tests := []struct {
		name                    string
		response                string
		structuredOutputEnabled bool
		opts                    []LanguageModelOption
		expected                string
	}{
		{
			name:                    "schema requested, structured output disabled: strips fencing",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                `{"name": "test"}`,
		},
		{
			name:                    "schema requested, structured output enabled: untouched",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: true,
			opts:                    []LanguageModelOption{withSchema},
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:                    "no schema, structured output disabled: untouched",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: false,
			opts:                    nil,
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:                    "no schema, structured output enabled: untouched",
			response:                "```json\n{\"name\": \"test\"}\n```",
			structuredOutputEnabled: true,
			opts:                    nil,
			expected:                "```json\n{\"name\": \"test\"}\n```",
		},
		{
			name:                    "no fencing, schema requested, structured output disabled: untouched",
			response:                `{"name": "test"}`,
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			expected:                `{"name": "test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapper := NewStructuredOutputFallbackWrapper(
				&fakeLLMForFallback{response: tt.response},
				tt.structuredOutputEnabled,
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
		name                    string
		structuredOutputEnabled bool
		opts                    []LanguageModelOption
		posts                   []Post
		wantSchemaDownstream    bool
		wantInstruction         bool
	}

	tests := []gatingCase{
		{
			name:                    "enabled with schema: schema forwarded, posts untouched",
			structuredOutputEnabled: true,
			opts:                    []LanguageModelOption{withSchema},
			posts:                   []Post{systemPost, userPost},
			wantSchemaDownstream:    true,
			wantInstruction:         false,
		},
		{
			name:                    "disabled with schema, first post system: schema stripped, instruction appended",
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			posts:                   []Post{systemPost, userPost},
			wantSchemaDownstream:    false,
			wantInstruction:         true,
		},
		{
			name:                    "disabled with schema, no system post: schema stripped, system post prepended",
			structuredOutputEnabled: false,
			opts:                    []LanguageModelOption{withSchema},
			posts:                   []Post{userPost},
			wantSchemaDownstream:    false,
			wantInstruction:         true,
		},
		{
			name:                    "disabled without schema: pass-through",
			structuredOutputEnabled: false,
			opts:                    nil,
			posts:                   []Post{systemPost, userPost},
			wantSchemaDownstream:    false,
			wantInstruction:         false,
		},
		{
			name:                    "enabled without schema: pass-through",
			structuredOutputEnabled: true,
			opts:                    nil,
			posts:                   []Post{systemPost, userPost},
			wantSchemaDownstream:    false,
			wantInstruction:         false,
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
			wrapper := NewStructuredOutputFallbackWrapper(fake, tt.structuredOutputEnabled)

			callerPosts := make([]Post, len(tt.posts))
			copy(callerPosts, tt.posts)
			request := CompletionRequest{Posts: callerPosts}

			_, err := wrapper.ChatCompletionNoStream(context.Background(), request, tt.opts...)
			require.NoError(t, err)
			assertGating(t, tt, fake, callerPosts)
		})

		t.Run("streaming: "+tt.name, func(t *testing.T) {
			fake := &fakeLLMForFallback{}
			wrapper := NewStructuredOutputFallbackWrapper(fake, tt.structuredOutputEnabled)

			callerPosts := make([]Post, len(tt.posts))
			copy(callerPosts, tt.posts)
			request := CompletionRequest{Posts: callerPosts}

			_, err := wrapper.ChatCompletion(context.Background(), request, tt.opts...)
			require.NoError(t, err)
			assertGating(t, tt, fake, callerPosts)
		})
	}
}

func TestStructuredOutputFallbackWrapperDisabledStripsFenceAndSchemaTogether(t *testing.T) {
	jsonSchema := NewJSONSchemaFromStruct[struct {
		Name string `json:"name"`
	}]()
	fake := &fakeLLMForFallback{response: "```json\n{\"name\": \"test\"}\n```"}
	wrapper := NewStructuredOutputFallbackWrapper(fake, false)

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
