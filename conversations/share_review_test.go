// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func TestShareReview(t *testing.T) {
	const remoteOrigin = "https://mcp.example.com/mcp"

	destPublic := &model.Channel{Id: "dest-public", Name: "town-square", Type: model.ChannelTypeOpen, TeamId: "team-a"}
	destPrivate := &model.Channel{Id: "dest-private", Type: model.ChannelTypePrivate, TeamId: "team-a"}
	destGM := &model.Channel{Id: "dest-gm", Type: model.ChannelTypeGroup}
	otherPrivate := &model.Channel{Id: "other-private", Name: "sales", DisplayName: "Sales", Type: model.ChannelTypePrivate, TeamId: "team-a"}
	otherPublic := &model.Channel{Id: "other-public", Name: "off-topic", DisplayName: "Off-Topic", Type: model.ChannelTypeOpen, TeamId: "team-a"}
	otherTeamPublic := &model.Channel{Id: "other-team-public", Type: model.ChannelTypeOpen, TeamId: "team-b"}
	sourceDM := &model.Channel{Id: "source-dm", Type: model.ChannelTypeDirect}
	idOnly := &model.Channel{Id: "unknown-channel"}

	readChannel := llm.Tool{Name: "mattermost__read_channel", ServerOrigin: mcp.EmbeddedClientKey}
	getTime := llm.Tool{Name: "mattermost__get_time", ServerOrigin: mcp.EmbeddedClientKey}
	remoteTool := llm.Tool{Name: "jira__get_issue", ServerOrigin: remoteOrigin}
	createFile := llm.Tool{Name: "create_file", AutoExecute: true}

	callOf := func(tool llm.Tool) llm.ToolCall {
		return llm.ToolCall{Name: tool.Name, ServerOrigin: tool.ServerOrigin}
	}

	tests := []struct {
		name        string
		dest        *model.Channel
		noGuests    bool
		calls       []llm.Tool
		sources     any
		reported    []string
		wantReview  bool
		wantSources []llm.SourceChannel
	}{
		{name: "source is the destination", dest: destPublic, calls: []llm.Tool{readChannel}, sources: []*model.Channel{destPublic}, reported: []string{"read_channel"}},
		{name: "source is the private destination", dest: destPrivate, calls: []llm.Tool{readChannel}, sources: []*model.Channel{destPrivate}, reported: []string{"read_channel"}},
		{name: "group message is its own source", dest: destGM, calls: []llm.Tool{readChannel}, sources: []*model.Channel{destGM}, reported: []string{"read_channel"}},
		{name: "reported no sources", dest: destPublic, calls: []llm.Tool{readChannel}, sources: []*model.Channel{}, reported: []string{"read_channel"}},
		{name: "public channel of the team, no guests in the destination", dest: destPublic, noGuests: true, calls: []llm.Tool{readChannel}, sources: []*model.Channel{otherPublic}, reported: []string{"read_channel"}},
		{name: "only auto-execute built-ins ran", dest: destPublic, calls: []llm.Tool{createFile}},
		{
			name: "public channel of the team when the destination may have guests", dest: destPublic,
			calls: []llm.Tool{readChannel}, sources: []*model.Channel{otherPublic}, reported: []string{"read_channel"},
			wantReview: true, wantSources: []llm.SourceChannel{{ID: "other-public", Name: "off-topic", DisplayName: "Off-Topic"}},
		},
		{
			name: "another private channel is named", dest: destPublic, noGuests: true,
			calls: []llm.Tool{readChannel}, sources: []*model.Channel{destPublic, otherPublic, otherPrivate}, reported: []string{"read_channel"},
			wantReview: true, wantSources: []llm.SourceChannel{{ID: "other-private", Name: "sales", DisplayName: "Sales"}},
		},
		{
			name: "a different private channel of a private destination", dest: destPrivate, noGuests: true,
			calls: []llm.Tool{readChannel}, sources: []*model.Channel{otherPrivate}, reported: []string{"read_channel"},
			wantReview: true, wantSources: []llm.SourceChannel{{ID: "other-private", Name: "sales", DisplayName: "Sales"}},
		},
		{
			name: "public channel of another team", dest: destPublic, noGuests: true,
			calls: []llm.Tool{readChannel}, sources: []*model.Channel{otherTeamPublic}, reported: []string{"read_channel"},
			wantReview: true, wantSources: []llm.SourceChannel{{ID: "other-team-public"}},
		},
		{
			name: "public channel read into a group message", dest: destGM, noGuests: true,
			calls: []llm.Tool{readChannel}, sources: []*model.Channel{otherPublic}, reported: []string{"read_channel"},
			wantReview: true, wantSources: []llm.SourceChannel{{ID: "other-public", Name: "off-topic", DisplayName: "Off-Topic"}},
		},
		{
			name: "a direct message source", dest: destPublic, noGuests: true,
			calls: []llm.Tool{readChannel}, sources: []*model.Channel{sourceDM}, reported: []string{"read_channel"},
			wantReview: true, wantSources: []llm.SourceChannel{{ID: "source-dm"}},
		},
		{
			name: "a source known only by ID", dest: destPublic, noGuests: true,
			calls: []llm.Tool{readChannel}, sources: []*model.Channel{idOnly}, reported: []string{"read_channel"},
			wantReview: true, wantSources: []llm.SourceChannel{{ID: "unknown-channel"}},
		},
		{name: "no destination channel", noGuests: true, calls: []llm.Tool{readChannel}, sources: []*model.Channel{otherPublic}, reported: []string{"read_channel"}, wantReview: true, wantSources: []llm.SourceChannel{{ID: "other-public", Name: "off-topic", DisplayName: "Off-Topic"}}},
		{name: "a tool that reports no sources", dest: destPublic, calls: []llm.Tool{getTime}, wantReview: true},
		{name: "a reporting tool that did not report", dest: destPublic, calls: []llm.Tool{readChannel}, sources: []*model.Channel{destPublic}, wantReview: true},
		{name: "a remote tool", dest: destPublic, calls: []llm.Tool{remoteTool}, wantReview: true},
		{name: "a remote tool beside a covered read", dest: destPublic, calls: []llm.Tool{readChannel, remoteTool}, sources: []*model.Channel{destPublic}, reported: []string{"read_channel"}, wantReview: true},
		{name: "a remote tool claiming a reporting name", dest: destPublic, calls: []llm.Tool{{Name: "remote__read_channel", ServerOrigin: remoteOrigin}}, sources: []*model.Channel{destPublic}, reported: []string{"read_channel"}, wantReview: true},
		{name: "a tool missing from the catalog", dest: destPublic, calls: []llm.Tool{{Name: "mattermost__read_post", ServerOrigin: mcp.EmbeddedClientKey}}, wantReview: true},
		{
			name: "sources of an unexpected type", dest: destPublic, calls: []llm.Tool{readChannel},
			sources: []any{map[string]any{"id": "dest-public"}}, reported: []string{"read_channel"}, wantReview: true,
		},
		{
			name: "a missing source entry", dest: destPublic, calls: []llm.Tool{readChannel},
			sources: []*model.Channel{destPublic, nil}, reported: []string{"read_channel"}, wantReview: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			llmCtx := &llm.Context{Channel: tc.dest, Tools: llm.NewToolStore(), DestinationHasNoGuests: tc.noGuests, Parameters: map[string]any{}}
			if tc.dest != nil && tc.dest.TeamId != "" {
				llmCtx.Team = &model.Team{Id: tc.dest.TeamId}
			}
			if tc.sources != nil {
				llmCtx.Parameters[mcp.SourceChannelsParam] = tc.sources
			}
			if tc.reported != nil {
				reported := map[string]bool{}
				for _, name := range tc.reported {
					reported[name] = true
				}
				llmCtx.Parameters[mcp.ReportedSourceToolsParam] = reported
			}
			var calls []llm.ToolCall
			for _, tool := range tc.calls {
				if tool.Name != "mattermost__read_post" {
					llmCtx.Tools.AddTools([]llm.Tool{tool})
				}
				calls = append(calls, callOf(tool))
			}

			review := shareReview(llmCtx, calls)

			if !tc.wantReview {
				assert.Nil(t, review)
				return
			}
			if assert.NotNil(t, review) {
				assert.Equal(t, tc.wantSources, review.Sources)
			}
		})
	}
}
