// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

func TestConvertToResponsesToolsSkipNativeWebSearch(t *testing.T) {
	hasWebSearch := func(tools []schemas.ResponsesTool) bool {
		for _, tool := range tools {
			if tool.Type == schemas.ResponsesToolTypeWebSearch {
				return true
			}
		}
		return false
	}

	t.Run("configured native web search is omitted when skipped", func(t *testing.T) {
		nativeTools := []string{llm.NativeToolWebSearch}
		request := llm.CompletionRequest{Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hello"}}}

		require.True(t, hasWebSearch(convertToResponsesTools(request, llm.LanguageModelConfig{}, nativeTools)))
		require.False(t, hasWebSearch(convertToResponsesTools(request, llm.LanguageModelConfig{SkipNativeWebSearch: true}, nativeTools)))
	})

	t.Run("NativeWebSearchAllowed does not attach web search when skipped", func(t *testing.T) {
		request := llm.CompletionRequest{Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hello"}}}

		require.True(t, hasWebSearch(convertToResponsesTools(request, llm.LanguageModelConfig{NativeWebSearchAllowed: true}, nil)))
		require.False(t, hasWebSearch(convertToResponsesTools(request, llm.LanguageModelConfig{
			NativeWebSearchAllowed: true,
			SkipNativeWebSearch:    true,
		}, nil)))
	})
}

func TestSkipNativeToolsOmitsEveryNativeTool(t *testing.T) {
	nativeTools := []string{llm.NativeToolWebSearch, llm.NativeToolWebFetch, llm.NativeToolCodeInterpreter}
	request := llm.CompletionRequest{Posts: []llm.Post{{Role: llm.PostRoleUser, Message: "hello"}}}

	require.Len(t, convertToResponsesTools(request, llm.LanguageModelConfig{}, nativeTools), 3)
	require.Empty(t, convertToResponsesTools(request, llm.LanguageModelConfig{SkipNativeTools: true}, nativeTools))
	require.Empty(t, convertToResponsesTools(request, llm.LanguageModelConfig{SkipNativeTools: true, NativeWebSearchAllowed: true}, nil))
}
