// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

// AgentInfo holds display fields for formatting an AI agent list (e.g. MCP tool output).
type AgentInfo struct {
	ID          string
	DisplayName string
	Username    string
}

// AgentList formats discovered agents as a numbered list for LLM-facing text.
// When currentBotUserID matches an agent's ID, a marker line is added for that row.
func AgentList(agents []AgentInfo, currentBotUserID string) string {
	if len(agents) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d agent(s):\n\n", len(agents)))
	for i, a := range agents {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, a.DisplayName))
		b.WriteString(fmt.Sprintf("   ID: %s\n", a.ID))
		b.WriteString(fmt.Sprintf("   Username: @%s\n", a.Username))
		if currentBotUserID != "" && a.ID == currentBotUserID {
			b.WriteString("   ** This is YOU (the current agent) **\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func ThreadData(data *mmapi.ThreadData) string {
	result := ""
	for _, post := range data.Posts {
		username := "unknown"
		if user := data.UsersByID[post.UserId]; user != nil {
			username = user.Username
		}
		if post.CreateAt > 0 {
			t := time.Unix(post.CreateAt/1000, (post.CreateAt%1000)*int64(time.Millisecond))
			result += fmt.Sprintf("%s (%s): %s\n\n", username, t.UTC().Format(time.RFC3339), PostBody(post))
		} else {
			result += fmt.Sprintf("%s: %s\n\n", username, PostBody(post))
		}
	}

	return result
}

func PostBody(post *model.Post) string {
	attachments := post.Attachments()
	if len(attachments) > 0 {
		result := strings.Builder{}
		result.WriteString(post.Message)
		for _, attachment := range attachments {
			result.WriteString("\n")
			if attachment.Pretext != "" {
				result.WriteString(attachment.Pretext)
				result.WriteString("\n")
			}
			if attachment.Title != "" {
				result.WriteString(attachment.Title)
				result.WriteString("\n")
			}
			if attachment.Text != "" {
				result.WriteString(attachment.Text)
				result.WriteString("\n")
			}
			for _, field := range attachment.Fields {
				value, err := json.Marshal(field.Value)
				if err != nil {
					continue
				}
				result.WriteString(field.Title)
				result.WriteString(": ")
				result.Write(value)
				result.WriteString("\n")
			}

			if attachment.Footer != "" {
				result.WriteString(attachment.Footer)
				result.WriteString("\n")
			}
		}
		return result.String()
	}
	return post.Message
}

// AuthoredPost formats a post body with the username of its author for LLM
// consumption.
func AuthoredPost(post *model.Post, username string) string {
	return "@" + username + ": " + PostBody(post)
}

// PostEntry holds pre-resolved data for formatting a single post.
// Used by MCP tools and other callers that need structured post output.
type PostEntry struct {
	// Header components
	HeaderLabel     string  // e.g. "Post 1", "Result 3"
	Username        string  // resolved username; "" → "Unknown User"
	Score           float32 // >0 means show "(Score: X.XX)" — search only
	ReplyAnnotation string  // e.g. "(reply to Post 2)" — appended to header

	// The source post
	Post *model.Post

	// Optional context metadata (search results show per-result channel info)
	ChannelName string
	TeamName    string
	ShowChannel bool // show Channel ID line

}

// FormatPost writes a single formatted post entry to the builder.
func WritePost(w *strings.Builder, entry PostEntry) {
	username := entry.Username
	if username == "" {
		username = "Unknown User"
	}

	// Header line
	if entry.Score > 0 {
		fmt.Fprintf(w, "**%s** (Score: %.2f) by %s", entry.HeaderLabel, entry.Score, username)
	} else {
		fmt.Fprintf(w, "**%s** by %s", entry.HeaderLabel, username)
	}
	if entry.ReplyAnnotation != "" {
		fmt.Fprintf(w, " %s", entry.ReplyAnnotation)
	}
	w.WriteString(":\n")

	// Optional channel/team context
	if entry.ChannelName != "" {
		if entry.TeamName != "" {
			fmt.Fprintf(w, "Channel: %s (Team: %s)\n", entry.ChannelName, entry.TeamName)
		} else {
			fmt.Fprintf(w, "Channel: %s\n", entry.ChannelName)
		}
	}

	// Post ID
	fmt.Fprintf(w, "Post ID: %s\n", entry.Post.Id)

	// Optional Channel ID
	if entry.ShowChannel {
		fmt.Fprintf(w, "Channel ID: %s\n", entry.Post.ChannelId)
	}

	// Optional Root ID
	if entry.Post.RootId != "" {
		fmt.Fprintf(w, "Root ID: %s\n", entry.Post.RootId)
	}

	// Timestamp (only when available)
	if entry.Post.CreateAt > 0 {
		t := time.Unix(entry.Post.CreateAt/1000, (entry.Post.CreateAt%1000)*int64(time.Millisecond))
		fmt.Fprintf(w, "Time: %s\n", t.UTC().Format(time.RFC3339))
	}

	fmt.Fprintf(w, "Message: %s\n\n", PostBody(entry.Post))
}

// PostInfoEntry holds data for formatting a single post's metadata/context.
type PostInfoEntry struct {
	PostID   string
	PostInfo *model.PostInfo
}

// WritePostInfo writes a post's channel/team context metadata to the builder.
func WritePostInfo(w *strings.Builder, entry PostInfoEntry) {
	w.WriteString("Post Information:\n")
	fmt.Fprintf(w, "Post ID: %s\n", entry.PostID)
	fmt.Fprintf(w, "Channel ID: %s\n", entry.PostInfo.ChannelId)
	if entry.PostInfo.ChannelDisplayName != "" {
		fmt.Fprintf(w, "Channel: %s\n", entry.PostInfo.ChannelDisplayName)
	}
	fmt.Fprintf(w, "Channel Type: %s\n", entry.PostInfo.ChannelType)
	if entry.PostInfo.TeamId != "" {
		if entry.PostInfo.TeamDisplayName != "" {
			fmt.Fprintf(w, "Team: %s (ID: %s)\n", entry.PostInfo.TeamDisplayName, entry.PostInfo.TeamId)
		} else {
			fmt.Fprintf(w, "Team ID: %s\n", entry.PostInfo.TeamId)
		}
	}
	fmt.Fprintf(w, "You are a member of this channel: %t\n", entry.PostInfo.HasJoinedChannel)
	fmt.Fprintf(w, "You are a member of this team: %t\n", entry.PostInfo.HasJoinedTeam)
	w.WriteString("\n")
}

// ScheduledPostEntry holds data for formatting a single scheduled post.
type ScheduledPostEntry struct {
	HeaderLabel   string // e.g. "Scheduled Post 1"; empty to omit
	ScheduledPost *model.ScheduledPost
	ChannelName   string // resolved channel display name, optional
}

// WriteScheduledPost writes a formatted scheduled post entry to the builder.
func WriteScheduledPost(w *strings.Builder, entry ScheduledPostEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	sp := entry.ScheduledPost
	fmt.Fprintf(w, "ID: %s\n", sp.Id)
	fmt.Fprintf(w, "Channel ID: %s\n", sp.ChannelId)
	if entry.ChannelName != "" {
		fmt.Fprintf(w, "Channel: %s\n", entry.ChannelName)
	}
	if sp.RootId != "" {
		fmt.Fprintf(w, "Root ID: %s\n", sp.RootId)
	}
	if sp.ScheduledAt > 0 {
		t := time.Unix(sp.ScheduledAt/1000, (sp.ScheduledAt%1000)*int64(time.Millisecond))
		fmt.Fprintf(w, "Scheduled for: %s\n", t.UTC().Format(time.RFC3339))
	}
	if sp.ErrorCode != "" {
		fmt.Fprintf(w, "Error: %s\n", sp.ErrorCode)
	}
	fmt.Fprintf(w, "Message: %s\n\n", sp.Message)
}

// WriteReactions writes a post's emoji reactions grouped by emoji, with the
// reacting usernames, to the builder. usernames maps user IDs to usernames.
func WriteReactions(w *strings.Builder, postID string, reactions []*model.Reaction, usernames map[string]string) {
	fmt.Fprintf(w, "Reactions on post %s:\n", postID)
	if len(reactions) == 0 {
		w.WriteString("(none)\n")
		return
	}

	// Group by emoji name, preserving first-seen order.
	order := make([]string, 0)
	byEmoji := make(map[string][]string)
	for _, r := range reactions {
		if _, ok := byEmoji[r.EmojiName]; !ok {
			order = append(order, r.EmojiName)
		}
		name := usernames[r.UserId]
		if name == "" {
			name = r.UserId
		}
		byEmoji[r.EmojiName] = append(byEmoji[r.EmojiName], name)
	}

	for _, emoji := range order {
		users := byEmoji[emoji]
		fmt.Fprintf(w, ":%s: (%d) — %s\n", emoji, len(users), strings.Join(users, ", "))
	}
}

// EmojiEntry holds data for formatting a single custom emoji.
type EmojiEntry struct {
	HeaderLabel string
	Emoji       *model.Emoji
	CreatorName string
}

// WriteEmoji writes a formatted custom emoji entry to the builder.
func WriteEmoji(w *strings.Builder, entry EmojiEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	fmt.Fprintf(w, "Name: :%s:\n", entry.Emoji.Name)
	fmt.Fprintf(w, "ID: %s\n", entry.Emoji.Id)
	if entry.CreatorName != "" {
		fmt.Fprintf(w, "Created by: %s\n", entry.CreatorName)
	}
	w.WriteString("\n")
}

// WriteTeamUnread writes a single team's unread counts to the builder.
func WriteTeamUnread(w *strings.Builder, unread *model.TeamUnread) {
	fmt.Fprintf(w, "Team %s: %d unread messages, %d mentions, %d unread threads, %d thread mentions\n",
		unread.TeamId, unread.MsgCount, unread.MentionCount, unread.ThreadCount, unread.ThreadMentionCount)
}

// WriteChannelUnread writes a channel's unread counts to the builder.
func WriteChannelUnread(w *strings.Builder, unread *model.ChannelUnread) {
	w.WriteString("Channel Unread Counts:\n")
	fmt.Fprintf(w, "Channel ID: %s\n", unread.ChannelId)
	if unread.TeamId != "" {
		fmt.Fprintf(w, "Team ID: %s\n", unread.TeamId)
	}
	fmt.Fprintf(w, "Unread messages: %d\n", unread.MsgCount)
	fmt.Fprintf(w, "Mentions: %d\n", unread.MentionCount)
}

// ThreadSummaryEntry holds data for formatting a single thread from the inbox.
type ThreadSummaryEntry struct {
	HeaderLabel string
	Thread      *model.ThreadResponse
	Username    string // root post author
}

// WriteThreadSummary writes a collated-thread summary to the builder.
func WriteThreadSummary(w *strings.Builder, entry ThreadSummaryEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	tr := entry.Thread
	fmt.Fprintf(w, "Root Post ID: %s\n", tr.PostId)
	fmt.Fprintf(w, "Replies: %d (unread: %d, unread mentions: %d)\n", tr.ReplyCount, tr.UnreadReplies, tr.UnreadMentions)
	if tr.LastReplyAt > 0 {
		t := time.Unix(tr.LastReplyAt/1000, (tr.LastReplyAt%1000)*int64(time.Millisecond))
		fmt.Fprintf(w, "Last reply: %s\n", t.UTC().Format(time.RFC3339))
	}
	if tr.Post != nil {
		username := entry.Username
		if username == "" {
			username = "Unknown User"
		}
		fmt.Fprintf(w, "By %s: %s\n", username, PostBody(tr.Post))
	}
	w.WriteString("\n")
}

// WriteChannelStats writes a channel's statistics to the builder.
func WriteChannelStats(w *strings.Builder, stats *model.ChannelStats) {
	w.WriteString("Channel Statistics:\n")
	fmt.Fprintf(w, "Channel ID: %s\n", stats.ChannelId)
	fmt.Fprintf(w, "Members: %d\n", stats.MemberCount)
	fmt.Fprintf(w, "Guests: %d\n", stats.GuestCount)
	fmt.Fprintf(w, "Pinned posts: %d\n", stats.PinnedPostCount)
	fmt.Fprintf(w, "Files: %d\n", stats.FilesCount)
}

// ChannelMemberEntry holds data for formatting a single channel membership record.
type ChannelMemberEntry struct {
	HeaderLabel string
	Member      *model.ChannelMember
	Username    string // resolved, optional
}

// WriteChannelMember writes a channel membership record (roles, mute, last-viewed).
func WriteChannelMember(w *strings.Builder, entry ChannelMemberEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	m := entry.Member
	fmt.Fprintf(w, "Channel ID: %s\n", m.ChannelId)
	fmt.Fprintf(w, "User ID: %s\n", m.UserId)
	if entry.Username != "" {
		fmt.Fprintf(w, "Username: %s\n", entry.Username)
	}
	if role := MemberRole(m.SchemeAdmin, m.SchemeGuest, m.SchemeUser); role != "" {
		fmt.Fprintf(w, "Role: %s\n", role)
	}
	muted := m.NotifyProps != nil && m.NotifyProps["mark_unread"] == "mention"
	fmt.Fprintf(w, "Muted: %t\n", muted)
	if m.LastViewedAt > 0 {
		t := time.Unix(m.LastViewedAt/1000, (m.LastViewedAt%1000)*int64(time.Millisecond))
		fmt.Fprintf(w, "Last viewed: %s\n", t.UTC().Format(time.RFC3339))
	}
	w.WriteString("\n")
}

// BookmarkEntry holds data for formatting a single channel bookmark.
type BookmarkEntry struct {
	HeaderLabel string
	Bookmark    *model.ChannelBookmarkWithFileInfo
}

// WriteBookmark writes a formatted channel bookmark entry to the builder.
func WriteBookmark(w *strings.Builder, entry BookmarkEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	b := entry.Bookmark
	fmt.Fprintf(w, "ID: %s\n", b.Id)
	fmt.Fprintf(w, "Name: %s\n", b.DisplayName)
	fmt.Fprintf(w, "Type: %s\n", b.Type)
	if b.LinkUrl != "" {
		fmt.Fprintf(w, "Link: %s\n", b.LinkUrl)
	}
	if b.FileId != "" {
		fmt.Fprintf(w, "File ID: %s\n", b.FileId)
	}
	if b.Emoji != "" {
		fmt.Fprintf(w, "Emoji: %s\n", b.Emoji)
	}
	w.WriteString("\n")
}

// SidebarCategoryEntry holds data for formatting a single sidebar category.
type SidebarCategoryEntry struct {
	HeaderLabel string
	Category    *model.SidebarCategoryWithChannels
}

// WriteSidebarCategory writes a sidebar category and its channel IDs to the builder.
func WriteSidebarCategory(w *strings.Builder, entry SidebarCategoryEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	c := entry.Category
	fmt.Fprintf(w, "ID: %s\n", c.Id)
	fmt.Fprintf(w, "Name: %s\n", c.DisplayName)
	fmt.Fprintf(w, "Type: %s\n", c.Type)
	fmt.Fprintf(w, "Channels (%d): %s\n", len(c.Channels), strings.Join(c.Channels, ", "))
	w.WriteString("\n")
}

// StatusEntry holds data for formatting a single user's presence status.
type StatusEntry struct {
	HeaderLabel string
	Status      *model.Status
	Username    string // resolved, optional
}

// WriteStatus writes a user's presence status to the builder.
func WriteStatus(w *strings.Builder, entry StatusEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	s := entry.Status
	fmt.Fprintf(w, "User ID: %s\n", s.UserId)
	if entry.Username != "" {
		fmt.Fprintf(w, "Username: %s\n", entry.Username)
	}
	fmt.Fprintf(w, "Status: %s\n", s.Status)
	if s.Status == model.StatusDnd && s.DNDEndTime > 0 {
		t := time.Unix(s.DNDEndTime, 0)
		fmt.Fprintf(w, "Do not disturb until: %s\n", t.UTC().Format(time.RFC3339))
	}
	w.WriteString("\n")
}

// WriteCustomStatus writes a user's custom status (emoji + text + expiry).
func WriteCustomStatus(w *strings.Builder, userID string, cs *model.CustomStatus) {
	fmt.Fprintf(w, "Custom status for user %s:\n", userID)
	if cs == nil || (cs.Emoji == "" && cs.Text == "") {
		w.WriteString("(none set)\n")
		return
	}
	if cs.Emoji != "" {
		fmt.Fprintf(w, "Emoji: :%s:\n", cs.Emoji)
	}
	if cs.Text != "" {
		fmt.Fprintf(w, "Text: %s\n", cs.Text)
	}
	if !cs.ExpiresAt.IsZero() {
		fmt.Fprintf(w, "Expires at: %s\n", cs.ExpiresAt.UTC().Format(time.RFC3339))
	}
}

// CPAFieldEntry holds data for formatting a single Custom Profile Attribute field.
type CPAFieldEntry struct {
	HeaderLabel string
	Field       *model.PropertyField
}

// WriteCPAField writes a Custom Profile Attribute field definition to the builder.
func WriteCPAField(w *strings.Builder, entry CPAFieldEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	fmt.Fprintf(w, "ID: %s\n", entry.Field.ID)
	fmt.Fprintf(w, "Name: %s\n", entry.Field.Name)
	fmt.Fprintf(w, "Type: %s\n", entry.Field.Type)
	w.WriteString("\n")
}

// TeamMemberEntry holds data for formatting a single team membership record.
type TeamMemberEntry struct {
	HeaderLabel string
	Member      *model.TeamMember
	Username    string // resolved, optional
}

// WriteTeamMember writes a team membership record (roles) to the builder.
func WriteTeamMember(w *strings.Builder, entry TeamMemberEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	m := entry.Member
	fmt.Fprintf(w, "Team ID: %s\n", m.TeamId)
	fmt.Fprintf(w, "User ID: %s\n", m.UserId)
	if entry.Username != "" {
		fmt.Fprintf(w, "Username: %s\n", entry.Username)
	}
	if role := MemberRole(m.SchemeAdmin, m.SchemeGuest, m.SchemeUser); role != "" {
		fmt.Fprintf(w, "Role: %s\n", role)
	}
	w.WriteString("\n")
}

// WriteTeamStats writes a team's member statistics to the builder.
func WriteTeamStats(w *strings.Builder, stats *model.TeamStats) {
	w.WriteString("Team Statistics:\n")
	fmt.Fprintf(w, "Team ID: %s\n", stats.TeamId)
	fmt.Fprintf(w, "Total members: %d\n", stats.TotalMemberCount)
	fmt.Fprintf(w, "Active members: %d\n", stats.ActiveMemberCount)
}

// BotEntry holds data for formatting a single bot account.
type BotEntry struct {
	HeaderLabel string
	Bot         *model.Bot
}

// WriteBot writes a bot account's details to the builder.
func WriteBot(w *strings.Builder, entry BotEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	b := entry.Bot
	fmt.Fprintf(w, "User ID: %s\n", b.UserId)
	fmt.Fprintf(w, "Username: %s\n", b.Username)
	if b.DisplayName != "" {
		fmt.Fprintf(w, "Display Name: %s\n", b.DisplayName)
	}
	if b.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", b.Description)
	}
	fmt.Fprintf(w, "Owner ID: %s\n", b.OwnerId)
	if b.DeleteAt != 0 {
		w.WriteString("Disabled: true\n")
	}
	w.WriteString("\n")
}

