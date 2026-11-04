// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/toolrunner"
	"github.com/mattermost/mattermost/server/public/model"
)

// annotateHeldAnswers attaches channel-source markers to a draft that is not
// posted to the channel yet. Names stay on the requester-only block.
func annotateHeldAnswers(turns []toolrunner.ToolTurn, llmCtx *llm.Context) {
	if llmCtx == nil || llmCtx.Channel == nil || llmCtx.Channel.Type == model.ChannelTypeDirect {
		return
	}
	sources, _ := llmCtx.Parameters[sourceChannelsParam].([]*model.Channel)
	destTeamID := llmCtx.Channel.TeamId
	if destTeamID == "" && llmCtx.Team != nil {
		destTeamID = llmCtx.Team.Id
	}
	annotations := channelSourceAnnotations(turns, sources, llmCtx.Channel, destTeamID, llmCtx.DestinationHasNoGuests)
	heldSources := heldSourcesFor(sources, llmCtx.Channel, destTeamID, llmCtx.DestinationHasNoGuests)
	for i := range turns {
		if turns[i].HeldAnswer == "" {
			continue
		}
		turns[i].HeldAnnotations = annotations
		turns[i].HeldSources = heldSources
	}
	if len(turns) > 0 && turns[len(turns)-1].HeldAnswer == "" && len(heldSources) > 0 {
		turns[len(turns)-1].HeldSources = heldSources
		turns[len(turns)-1].HeldAnnotations = annotations
	}
}

func heldSourcesFor(sources []*model.Channel, dest *model.Channel, destTeamID string, destHasNoGuests bool) []toolrunner.HeldSource {
	var out []toolrunner.HeldSource
	for _, src := range sources {
		if src == nil {
			continue
		}
		out = append(out, toolrunner.HeldSource{
			ID:          src.Id,
			Name:        src.Name,
			DisplayName: src.DisplayName,
			Type:        string(src.Type),
			TeamID:      src.TeamId,
			Private:     !sourceCoveredByDestination(src, dest, destTeamID, destHasNoGuests),
		})
	}
	return out
}

func channelSourceAnnotations(turns []toolrunner.ToolTurn, sources []*model.Channel, dest *model.Channel, destTeamID string, destHasNoGuests bool) []llm.Annotation {
	text := ""
	for _, turn := range slices.Backward(turns) {
		if turn.HeldAnswer != "" {
			text = turn.HeldAnswer
			break
		}
	}
	var annotations []llm.Annotation
	index := 1
	for _, src := range sources {
		if src == nil {
			continue
		}
		private := !sourceCoveredByDestination(src, dest, destTeamID, destHasNoGuests)
		label := channelLabel(src)
		if label == "" {
			if private && text != "" {
				annotations = append(annotations, llm.Annotation{
					Type:       llm.AnnotationTypeChannel,
					StartIndex: 0,
					EndIndex:   utf16Len(text),
					ChannelID:  src.Id,
					Private:    true,
					Index:      index,
				})
				index++
			}
			continue
		}
		spans := findUTF16Spans(text, label)
		if src.Name != "" && src.Name != label {
			spans = append(spans, findUTF16Spans(text, src.Name)...)
		}
		if len(spans) == 0 {
			if !private || text == "" {
				continue
			}
			spans = [][2]int{{0, utf16Len(text)}}
		}
		for _, span := range spans {
			annotations = append(annotations, llm.Annotation{
				Type:        llm.AnnotationTypeChannel,
				StartIndex:  span[0],
				EndIndex:    span[1],
				ChannelID:   src.Id,
				ChannelName: label,
				Private:     private,
				Index:       index,
			})
			index++
		}
	}
	return annotations
}

func channelLabel(ch *model.Channel) string {
	if ch == nil {
		return ""
	}
	if ch.DisplayName != "" {
		return ch.DisplayName
	}
	return ch.Name
}

func findUTF16Spans(text, needle string) [][2]int {
	if text == "" || needle == "" {
		return nil
	}
	var spans [][2]int
	rest := text
	base := 0
	for {
		idx := strings.Index(rest, needle)
		if idx < 0 {
			return spans
		}
		start := base + utf16Len(rest[:idx])
		end := start + utf16Len(needle)
		spans = append(spans, [2]int{start, end})
		next := idx + len(needle)
		base += utf16Len(rest[:next])
		rest = rest[next:]
	}
}

func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// publishHeldChannelAnswer copies a requester-only draft onto the channel post
// with private channel names and markers removed. Only drafts from the clicked
// tool round are published. It returns false when there is no draft to publish.
func (c *Conversations) publishHeldChannelAnswer(post *model.Post, turns []store.Turn, decoded [][]conversation.ContentBlock, clickedToolUseIDs map[string]struct{}) bool {
	if post == nil {
		return false
	}
	published := false
	var message string
	for i := range turns {
		if i >= len(decoded) {
			break
		}
		if !turnMatchesClickedTools(decoded[i], clickedToolUseIDs) {
			continue
		}
		changed := false
		for j := range decoded[i] {
			block := &decoded[i][j]
			if !block.RequesterOnly {
				continue
			}
			message = sanitizePublishedAnswer(block.Text, block.SourceChannels)
			block.Text = message
			block.RequesterOnly = false
			block.Citations = nil
			block.SourceChannels = nil
			changed = true
			published = true
		}
		if !changed {
			continue
		}
		updated, err := json.Marshal(decoded[i])
		if err != nil {
			c.mmClient.LogError("Failed to marshal published answer", "error", err)
			return false
		}
		if err := c.convService.UpdateTurnContent(turns[i].ID, updated); err != nil {
			c.mmClient.LogError("Failed to publish held answer", "error", err)
			return false
		}
	}
	if !published {
		return false
	}
	post.Message = message
	if err := c.mmClient.UpdatePost(post); err != nil {
		c.mmClient.LogError("Failed to update post with published answer", "error", err)
		return false
	}
	return true
}

