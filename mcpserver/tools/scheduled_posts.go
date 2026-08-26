// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost/server/public/model"
)

// ListScheduledPostsArgs represents arguments for the list_scheduled_posts tool.
type ListScheduledPostsArgs struct {
	TeamID string `json:"team_id" jsonschema:"The team whose pending scheduled posts to list,minLength=26,maxLength=26"`
}

// CreateScheduledPostArgs represents arguments for the create_scheduled_post tool.
type CreateScheduledPostArgs struct {
	ChannelID   string `json:"channel_id" jsonschema:"The ID of the channel to post in,minLength=26,maxLength=26"`
	Message     string `json:"message" jsonschema:"The message content,minLength=1"`
	ScheduledAt string `json:"scheduled_at" jsonschema:"When to send the message (ISO 8601 / RFC3339 timestamp, must be in the future),format=date-time"`
	RootID      string `json:"root_id,omitempty" jsonschema:"Optional root post ID to schedule a thread reply,maxLength=26"`
}

// UpdateScheduledPostArgs represents arguments for the update_scheduled_post tool.
// root_id is intentionally absent: the server restores it from the existing
// record (RestoreNonUpdatableFields), so a scheduled post's thread cannot change.
type UpdateScheduledPostArgs struct {
	ScheduledPostID string `json:"scheduled_post_id" jsonschema:"The ID of the pending scheduled post,minLength=26,maxLength=26"`
	ChannelID       string `json:"channel_id" jsonschema:"The ID of the channel the scheduled post belongs to,minLength=26,maxLength=26"`
	Message         string `json:"message,omitempty" jsonschema:"New message content (optional)"`
	ScheduledAt     string `json:"scheduled_at,omitempty" jsonschema:"New send time (ISO 8601 / RFC3339 timestamp, optional),format=date-time"`
}

// DeleteScheduledPostArgs represents arguments for the delete_scheduled_post tool.
type DeleteScheduledPostArgs struct {
	ScheduledPostID string `json:"scheduled_post_id" jsonschema:"The ID of the pending scheduled post to cancel,minLength=26,maxLength=26"`
}

// SetPostReminderArgs represents arguments for the set_post_reminder tool.
type SetPostReminderArgs struct {
	PostID   string `json:"post_id" jsonschema:"The ID of the post to be reminded about,minLength=26,maxLength=26"`
	RemindAt string `json:"remind_at" jsonschema:"When to be reminded (ISO 8601 / RFC3339 timestamp, must be in the future),format=date-time"`
}

const (
	listScheduledPostsDescription  = "List your pending scheduled posts for a team. Parameters: team_id (required). Returns each scheduled post's channel, send time, and message."
	createScheduledPostDescription = "Schedule a message to send to a channel (or thread) at a future time. Parameters: channel_id (required), message (required), scheduled_at (required, future ISO 8601 timestamp), root_id (optional). Returns the scheduled post ID."
	updateScheduledPostDescription = "Change a pending scheduled post's text or send time. Parameters: scheduled_post_id (required), channel_id (required), message (optional), scheduled_at (optional). Unspecified fields keep their current values."
	deleteScheduledPostDescription = "Cancel a pending scheduled post. Parameters: scheduled_post_id (required)."
	setPostReminderDescription     = "Set yourself a reminder about a post at a given time. Parameters: post_id (required), remind_at (required, future ISO 8601 timestamp)."
)

// getScheduledPostTools returns the scheduled-post and reminder tools.
func (p *MattermostToolProvider) getScheduledPostTools() []MCPTool {
	return []MCPTool{
		{
			Name:        "list_scheduled_posts",
			Description: listScheduledPostsDescription,
			Schema:      NewJSONSchemaForAccessMode[ListScheduledPostsArgs](string(p.accessMode)),
			Resolver:    typed("list_scheduled_posts", p.toolListScheduledPosts),
		},
		{
			Name:        "create_scheduled_post",
			Description: createScheduledPostDescription,
			Schema:      NewJSONSchemaForAccessMode[CreateScheduledPostArgs](string(p.accessMode)),
			Resolver:    typed("create_scheduled_post", p.toolCreateScheduledPost),
		},
		{
			Name:        "update_scheduled_post",
			Description: updateScheduledPostDescription,
			Schema:      NewJSONSchemaForAccessMode[UpdateScheduledPostArgs](string(p.accessMode)),
			Resolver:    typed("update_scheduled_post", p.toolUpdateScheduledPost),
		},
		{
			Name:        "delete_scheduled_post",
			Description: deleteScheduledPostDescription,
			Schema:      NewJSONSchemaForAccessMode[DeleteScheduledPostArgs](string(p.accessMode)),
			Resolver:    typed("delete_scheduled_post", p.toolDeleteScheduledPost),
		},
		{
			Name:        "set_post_reminder",
			Description: setPostReminderDescription,
			Schema:      NewJSONSchemaForAccessMode[SetPostReminderArgs](string(p.accessMode)),
			Resolver:    typed("set_post_reminder", p.toolSetPostReminder),
		},
	}
}

