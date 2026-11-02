// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// ListDraftsArgs represents arguments for the list_drafts tool.
type ListDraftsArgs struct {
	TeamID string `json:"team_id" jsonschema:"The team whose drafts to list (direct and group message drafts are included),minLength=26,maxLength=26"`
}

// CreateDraftArgs represents arguments for the create_draft tool.
type CreateDraftArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The ID of the channel to leave the draft in,minLength=26,maxLength=26"`
	Message   string `json:"message" jsonschema:"The draft message content,minLength=1"`
	RootID    string `json:"root_id,omitempty" jsonschema:"Optional root post ID to draft a thread reply,maxLength=26"`
}

// DeleteDraftArgs represents arguments for the delete_draft tool.
type DeleteDraftArgs struct {
	ChannelID string `json:"channel_id" jsonschema:"The ID of the channel the draft belongs to,minLength=26,maxLength=26"`
	RootID    string `json:"root_id,omitempty" jsonschema:"Root post ID when the draft is a thread reply,maxLength=26"`
}

const (
	listDraftsDescription  = "List your unsent message drafts for a team, including direct and group message drafts. Parameters: team_id (required). Returns each draft's channel, thread, last update time, and message."
	createDraftDescription = "Save a message as a draft in a channel (or thread) for you to review and send yourself; nothing is posted. Replaces any existing draft in that channel or thread. Parameters: channel_id (required), message (required), root_id (optional)."
	deleteDraftDescription = "Delete your draft in a channel (or thread). Parameters: channel_id (required), root_id (optional, for a thread reply draft)."
)

// getDraftTools returns the message draft tools.
func (p *MattermostToolProvider) getDraftTools() []MCPTool {
	return []MCPTool{
		mcpReadTool(p, "list_drafts", listDraftsDescription, p.toolListDrafts),
		mcpTool(p, "create_draft", createDraftDescription, p.toolCreateDraft),
		mcpTool(p, "delete_draft", deleteDraftDescription, p.toolDeleteDraft),
	}
}

// toolListDrafts implements the list_drafts tool.
func (p *MattermostToolProvider) toolListDrafts(mcpContext *MCPToolContext, args ListDraftsArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	drafts, _, err := mcpContext.Client.GetDrafts(mcpContext.Ctx, userID, args.TeamID)
	if err != nil {
		return "", fmt.Errorf("error fetching drafts: %w", err)
	}
	if len(drafts) == 0 {
		return "no drafts found", nil
	}

	// Most recently edited first.
	slices.SortFunc(drafts, func(a, b *model.Draft) int {
		return cmp.Compare(b.UpdateAt, a.UpdateAt)
	})

	var result strings.Builder
	fmt.Fprintf(&result, "Found %d draft(s):\n\n", len(drafts))
	for i, d := range drafts {
		format.WriteDraft(&result, format.DraftEntry{
			HeaderLabel: fmt.Sprintf("Draft %d", i+1),
			Draft:       d,
		})
	}
	return result.String(), nil
}

// toolCreateDraft implements the create_draft tool.
func (p *MattermostToolProvider) toolCreateDraft(mcpContext *MCPToolContext, args CreateDraftArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if args.Message == "" {
		return "", fmt.Errorf("message cannot be empty")
	}
	if err := optionalID("root_id", args.RootID); err != nil {
		return "", err
	}

	// The server sets the draft's owner from the session, so UserId is left empty.
	draft := &model.Draft{
		ChannelId: args.ChannelID,
		RootId:    args.RootID,
		Message:   args.Message,
	}
	p.stampAIGeneratedDraft(draft, mcpContext)

	if _, _, err := mcpContext.Client.UpsertDraft(mcpContext.Ctx, draft); err != nil {
		return "", fmt.Errorf("error saving draft: %w", err)
	}

	if args.RootID != "" {
		return fmt.Sprintf("Successfully saved draft reply in thread %s (channel %s)", args.RootID, args.ChannelID), nil
	}
	return fmt.Sprintf("Successfully saved draft in channel %s", args.ChannelID), nil
}

// toolDeleteDraft implements the delete_draft tool.
func (p *MattermostToolProvider) toolDeleteDraft(mcpContext *MCPToolContext, args DeleteDraftArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if err := optionalID("root_id", args.RootID); err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	// Client4.DeleteDraft ignores its rootId argument and always targets the
	// channel-level draft, so thread drafts are deleted through the server's
	// /drafts/{thread_id} route directly.
	route := fmt.Sprintf("/users/%s/channels/%s/drafts", userID, args.ChannelID)
	if args.RootID != "" {
		route += "/" + args.RootID
	}
	resp, err := mcpContext.Client.DoAPIDelete(mcpContext.Ctx, route)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return "", fmt.Errorf("error deleting draft: %w", err)
	}

	if args.RootID != "" {
		return fmt.Sprintf("Successfully deleted draft reply in thread %s", args.RootID), nil
	}
	return fmt.Sprintf("Successfully deleted draft in channel %s", args.ChannelID), nil
}