// GroupEntry holds data for formatting a single group.
type GroupEntry struct {
	HeaderLabel string
	Group       *model.Group
}

// WriteGroup writes a group's metadata to the builder.
func WriteGroup(w *strings.Builder, entry GroupEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	g := entry.Group
	fmt.Fprintf(w, "ID: %s\n", g.Id)
	if g.Name != nil && *g.Name != "" {
		fmt.Fprintf(w, "Name: %s\n", *g.Name)
	}
	fmt.Fprintf(w, "Display Name: %s\n", g.DisplayName)
	fmt.Fprintf(w, "Source: %s\n", g.Source)
	if g.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", g.Description)
	}
	if g.MemberCount != nil {
		fmt.Fprintf(w, "Member Count: %d\n", *g.MemberCount)
	}
	w.WriteString("\n")
}

// RoleEntry holds data for formatting a single role.
type RoleEntry struct {
	Role *model.Role
}

// WriteRole writes a role definition and its permissions to the builder.
func WriteRole(w *strings.Builder, entry RoleEntry) {
	r := entry.Role
	w.WriteString("Role:\n")
	fmt.Fprintf(w, "ID: %s\n", r.Id)
	fmt.Fprintf(w, "Name: %s\n", r.Name)
	fmt.Fprintf(w, "Display Name: %s\n", r.DisplayName)
	fmt.Fprintf(w, "Permissions (%d): %s\n", len(r.Permissions), strings.Join(r.Permissions, ", "))
	w.WriteString("\n")
}