// toolListScheduledPosts implements the list_scheduled_posts tool.
func (p *MattermostToolProvider) toolListScheduledPosts(mcpContext *MCPToolContext, args ListScheduledPostsArgs) (string, error) {
	if err := requireID("team_id", args.TeamID); err != nil {
		return "", err
	}

	byChannel, _, err := mcpContext.Client.GetUserScheduledPosts(mcpContext.Ctx, args.TeamID, includeDirectChannels(mcpContext))
	if err != nil {
		return "", fmt.Errorf("error fetching scheduled posts: %w", err)
	}

	scheduled := make([]*model.ScheduledPost, 0)
	for _, posts := range byChannel {
		scheduled = append(scheduled, posts...)
	}
	scheduled = filterScheduledPosts(mcpContext, scheduled)
	if len(scheduled) == 0 {
		return "no scheduled posts found", nil
	}

	sort.Slice(scheduled, func(i, j int) bool {
		return scheduled[i].ScheduledAt < scheduled[j].ScheduledAt
	})

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d scheduled post(s):\n\n", len(scheduled)))
	for i, sp := range scheduled {
		format.WriteScheduledPost(&result, format.ScheduledPostEntry{
			HeaderLabel:   fmt.Sprintf("Scheduled Post %d", i+1),
			ScheduledPost: sp,
		})
	}
	return result.String(), nil
}

// toolCreateScheduledPost implements the create_scheduled_post tool.
func (p *MattermostToolProvider) toolCreateScheduledPost(mcpContext *MCPToolContext, args CreateScheduledPostArgs) (string, error) {
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if args.Message == "" {
		return "", fmt.Errorf("message cannot be empty")
	}
	if err := optionalID("root_id", args.RootID); err != nil {
		return "", err
	}
	scheduledAt, err := parseTimeMillis(args.ScheduledAt)
	if err != nil {
		return "", err
	}

	scheduledPost := &model.ScheduledPost{
		Draft: model.Draft{
			ChannelId: args.ChannelID,
			Message:   args.Message,
			RootId:    args.RootID,
		},
		ScheduledAt: scheduledAt,
	}
	p.stampAIGeneratedDraft(&scheduledPost.Draft, mcpContext)

	created, _, err := mcpContext.Client.CreateScheduledPost(mcpContext.Ctx, scheduledPost)
	if err != nil {
		return "", fmt.Errorf("error creating scheduled post: %w", err)
	}

	return fmt.Sprintf("Successfully scheduled post %s for channel %s", created.Id, args.ChannelID), nil
}

// toolUpdateScheduledPost implements the update_scheduled_post tool.
func (p *MattermostToolProvider) toolUpdateScheduledPost(mcpContext *MCPToolContext, args UpdateScheduledPostArgs) (string, error) {
	if err := requireID("scheduled_post_id", args.ScheduledPostID); err != nil {
		return "", err
	}
	if err := requireID("channel_id", args.ChannelID); err != nil {
		return "", err
	}
	if args.Message == "" && args.ScheduledAt == "" {
		return "", fmt.Errorf("provide message and/or scheduled_at to update")
	}

	// UpdateScheduledPost validates and replaces the whole record server-side, so
	// start from the existing scheduled post and override only the fields the
	// caller supplied. Otherwise omitted fields would fail validation (an empty
	// message or zero scheduled_at is rejected) or silently drop files/priority.
	scheduledPost, err := p.findScheduledPost(mcpContext, args.ChannelID, args.ScheduledPostID)
	if err != nil {
		return "", err
	}

	if args.Message != "" {
		scheduledPost.Message = args.Message
	}
	if args.ScheduledAt != "" {
		var scheduledAt int64
		scheduledAt, err = parseTimeMillis(args.ScheduledAt)
		if err != nil {
			return "", err
		}
		scheduledPost.ScheduledAt = scheduledAt
	}
	p.stampAIGeneratedDraft(&scheduledPost.Draft, mcpContext)

	updated, _, err := mcpContext.Client.UpdateScheduledPost(mcpContext.Ctx, scheduledPost)
	if err != nil {
		return "", fmt.Errorf("error updating scheduled post: %w", err)
	}

	return fmt.Sprintf("Successfully updated scheduled post %s", updated.Id), nil
}

