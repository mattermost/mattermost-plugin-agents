// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost/server/public/model"
)

type fakeChannelArtifactsService struct {
	enabled bool
	entries []*channelartifacts.Entry
	view    ChannelArtifactContent
	listErr error
	getErr  error
	gotOpts ChannelArtifactReadOptions
}

func (f *fakeChannelArtifactsService) Enabled() bool { return f.enabled }

func (f *fakeChannelArtifactsService) List(_ context.Context, _, _ string) ([]*channelartifacts.Entry, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.entries, nil
}

func (f *fakeChannelArtifactsService) Get(_ context.Context, _, _ string, opts ChannelArtifactReadOptions) (ChannelArtifactContent, error) {
	f.gotOpts = opts
	if f.getErr != nil {
		return ChannelArtifactContent{}, f.getErr
	}
	return f.view, nil
}

func TestToolListChannelArtifacts(t *testing.T) {
	channelID := model.NewId()
	artifactID := model.NewId()

	tests := []struct {
		name        string
		service     ChannelArtifactsService
		channelID   string
		wantErr     string
		wantContain []string
	}{
		{name: "nil service returns clear error", service: nil, channelID: channelID, wantErr: "channel artifacts are not available"},
		{name: "invalid channel id", service: &fakeChannelArtifactsService{}, channelID: "short", wantErr: "channel_id"},
		{name: "not found maps to guidance", service: &fakeChannelArtifactsService{listErr: channelartifacts.ErrNotFound}, channelID: channelID, wantErr: "permission"},
		{name: "empty list", service: &fakeChannelArtifactsService{}, channelID: channelID, wantContain: []string{"no saved artifacts"}},
		{
			name: "lists entries with metadata",
			service: &fakeChannelArtifactsService{entries: []*channelartifacts.Entry{
				{ID: artifactID, ChannelID: channelID, Title: "Ops Report", Version: 2, SHA256: "abcdef0123456789", SourceAgentDisplayName: "Fusion", Available: true},
				{ID: model.NewId(), Title: "Gone", Available: false},
			}},
			channelID:   channelID,
			wantContain: []string{"Ops Report", artifactID, "abcdef012345", "Fusion", "Availability: unavailable"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MattermostToolProvider{channelArtifactsService: tt.service}
			result, err := p.toolListChannelArtifacts(&MCPToolContext{Ctx: t.Context(), UserID: model.NewId()}, ListChannelArtifactsArgs{ChannelID: tt.channelID})
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			for _, want := range tt.wantContain {
				assert.Contains(t, result, want)
			}
		})
	}
}

func TestToolGetChannelArtifact(t *testing.T) {
	artifactID := model.NewId()
	entry := &channelartifacts.Entry{ID: artifactID, Title: "Dashboard", Version: 3, Available: true}
	off := false

	tests := []struct {
		name        string
		service     *fakeChannelArtifactsService
		args        GetChannelArtifactArgs
		wantErr     string
		wantContain []string
		wantNot     []string
		wantOpts    ChannelArtifactReadOptions
	}{
		{name: "not found", service: &fakeChannelArtifactsService{getErr: channelartifacts.ErrNotFound}, args: GetChannelArtifactArgs{ArtifactID: artifactID}, wantErr: "list_channel_artifacts"},
		{name: "source deleted", service: &fakeChannelArtifactsService{getErr: channelartifacts.ErrUnavailable}, args: GetChannelArtifactArgs{ArtifactID: artifactID}, wantErr: "deleted"},
		{name: "invalid id", service: &fakeChannelArtifactsService{}, args: GetChannelArtifactArgs{ArtifactID: "x"}, wantErr: "artifact_id"},
		{
			name:        "metadata only",
			service:     &fakeChannelArtifactsService{view: ChannelArtifactContent{Entry: entry}},
			args:        GetChannelArtifactArgs{ArtifactID: artifactID, IncludeContent: &off},
			wantContain: []string{"Dashboard"},
			wantOpts:    ChannelArtifactReadOptions{IncludeContent: false},
		},
		{
			name: "first window points at the rest",
			service: &fakeChannelArtifactsService{view: ChannelArtifactContent{
				Entry: entry, ContentIncluded: true, Content: "<html>", TotalRunes: 100, Returned: 6, HasMore: true, NextOffset: 6,
			}},
			args:        GetChannelArtifactArgs{ArtifactID: artifactID, ContentLimit: 6},
			wantContain: []string{"<html>", "characters 0-6 of 100", "offset=6"},
			wantOpts:    ChannelArtifactReadOptions{IncludeContent: true, Limit: 6},
		},
		{
			name: "a Markdown artifact is labeled as Markdown",
			service: &fakeChannelArtifactsService{view: ChannelArtifactContent{
				Entry:           &channelartifacts.Entry{ID: artifactID, Title: "Charter", Format: channelartifacts.FormatMarkdown, Available: true},
				ContentIncluded: true, Content: "# Goals", TotalRunes: 7, Returned: 7,
			}},
			args:        GetChannelArtifactArgs{ArtifactID: artifactID},
			wantContain: []string{"Format: markdown", "Markdown content (characters 0-7 of 7)", "# Goals"},
			wantNot:     []string{"HTML content"},
			wantOpts:    ChannelArtifactReadOptions{IncludeContent: true},
		},
		{
			name: "later window and version are passed through",
			service: &fakeChannelArtifactsService{view: ChannelArtifactContent{
				Entry: entry, ContentIncluded: true, Content: "tail", Offset: 96, TotalRunes: 100, Returned: 4,
			}},
			args:        GetChannelArtifactArgs{ArtifactID: artifactID, Offset: 96, Version: 2},
			wantContain: []string{"HTML content (characters 96-100 of 100)", "tail"},
			wantOpts:    ChannelArtifactReadOptions{IncludeContent: true, Offset: 96, Version: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MattermostToolProvider{channelArtifactsService: tt.service}
			result, err := p.toolGetChannelArtifact(&MCPToolContext{Ctx: t.Context(), UserID: model.NewId()}, tt.args)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			for _, want := range tt.wantContain {
				assert.Contains(t, result, want)
			}
			for _, unwanted := range tt.wantNot {
				assert.NotContains(t, result, unwanted)
			}
			assert.Equal(t, tt.wantOpts, tt.service.gotOpts)
		})
	}
}