// IncomingWebhookEntry holds data for formatting an incoming webhook.
type IncomingWebhookEntry struct {
	HeaderLabel string
	Webhook     *model.IncomingWebhook
}

// WriteIncomingWebhook writes an incoming webhook's details to the builder.
func WriteIncomingWebhook(w *strings.Builder, entry IncomingWebhookEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	h := entry.Webhook
	fmt.Fprintf(w, "ID: %s\n", h.Id)
	fmt.Fprintf(w, "Display Name: %s\n", h.DisplayName)
	fmt.Fprintf(w, "Channel ID: %s\n", h.ChannelId)
	fmt.Fprintf(w, "Team ID: %s\n", h.TeamId)
	if h.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", h.Description)
	}
	w.WriteString("\n")
}

// OutgoingWebhookEntry holds data for formatting an outgoing webhook.
type OutgoingWebhookEntry struct {
	HeaderLabel string
	Webhook     *model.OutgoingWebhook
}

// WriteOutgoingWebhook writes an outgoing webhook's details to the builder.
func WriteOutgoingWebhook(w *strings.Builder, entry OutgoingWebhookEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}
	h := entry.Webhook
	fmt.Fprintf(w, "ID: %s\n", h.Id)
	fmt.Fprintf(w, "Display Name: %s\n", h.DisplayName)
	fmt.Fprintf(w, "Channel ID: %s\n", h.ChannelId)
	fmt.Fprintf(w, "Team ID: %s\n", h.TeamId)
	if len(h.TriggerWords) > 0 {
		fmt.Fprintf(w, "Trigger words: %s\n", strings.Join(h.TriggerWords, ", "))
	}
	if len(h.CallbackURLs) > 0 {
		fmt.Fprintf(w, "Callback URLs: %s\n", strings.Join(h.CallbackURLs, ", "))
	}
	w.WriteString("\n")
}

