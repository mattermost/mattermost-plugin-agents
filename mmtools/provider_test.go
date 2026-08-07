// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// TestGetToolsCreateFileCatalog pins that the CreateFile tool is only
// cataloged when the flow can attach created files to the response post.
func TestGetToolsCreateFileCatalog(t *testing.T) {
	responseFilesCtx := func() *llm.Context {
		return &llm.Context{ToolCatalog: llm.ToolCatalogContext{ResponseFilesSupported: true}}
	}

	tests := []struct {
		name       string
		nilClient  bool
		llmContext *llm.Context
		want       bool
	}{
		{name: "cataloged when response files supported", llmContext: responseFilesCtx(), want: true},
		{name: "not cataloged when response files unsupported", llmContext: &llm.Context{}, want: false},
		{name: "not cataloged with nil context", llmContext: nil, want: false},
		{name: "not cataloged with nil client", nilClient: true, llmContext: responseFilesCtx(), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var client mmapi.Client
			if !tt.nilClient {
				client = mocks.NewMockClient(t)
			}
			provider := NewMMToolProvider(client, nil)

			names := []string{}
			for _, tool := range provider.GetTools(nil, tt.llmContext) {
				names = append(names, tool.Name)
			}

			if tt.want {
				require.Contains(t, names, CreateFileToolName)
			} else {
				require.NotContains(t, names, CreateFileToolName)
			}
		})
	}
}

// downloadCapableLLM is a stub LanguageModel that also implements
// llm.ProviderFileDownloader, mirroring *bifrost.LLM's shape.
type downloadCapableLLM struct{ plainLLM }

func (d *downloadCapableLLM) DownloadProviderFile(context.Context, string) ([]byte, string, error) {
	return []byte("x"), "text/plain", nil
}

// plainLLM is a stub LanguageModel without download support.
type plainLLM struct{}

func (plainLLM) ChatCompletion(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (*llm.TextStreamResult, error) {
	return nil, nil
}

func (plainLLM) ChatCompletionNoStream(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (string, error) {
	return "", nil
}

func (plainLLM) CountTokens(context.Context, llm.CompletionRequest, ...llm.LanguageModelOption) (int, error) {
	return 0, llm.ErrUnsupportedTokenCount
}
func (plainLLM) InputTokenLimit() int  { return 0 }
func (plainLLM) OutputTokenLimit() int { return 0 }

// TestGetToolsAttachSandboxFileCatalog pins the cataloging gate: the tool
// appears only for Anthropic agents with the code_interpreter native tool
// enabled, in response-file-capable flows, on a download-capable LLM.
func TestGetToolsAttachSandboxFileCatalog(t *testing.T) {
	responseFilesCtx := func() *llm.Context {
		return &llm.Context{ToolCatalog: llm.ToolCatalogContext{ResponseFilesSupported: true}}
	}
	sandboxBot := func(lm llm.LanguageModel) *bots.Bot {
		return bots.NewBot(
			llm.BotConfig{EnabledNativeTools: []string{llm.NativeToolWebSearch, llm.NativeToolCodeInterpreter}},
			llm.ServiceConfig{Type: llm.ServiceTypeAnthropic},
			&model.Bot{UserId: "bot-id"},
			lm,
		)
	}

	tests := []struct {
		name       string
		bot        *bots.Bot
		llmContext *llm.Context
		want       bool
	}{
		{
			name:       "cataloged for anthropic sandbox bot with download-capable llm",
			bot:        sandboxBot(&downloadCapableLLM{}),
			llmContext: responseFilesCtx(),
			want:       true,
		},
		{
			name:       "not cataloged without download-capable llm",
			bot:        sandboxBot(plainLLM{}),
			llmContext: responseFilesCtx(),
			want:       false,
		},
		{
			name: "not cataloged without code_interpreter enabled",
			bot: bots.NewBot(
				llm.BotConfig{EnabledNativeTools: []string{llm.NativeToolWebSearch}},
				llm.ServiceConfig{Type: llm.ServiceTypeAnthropic},
				&model.Bot{UserId: "bot-id"},
				&downloadCapableLLM{},
			),
			llmContext: responseFilesCtx(),
			want:       false,
		},
		{
			name: "not cataloged for non-anthropic service",
			bot: bots.NewBot(
				llm.BotConfig{EnabledNativeTools: []string{llm.NativeToolCodeInterpreter}},
				llm.ServiceConfig{Type: llm.ServiceTypeOpenAI},
				&model.Bot{UserId: "bot-id"},
				&downloadCapableLLM{},
			),
			llmContext: responseFilesCtx(),
			want:       false,
		},
		{
			name:       "not cataloged when response files unsupported",
			bot:        sandboxBot(&downloadCapableLLM{}),
			llmContext: &llm.Context{},
			want:       false,
		},
		{
			name:       "not cataloged with nil bot",
			bot:        nil,
			llmContext: responseFilesCtx(),
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewMMToolProvider(mocks.NewMockClient(t), nil)

			names := []string{}
			for _, tool := range provider.GetTools(tt.bot, tt.llmContext) {
				names = append(names, tool.Name)
			}

			if tt.want {
				require.Contains(t, names, AttachSandboxFileToolName)
			} else {
				require.NotContains(t, names, AttachSandboxFileToolName)
			}
		})
	}
}
