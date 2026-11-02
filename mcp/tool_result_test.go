// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallToolWithMetadataRendersEveryContentType(t *testing.T) {
	tests := []struct {
		name    string
		result  *mcp.CallToolResult
		want    string
		wantErr string
	}{
		{
			name:   "text",
			result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "hello"}}},
			want:   "hello\n",
		},
		{
			name: "embedded text resource",
			result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.EmbeddedResource{
				Resource: &mcp.ResourceContents{URI: "file:///notes.txt", MIMEType: "text/plain", Text: "note body"},
			}}},
			want: "note body\n",
		},
		{
			name: "resource links",
			result: &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.ResourceLink{URI: "file:///a.txt", Name: "a.txt", Title: "Report A"},
				&mcp.ResourceLink{URI: "file:///b.txt", Name: "b.txt"},
				&mcp.ResourceLink{URI: "file:///c.txt"},
			}},
			want: "[resource: file:///a.txt (Report A)]\n[resource: file:///b.txt (b.txt)]\n[resource: file:///c.txt]\n",
		},
		{
			name: "binary content gets placeholders",
			result: &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.ImageContent{Data: []byte("png"), MIMEType: "image/png"},
				&mcp.AudioContent{Data: []byte("wav"), MIMEType: "audio/wav"},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///r.pdf", MIMEType: "application/pdf", Blob: []byte("pdf")}},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///r.bin", Blob: []byte("bin")}},
			}},
			want: "[image content omitted: image/png]\n[audio content omitted: audio/wav]\n[resource content omitted: application/pdf]\n[resource content omitted]\n",
		},
		{
			name:   "structured content without text",
			result: &mcp.CallToolResult{StructuredContent: map[string]any{"count": 2}},
			want:   "{\"count\":2}\n",
		},
		{
			name: "structured content after a placeholder",
			result: &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.ImageContent{Data: []byte("png"), MIMEType: "image/png"}},
				StructuredContent: map[string]any{"count": 2},
			},
			want: "[image content omitted: image/png]\n{\"count\":2}\n",
		},
		{
			name: "text wins over structured content",
			result: &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: "{\"count\":2}"}},
				StructuredContent: map[string]any{"count": 2},
			},
			want: "{\"count\":2}\n",
		},
		{
			name: "tool error keeps its text",
			result: &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "quota exceeded"}},
			},
			want:    "quota exceeded",
			wantErr: "quota exceeded",
		},
		{
			name:    "tool error without content",
			result:  &mcp.CallToolResult{IsError: true},
			wantErr: "tool render_tool on server srv returned an error",
		},
		{
			name:    "empty result",
			result:  &mcp.CallToolResult{},
			wantErr: "no text content found in response from tool render_tool on server srv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "render-test", Version: "1.0.0"}, nil)
			server.AddTool(&mcp.Tool{
				Name:        "render_tool",
				InputSchema: map[string]any{"type": "object"},
			}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return tt.result, nil
			})
			client := &Client{
				session: connectInMemoryTestSession(t, server),
				config:  ServerConfig{Name: "srv"},
				log:     newTestLogService(),
			}

			got, err := client.CallToolWithMetadata(context.Background(), "render_tool", map[string]any{}, nil)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