// WriteChannelModerations writes a channel's moderation settings to the builder.
func WriteChannelModerations(w *strings.Builder, moderations []*model.ChannelModeration) {
	w.WriteString("Channel Moderations:\n")
	for _, m := range moderations {
		if m == nil {
			continue
		}
		members := false
		guests := false
		if m.Roles != nil {
			if m.Roles.Members != nil {
				members = m.Roles.Members.Value
			}
			if m.Roles.Guests != nil {
				guests = m.Roles.Guests.Value
			}
		}
		fmt.Fprintf(w, "- %s: members=%t, guests=%t\n", m.Name, members, guests)
	}
}

// BuildPostIndex creates a map from post ID to its 1-based display index.
// Used to generate "(reply to Post N)" annotations.
func BuildPostIndex(posts []*model.Post) map[string]int {
	idx := make(map[string]int, len(posts))
	for i, p := range posts {
		idx[p.Id] = i + 1
	}
	return idx
}

// MemberRole converts scheme booleans to a readable role string.
// Works for both channel and team members.
func MemberRole(schemeAdmin, schemeGuest, schemeUser bool) string {
	switch {
	case schemeAdmin:
		return "admin"
	case schemeGuest:
		return "guest"
	case schemeUser:
		return "member"
	default:
		return ""
	}
}