func TestChannelArtifactWindow(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		offset     int
		limit      int
		want       string
		wantMore   bool
		wantNext   int
		wantReturn int
	}{
		{name: "whole text", text: "héllo", limit: 10, want: "héllo", wantReturn: 5},
		{name: "first window counts characters", text: "héllo wörld", limit: 5, want: "héllo", wantMore: true, wantNext: 5, wantReturn: 5},
		{name: "continues from the offset", text: "héllo wörld", offset: 5, limit: 100, want: " wörld", wantReturn: 6},
		{name: "offset past the end", text: "abc", offset: 9, want: ""},
		{name: "negative offset starts at zero", text: "abc", offset: -4, want: "abc", wantReturn: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChannelArtifactWindow(tt.text, tt.offset, tt.limit)
			assert.Equal(t, tt.want, got.Content)
			assert.Equal(t, tt.wantMore, got.HasMore)
			assert.Equal(t, tt.wantNext, got.NextOffset)
			assert.Equal(t, tt.wantReturn, got.Returned)
		})
	}

	t.Run("limit is capped and defaulted", func(t *testing.T) {
		long := string(make([]rune, MaxChannelArtifactContentLimit+500))
		assert.Equal(t, MaxChannelArtifactContentLimit, ChannelArtifactWindow(long, 0, 1<<20).Returned)
		assert.Equal(t, DefaultChannelArtifactContentLimit, ChannelArtifactWindow(long, 0, 0).Returned)
	})
}

func TestGetChannelArtifactToolsRegistered(t *testing.T) {
	p := &MattermostToolProvider{accessMode: AccessModeRemote, logger: &testLogger{t: t}}
	names := map[string]bool{}
	for _, tool := range p.getChannelArtifactTools() {
		names[tool.Name] = tool.ReadOnly
	}
	assert.Equal(t, map[string]bool{"list_channel_artifacts": true, "get_channel_artifact": true}, names)
}

func TestChannelArtifactsToolsMiddleware(t *testing.T) {
	tools := []string{"read_post", "list_channel_artifacts", "get_channel_artifact"}

	t.Run("tools/list hides the library tools while it is off", func(t *testing.T) {
		handler := channelArtifactsToolsMiddleware(alwaysFalse)(newToolsListHandler(tools...))
		result, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)
		listResult, ok := result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.ElementsMatch(t, []string{"read_post"}, toolNames(listResult.Tools))
	})

	t.Run("tools/list keeps them while it is on", func(t *testing.T) {
		handler := channelArtifactsToolsMiddleware(alwaysTrue)(newToolsListHandler(tools...))
		result, err := handler(context.Background(), "tools/list", nil)
		require.NoError(t, err)
		listResult, ok := result.(*mcp.ListToolsResult)
		require.True(t, ok)
		assert.ElementsMatch(t, tools, toolNames(listResult.Tools))
	})

	t.Run("tools/call is refused while it is off", func(t *testing.T) {
		called := false
		handler := channelArtifactsToolsMiddleware(alwaysFalse)(newToolsCallHandler(&called, "ran"))
		result, err := handler(context.Background(), "tools/call", callToolRequest("get_channel_artifact"))
		require.NoError(t, err)
		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		assert.True(t, callResult.IsError)
		assert.False(t, called)
	})

	t.Run("other tools are untouched", func(t *testing.T) {
		called := false
		handler := channelArtifactsToolsMiddleware(alwaysFalse)(newToolsCallHandler(&called, "ran"))
		_, err := handler(context.Background(), "tools/call", callToolRequest("read_post"))
		require.NoError(t, err)
		assert.True(t, called)
	})
}
