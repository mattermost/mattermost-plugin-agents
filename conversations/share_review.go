// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost/server/public/model"
)

// shareReview decides whether the results of calls, executed without a
// per-call approval in a channel, may reach the channel. It returns nil when
// every business tool reported the channels it drew from and the
// destination's members can read each of them. Otherwise the results need the
// requester's share decision, and the review lists the sources the members
// may not be able to read. Remote tools and tools that do not report sources
// always need a decision.
func shareReview(llmCtx *llm.Context, calls []llm.ToolCall) *llm.ShareReview {
	if llmCtx == nil {
		return &llm.ShareReview{}
	}
	known := true
	reported := reportedSourceToolSet(llmCtx)
	for _, tc := range calls {
		if isMCPMetaToolCall(tc, llmCtx) {
			continue
		}
		var lookup llm.ToolLookup
		found := false
		if llmCtx.Tools != nil {
			lookup, found = llmCtx.Tools.LookupTool(tc.Name, tc.ServerOrigin)
		}
		if !found {
			known = false
			continue
		}
		if isAutoExecuteBuiltIn(lookup.Tool) {
			continue
		}
		if mcp.IsRemoteServerOrigin(lookup.ServerOrigin) {
			known = false
			continue
		}
		if _, ok := provenanceReportingTools[lookup.BareName]; !ok || !reported[lookup.BareName] {
			known = false
		}
	}

	uncovered, readable := uncoveredSources(llmCtx)
	if known && readable && len(uncovered) == 0 {
		return nil
	}
	return &llm.ShareReview{Sources: uncovered}
}

// uncoveredSources lists the recorded source channels the destination's
// members may not be able to read. readable is false when the record is not
// a channel list.
func uncoveredSources(llmCtx *llm.Context) (uncovered []llm.SourceChannel, readable bool) {
	raw, recorded := llmCtx.Parameters[mcp.SourceChannelsParam]
	sources, ok := raw.([]*model.Channel)
	if recorded && !ok {
		return nil, false
	}
	readable = true
	dest := llmCtx.Channel
	destTeamID := ""
	if dest != nil {
		destTeamID = dest.TeamId
	}
	if destTeamID == "" && llmCtx.Team != nil {
		destTeamID = llmCtx.Team.Id
	}
	for _, src := range sources {
		if src == nil {
			readable = false
			continue
		}
		if sourceCoveredByDestination(src, dest, destTeamID, llmCtx.DestinationHasNoGuests) {
			continue
		}
		uncovered = append(uncovered, llm.SourceChannel{ID: src.Id, Name: src.Name, DisplayName: src.DisplayName})
	}
	return uncovered, readable
}

func sourceCoveredByDestination(src, dest *model.Channel, destTeamID string, destHasNoGuests bool) bool {
	if src == nil || dest == nil {
		return false
	}
	if src.Id != "" && src.Id == dest.Id {
		return true
	}
	// Guests can view the team but cannot read an arbitrary public channel.
	// Only a destination known to have no guests covers other public channels.
	return src.Type == model.ChannelTypeOpen && destHasNoGuests && destTeamID != "" && src.TeamId == destTeamID
}

func reportedSourceToolSet(llmCtx *llm.Context) map[string]bool {
	reported, _ := llmCtx.Parameters[mcp.ReportedSourceToolsParam].(map[string]bool)
	return reported
}