// UserEntry holds data for formatting a single user.
type UserEntry struct {
	HeaderLabel string      // e.g. "User 1"; empty for member lists
	User        *model.User // the source user
	Role        string      // "admin", "member", "guest", "" — from MemberRole
}

// WriteUser writes a formatted user entry to the builder.
func WriteUser(w *strings.Builder, entry UserEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "**%s**:\n", entry.HeaderLabel)
	}

	fmt.Fprintf(w, "Username: %s\n", entry.User.Username)
	fmt.Fprintf(w, "ID: %s\n", entry.User.Id)

	if entry.User.FirstName != "" || entry.User.LastName != "" {
		name := strings.TrimSpace(entry.User.FirstName + " " + entry.User.LastName)
		fmt.Fprintf(w, "Name: %s\n", name)
	}

	if entry.User.Email != "" {
		fmt.Fprintf(w, "Email: %s\n", entry.User.Email)
	}

	if entry.User.Nickname != "" {
		fmt.Fprintf(w, "Nickname: %s\n", entry.User.Nickname)
	}

	if entry.User.Position != "" {
		fmt.Fprintf(w, "Position: %s\n", entry.User.Position)
	}

	if entry.User.IsBot {
		w.WriteString("Is Bot: true\n")
	}

	if entry.User.DeleteAt != 0 {
		w.WriteString("Deactivated: true\n")
	}

	if entry.Role != "" {
		fmt.Fprintf(w, "Role: %s\n", entry.Role)
	}

	w.WriteString("\n")
}

