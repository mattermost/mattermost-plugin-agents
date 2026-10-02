// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
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
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].HeldAnswer != "" {
			text = turns[i].HeldAnswer
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