// findScheduledPost locates a pending scheduled post by ID. There is no
// get-by-ID endpoint, so it derives the team from the channel and scans the
// user's scheduled posts, requesting direct-channel posts too.
func (p *MattermostToolProvider) findScheduledPost(mcpContext *MCPToolContext, channelID, scheduledPostID string) (*model.ScheduledPost, error) {
	channel, _, err := mcpContext.Client.GetChannel(mcpContext.Ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("error fetching channel for scheduled post: %w", err)
	}

	// The scheduled-posts endpoint requires a non-empty team ID. DM/GM channels
	// have no team, so fall back to any team the user is on; their scheduled
	// posts are returned under the "directChannels" key when requested.
	teamID := channel.TeamId
	if teamID == "" {
		var userID string
		userID, err = p.resolveUserID(mcpContext)
		if err != nil {
			return nil, err
		}
		var teams []*model.Team
		teams, _, err = mcpContext.Client.GetTeamsForUser(mcpContext.Ctx, userID, "")
		if err != nil {
			return nil, fmt.Errorf("error resolving a team for scheduled post lookup: %w", err)
		}
		if len(teams) == 0 {
			return nil, fmt.Errorf("scheduled post %s not found (user is not on any team)", scheduledPostID)
		}
		teamID = teams[0].Id
	}

	byChannel, _, err := mcpContext.Client.GetUserScheduledPosts(mcpContext.Ctx, teamID, includeDirectChannels(mcpContext))
	if err != nil {
		return nil, fmt.Errorf("error fetching scheduled posts: %w", err)
	}
	for _, posts := range byChannel {
		for _, sp := range posts {
			if sp.Id == scheduledPostID {
				if mcpContext.IsBotSession {
					ch, chErr := mcpContext.channelResolver().channel(sp.ChannelId)
					if chErr != nil {
						return nil, errUnverifiedChannelReference
					}
					if err := rejectIfNotOpenOrPrivate(ch); err != nil {
						return nil, err
					}
				}
				return sp, nil
			}
		}
	}
	return nil, fmt.Errorf("scheduled post %s not found in channel %s", scheduledPostID, channelID)
}

// toolDeleteScheduledPost implements the delete_scheduled_post tool.
func (p *MattermostToolProvider) toolDeleteScheduledPost(mcpContext *MCPToolContext, args DeleteScheduledPostArgs) (string, error) {
	if err := requireID("scheduled_post_id", args.ScheduledPostID); err != nil {
		return "", err
	}

	if mcpContext.IsBotSession {
		if err := p.requireVisibleScheduledPost(mcpContext, args.ScheduledPostID); err != nil {
			return "", err
		}
	}

	if _, _, err := mcpContext.Client.DeleteScheduledPost(mcpContext.Ctx, args.ScheduledPostID); err != nil {
		return "", fmt.Errorf("error deleting scheduled post: %w", err)
	}

	return fmt.Sprintf("Successfully canceled scheduled post %s", args.ScheduledPostID), nil
}

// requireVisibleScheduledPost fails closed for bot sessions if the scheduled
// post cannot be found among non-direct posts or its channel is not open/private.
func (p *MattermostToolProvider) requireVisibleScheduledPost(mcpContext *MCPToolContext, scheduledPostID string) error {
	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return errUnverifiedChannelReference
	}
	teams, _, err := mcpContext.Client.GetTeamsForUser(mcpContext.Ctx, userID, "")
	if err != nil {
		return errUnverifiedChannelReference
	}
	r := mcpContext.channelResolver()
	for _, team := range teams {
		if team == nil {
			continue
		}
		byChannel, _, listErr := mcpContext.Client.GetUserScheduledPosts(mcpContext.Ctx, team.Id, false)
		if listErr != nil {
			return errUnverifiedChannelReference
		}
		for _, posts := range byChannel {
			for _, sp := range posts {
				if sp == nil || sp.Id != scheduledPostID {
					continue
				}
				ch, chErr := r.channel(sp.ChannelId)
				if chErr != nil {
					return errUnverifiedChannelReference
				}
				return rejectIfNotOpenOrPrivate(ch)
			}
		}
	}
	return errDirectOrGroupInaccessible
}

// toolSetPostReminder implements the set_post_reminder tool.
func (p *MattermostToolProvider) toolSetPostReminder(mcpContext *MCPToolContext, args SetPostReminderArgs) (string, error) {
	if err := requireID("post_id", args.PostID); err != nil {
		return "", err
	}
	targetTime, err := parseTimeMillis(args.RemindAt)
	if err != nil {
		return "", err
	}

	userID, err := p.resolveUserID(mcpContext)
	if err != nil {
		return "", err
	}

	// SetPostReminder expects the target time in seconds.
	reminder := &model.PostReminder{
		PostId:     args.PostID,
		UserId:     userID,
		TargetTime: targetTime / 1000,
	}

	if _, err := mcpContext.Client.SetPostReminder(mcpContext.Ctx, reminder); err != nil {
		return "", fmt.Errorf("error setting post reminder: %w", err)
	}

	return fmt.Sprintf("Successfully set a reminder for post %s", args.PostID), nil
}

// stampAIGeneratedDraft applies AI attribution to a Draft's props, mirroring
// stampAIGenerated for posts.
func (p *MattermostToolProvider) stampAIGeneratedDraft(draft *model.Draft, mcpContext *MCPToolContext) {
	if !p.trackAIGenerated {
		return
	}
	var userID string
	switch {
	case mcpContext.BotUserID != "" && model.IsValidId(mcpContext.BotUserID):
		userID = mcpContext.BotUserID
	case mcpContext.Client != nil:
		if user, _, err := mcpContext.Client.GetMe(mcpContext.Ctx, ""); err == nil && user != nil {
			userID = user.Id
		}
	}
	if userID != "" {
		props := draft.GetProps()
		if props == nil {
			props = model.StringInterface{}
		}
		props["ai_generated_by"] = userID
		draft.SetProps(props)
	}
}