// ChannelEntry holds data for formatting a single channel.
type ChannelEntry struct {
	HeaderLabel string         // e.g. "Channel Information:", "1. **General**"; empty to omit
	Channel     *model.Channel // the source channel
	TeamName    string         // resolved team display name
	TeamID      string         // team ID (shown when TeamName is empty but TeamID is set)
	MemberCount int64          // -1 means don't show
	Role        string         // requesting user's role: "admin" | "member" | "guest" | "not_member" | "" (omit)
}

// WriteChannel writes a formatted channel entry to the builder.
func WriteChannel(w *strings.Builder, entry ChannelEntry) {
	if entry.HeaderLabel != "" {
		fmt.Fprintf(w, "%s\n", entry.HeaderLabel)
	}

	fmt.Fprintf(w, "ID: %s\n", entry.Channel.Id)
	fmt.Fprintf(w, "Name: %s\n", entry.Channel.Name)
	fmt.Fprintf(w, "Display Name: %s\n", entry.Channel.DisplayName)
	fmt.Fprintf(w, "Type: %s\n", entry.Channel.Type)

	if entry.TeamName != "" {
		fmt.Fprintf(w, "Team: %s (ID: %s)\n", entry.TeamName, entry.Channel.TeamId)
	} else if entry.TeamID != "" {
		fmt.Fprintf(w, "Team ID: %s\n", entry.TeamID)
	}

	if entry.Channel.Purpose != "" {
		fmt.Fprintf(w, "Purpose: %s\n", entry.Channel.Purpose)
	}
	if entry.Channel.Header != "" {
		fmt.Fprintf(w, "Header: %s\n", entry.Channel.Header)
	}

	if entry.Channel.CreateAt > 0 {
		t := time.Unix(entry.Channel.CreateAt/1000, (entry.Channel.CreateAt%1000)*int64(time.Millisecond))
		fmt.Fprintf(w, "Created: %s\n", t.UTC().Format(time.RFC3339))
	}

	if entry.MemberCount >= 0 {
		fmt.Fprintf(w, "Member Count: %d\n", entry.MemberCount)
	}

	if entry.Role != "" {
		fmt.Fprintf(w, "Your role: %s\n", entry.Role)
	}

	w.WriteString("\n")
}

