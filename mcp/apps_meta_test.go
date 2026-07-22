// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/require"
)

func TestParseToolUIMeta(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
		want *llm.ToolUIMeta
	}{
		{
			name: "nil meta",
			meta: nil,
			want: nil,
		},
		{
			name: "empty meta",
			meta: map[string]any{},
			want: nil,
		},
		{
			name: "nested only",
			meta: map[string]any{
				"ui": map[string]any{"resourceUri": "ui://a/b"},
			},
			want: &llm.ToolUIMeta{ResourceURI: "ui://a/b"},
		},
		{
			name: "nested with visibility",
			meta: map[string]any{
				"ui": map[string]any{
					"resourceUri": "ui://a/b",
					"visibility":  []any{"model", "app"},
				},
			},
			want: &llm.ToolUIMeta{ResourceURI: "ui://a/b", Visibility: []string{"model", "app"}},
		},
		{
			name: "app-only visibility, no resourceUri",
			meta: map[string]any{
				"ui": map[string]any{"visibility": []any{"app"}},
			},
			want: &llm.ToolUIMeta{Visibility: []string{"app"}},
		},
		{
			name: "legacy flat only",
			meta: map[string]any{
				"ui/resourceUri": "ui://a/b",
			},
			want: &llm.ToolUIMeta{ResourceURI: "ui://a/b"},
		},
		{
			name: "both forms, nested wins",
			meta: map[string]any{
				"ui":             map[string]any{"resourceUri": "ui://nested"},
				"ui/resourceUri": "ui://flat",
			},
			want: &llm.ToolUIMeta{ResourceURI: "ui://nested"},
		},
		{
			name: "ui not a map",
			meta: map[string]any{"ui": "junk"},
			want: nil,
		},
		{
			name: "resourceUri not a string",
			meta: map[string]any{
				"ui": map[string]any{"resourceUri": 42},
			},
			want: nil,
		},
		{
			name: "visibility mixed types",
			meta: map[string]any{
				"ui": map[string]any{
					"visibility": []any{"model", 7},
				},
			},
			want: &llm.ToolUIMeta{Visibility: []string{"model"}},
		},
		{
			name: "unrelated meta keys",
			meta: map[string]any{"other": "x"},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseToolUIMeta(tt.meta)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestToolUIMetaVisibleToModel(t *testing.T) {
	tests := []struct {
		name       string
		visibility []string
		metaNil    bool
		want       bool
	}{
		{name: "nil", metaNil: true, want: true},
		{name: "empty", visibility: nil, want: true},
		{name: "model only", visibility: []string{"model"}, want: true},
		{name: "app only", visibility: []string{"app"}, want: false},
		{name: "model and app", visibility: []string{"model", "app"}, want: true},
		{name: "app and model", visibility: []string{"app", "model"}, want: true},
		{name: "bogus", visibility: []string{"bogus"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m *llm.ToolUIMeta
			if !tt.metaNil {
				m = &llm.ToolUIMeta{Visibility: tt.visibility}
			}
			require.Equal(t, tt.want, m.VisibleToModel())
		})
	}
}

func ptrBool(b bool) *bool { return &b }

func TestParseResourceUIMeta(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
		want *AppResourceUIMeta
	}{
		{
			name: "nil",
			meta: nil,
			want: nil,
		},
		{
			name: "no ui",
			meta: map[string]any{"x": 1},
			want: nil,
		},
		{
			name: "full",
			meta: map[string]any{
				"ui": map[string]any{
					"csp": map[string]any{
						"connectDomains":  []any{"https://a"},
						"resourceDomains": []any{"https://b"},
						"frameDomains":    []any{"https://c"},
						"baseUriDomains":  []any{"https://d"},
					},
					"permissions":   map[string]any{"camera": map[string]any{}},
					"domain":        "example.com",
					"prefersBorder": true,
				},
			},
			want: &AppResourceUIMeta{
				CSP: &AppResourceCSP{
					ConnectDomains:  []string{"https://a"},
					ResourceDomains: []string{"https://b"},
					FrameDomains:    []string{"https://c"},
					BaseURIDomains:  []string{"https://d"},
				},
				Permissions:   map[string]map[string]any{"camera": {}},
				Domain:        "example.com",
				PrefersBorder: ptrBool(true),
			},
		},
		{
			name: "csp only, partial",
			meta: map[string]any{
				"ui": map[string]any{
					"csp": map[string]any{
						"connectDomains": []any{"https://a"},
					},
				},
			},
			want: &AppResourceUIMeta{
				CSP: &AppResourceCSP{ConnectDomains: []string{"https://a"}},
			},
		},
		{
			name: "prefersBorder false",
			meta: map[string]any{
				"ui": map[string]any{"prefersBorder": false},
			},
			want: &AppResourceUIMeta{PrefersBorder: ptrBool(false)},
		},
		{
			name: "malformed csp ignored, prefersBorder kept",
			meta: map[string]any{
				"ui": map[string]any{
					"csp":           "junk",
					"prefersBorder": true,
				},
			},
			want: &AppResourceUIMeta{PrefersBorder: ptrBool(true)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseResourceUIMeta(tt.meta)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsUIResourceMIMEType(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		want     bool
	}{
		{name: "exact", mimeType: "text/html;profile=mcp-app", want: true},
		{name: "space after semicolon", mimeType: "text/html; profile=mcp-app", want: true},
		{name: "type and param name case", mimeType: "TEXT/HTML;PROFILE=mcp-app", want: true},
		// Parameter values are case-sensitive per RFC 2045.
		{name: "param value case", mimeType: "text/html;profile=MCP-APP", want: false},
		{name: "no profile", mimeType: "text/html", want: false},
		{name: "wrong profile", mimeType: "text/html;profile=other", want: false},
		{name: "wrong type", mimeType: "application/json", want: false},
		{name: "empty", mimeType: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsUIResourceMIMEType(tt.mimeType))
		})
	}
}

func TestUIClientCapabilities(t *testing.T) {
	caps := uiClientCapabilities()
	require.NotNil(t, caps)
	require.NotNil(t, caps.RootsV2)
	require.True(t, caps.RootsV2.ListChanged)
	require.Equal(t, map[string]any{
		"mimeTypes": []any{UIResourceMIMEType},
	}, caps.Extensions[UIExtensionID])
}