func turnMatchesClickedTools(blocks []conversation.ContentBlock, clickedToolUseIDs map[string]struct{}) bool {
	if len(clickedToolUseIDs) == 0 {
		return false
	}
	for _, b := range blocks {
		switch b.Type {
		case conversation.BlockTypeToolUse:
			if _, ok := clickedToolUseIDs[b.ID]; ok {
				return true
			}
		case conversation.BlockTypeToolResult:
			if _, ok := clickedToolUseIDs[b.ToolUseID]; ok {
				return true
			}
		}
	}
	return false
}

func sanitizePublishedAnswer(text string, sources []conversation.SourceChannel) string {
	for _, src := range sources {
		if !src.Private {
			continue
		}
		for _, token := range []string{src.DisplayName, src.Name} {
			if token == "" {
				continue
			}
			escaped := regexp.QuoteMeta(token)
			if re, err := regexp.Compile(`~` + escaped + `\b`); err == nil {
				text = re.ReplaceAllString(text, "")
			}
			if re, err := regexp.Compile(`\b` + escaped + `\b`); err == nil {
				text = re.ReplaceAllString(text, "")
			}
		}
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// allToolsAutoRunEverywhere reports whether the channel-visible answer may be
// posted now. Every business tool must be auto_run_everywhere, and, outside a
// direct message, every executed tool must have reported provenance that the
// destination audience can read. A false result withholds that answer until
// the requester records a share decision. Tools still run; this does not
// decide execution.
func (c *Conversations) allToolsAutoRunEverywhere(turns []toolrunner.ToolTurn, llmCtx *llm.Context) bool {
	sawToolCall := false
	sawBusinessTool := false
	sawRemoteTool := false
	var executedBare []string
	for _, turn := range turns {
		for _, tc := range turn.AssistantToolCalls {
			sawToolCall = true
			if isMCPMetaToolCall(tc, llmCtx) {
				continue
			}
			var lookup llm.ToolLookup
			var found bool
			if llmCtx != nil {
				lookup, found = llmCtx.Tools.LookupTool(tc.Name, tc.ServerOrigin)
			}
			if !found {
				return false
			}
			// Auto-execute built-ins never require approval, so a round made
			// up only of them can still post its answer.
			if isAutoExecuteBuiltIn(lookup.Tool) {
				continue
			}
			if c.toolPolicyChecker == nil {
				return false
			}
			policy, enabled := c.toolPolicyChecker.GetToolPolicy(lookup.ServerOrigin, lookup.BareName)
			if !enabled || !mcp.IsToolPolicyAutoRunEverywhere(policy) {
				return false
			}
			sawBusinessTool = true
			executedBare = append(executedBare, lookup.BareName)
			if mcp.IsRemoteServerOrigin(lookup.ServerOrigin) {
				sawRemoteTool = true
			}
		}
	}
	if !sawToolCall {
		return false
	}
	if sawBusinessTool && !sourcesReadableByDestination(llmCtx, executedBare, sawRemoteTool) {
		return false
	}
	return true
}

// sourcesReadableByDestination reports whether the destination audience covers
// every source the executed tools drew from. Direct-message destinations skip
// the comparison. A nil destination channel, a remote tool, a tool that did
// not report provenance, or a source the audience cannot read withholds the
// channel answer.
func sourcesReadableByDestination(llmCtx *llm.Context, executedBareNames []string, sawRemoteTool bool) bool {
	if llmCtx == nil || llmCtx.Channel == nil {
		return false
	}
	dest := llmCtx.Channel
	if dest.Type == model.ChannelTypeDirect {
		return true
	}
	if sawRemoteTool {
		return false
	}
	reported := reportedSourceToolSet(llmCtx)
	for _, name := range executedBareNames {
		if _, ok := provenanceReportingTools[name]; !ok {
			return false
		}
		if !reported[name] {
			return false
		}
	}
	if llmCtx.Parameters == nil {
		return false
	}
	raw, ok := llmCtx.Parameters[sourceChannelsParam]
	if !ok {
		return false
	}
	sources, ok := raw.([]*model.Channel)
	if !ok {
		return false
	}
	destTeamID := dest.TeamId
	if destTeamID == "" && llmCtx.Team != nil {
		destTeamID = llmCtx.Team.Id
	}
	for _, src := range sources {
		if !sourceCoveredByDestination(src, dest, destTeamID, llmCtx.DestinationHasNoGuests) {
			return false
		}
	}
	return true
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
	out := map[string]bool{}
	if llmCtx == nil || llmCtx.Parameters == nil {
		return out
	}
	switch v := llmCtx.Parameters[reportedSourceToolsParam].(type) {
	case map[string]bool:
		return v
	case map[string]struct{}:
		for name := range v {
			out[name] = true
		}
	case []string:
		for _, name := range v {
			out[name] = true
		}
	}
	return out
}

// holdUncoveredChannelAnswer stops the channel-visible completion when the
// tools that just ran are not covered by the destination audience. Direct
// messages do not set this.
func (c *Conversations) holdUncoveredChannelAnswer(llmCtx *llm.Context) {
	if llmCtx == nil {
		return
	}
	llmCtx.HoldChannelAnswer = func(calls []llm.ToolCall) bool {
		return !c.allToolsAutoRunEverywhere([]toolrunner.ToolTurn{{AssistantToolCalls: calls}}, llmCtx)
	}
}