// TeamEntry holds data for formatting a single team.
type TeamEntry struct {
	Team        *model.Team // the source team
	MemberCount int64       // -1 means don't show
}

// WriteTeam writes a formatted team entry to the builder.
func WriteTeam(w *strings.Builder, entry TeamEntry) {
	w.WriteString("Team Information:\n")
	fmt.Fprintf(w, "ID: %s\n", entry.Team.Id)
	fmt.Fprintf(w, "Name: %s\n", entry.Team.Name)
	fmt.Fprintf(w, "Display Name: %s\n", entry.Team.DisplayName)
	fmt.Fprintf(w, "Type: %s\n", entry.Team.Type)

	if entry.Team.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", entry.Team.Description)
	}

	if entry.Team.CreateAt > 0 {
		t := time.Unix(entry.Team.CreateAt/1000, (entry.Team.CreateAt%1000)*int64(time.Millisecond))
		fmt.Fprintf(w, "Created: %s\n", t.UTC().Format(time.RFC3339))
	}

	if entry.MemberCount >= 0 {
		fmt.Fprintf(w, "Member Count: %d\n", entry.MemberCount)
	}
}

// FileDescriptorEntry holds metadata for a file attachment surfaced to the LLM
// without inlining its contents. The File ID is included so the model can pass
// it to the read_file tool to fetch the contents on demand.
type FileDescriptorEntry struct {
	// Number is the 1-based position of the file in the attachment list; it
	// renders as an "Attached File N" header. A zero value omits the header.
	Number   int
	FileInfo *model.FileInfo // the source file
}

// WriteFileDescriptor writes a compact file metadata descriptor to the builder.
func WriteFileDescriptor(w *strings.Builder, entry FileDescriptorEntry) {
	if entry.Number > 0 {
		fmt.Fprintf(w, "**Attached File %d**:\n", entry.Number)
	}

	fmt.Fprintf(w, "Name: %s\n", entry.FileInfo.Name)
	fmt.Fprintf(w, "File ID: %s\n", entry.FileInfo.Id)

	if entry.FileInfo.MimeType != "" {
		fmt.Fprintf(w, "Type: %s\n", entry.FileInfo.MimeType)
	}

	fmt.Fprintf(w, "Size: %d bytes\n", entry.FileInfo.Size)

	w.WriteString("\n")
}
