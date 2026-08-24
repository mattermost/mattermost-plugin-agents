// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
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
			provider := NewMMToolProvider(client, nil, nil)

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

// TestGetToolsAskAnotherUserToggle pins the V2-C1 master gate: AskAnotherUser
// is cataloged only when the admin toggle is on, failing closed on a nil
// config getter or nil config, and the gate never disturbs the rest of the
// catalog.
func TestGetToolsAskAnotherUserToggle(t *testing.T) {
	interactiveCtx := &llm.Context{ToolCatalog: llm.ToolCatalogContext{InteractiveUserPresent: true}}

	tests := []struct {
		name      string
		cfgGetter func() *config.Config
		want      bool
	}{
		{
			name:      "toggle on catalogs the tool",
			cfgGetter: func() *config.Config { return &config.Config{EnableAskAnotherUser: true} },
			want:      true,
		},
		{
			name:      "toggle off hides the tool",
			cfgGetter: func() *config.Config { return &config.Config{EnableAskAnotherUser: false} },
			want:      false,
		},
		{
			name:      "nil config getter fails closed",
			cfgGetter: nil,
			want:      false,
		},
		{
			name:      "nil config from the getter fails closed",
			cfgGetter: func() *config.Config { return nil },
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewMMToolProvider(nil, nil, tt.cfgGetter)

			names := []string{}
			for _, tool := range provider.GetTools(nil, interactiveCtx) {
				names = append(names, tool.Name)
			}

			if tt.want {
				require.Contains(t, names, AskAnotherUserToolName)
			} else {
				require.NotContains(t, names, AskAnotherUserToolName)
			}
			// The master gate must not disturb the rest of the catalog:
			// the interactive context always yields AskUserQuestion.
			require.Contains(t, names, AskUserQuestionToolName)
		})
	}
}
