// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/public/mcptool"
	"github.com/mattermost/mattermost/server/public/model"
)

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

// AppendPluginAnnotations appends non-empty trimmed annotation lines to base,
// separated by newlines. Returns base unchanged if there are no annotations.
func AppendPluginAnnotations(base string, anns []string) string {
	if len(anns) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	for _, ann := range anns {
		s := strings.TrimSpace(ann)
		if s == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(s)
	}
	return b.String()
}

// SearchUsersOutput formats the search_users tool output for LLM consumption.
func SearchUsersOutput(o mcptool.SearchUsersOutput) (string, error) {
	if len(o.Users) == 0 {
		return AppendPluginAnnotations("no users found matching the search criteria", o.PluginAnnotations), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d users matching '%s':\n\n", len(o.Users), o.Term)
	for i, user := range o.Users {
		WriteUser(&b, UserEntry{
			HeaderLabel: fmt.Sprintf("User %d", i+1),
			User:        user,
		})
	}
	return AppendPluginAnnotations(b.String(), o.PluginAnnotations), nil
}

// SearchPostsOutput formats the search_posts tool output for LLM consumption.
func SearchPostsOutput(o mcptool.SearchPostsOutput) (string, error) {
	totalSemantic := len(o.SemanticResults)
	totalKeyword := len(o.KeywordResults)
	total := totalSemantic + totalKeyword

	if total == 0 {
		var msg string
		if len(o.Terms) > 2 {
			msg = fmt.Sprintf("No posts found for %q (%d terms). All terms must appear in a single post — try fewer terms (1-2).", o.Query, len(o.Terms))
		} else {
			msg = fmt.Sprintf("No posts found for %q.", o.Query)
		}
		return AppendPluginAnnotations(msg, o.PluginAnnotations), nil
	}

	var result strings.Builder

	noun := "results"
	if total == 1 {
		noun = "result"
	}
	if o.SemanticEnabled {
		fmt.Fprintf(&result, "Found %d %s for \"%s\" (%d semantic, %d keyword):\n", total, noun, o.Query, totalSemantic, totalKeyword)
	} else {
		fmt.Fprintf(&result, "Found %d %s for \"%s\":\n", total, noun, o.Query)
	}

	if o.ChannelIDFilter != "" {
		fmt.Fprintf(&result, "Channel ID filter: %s\n", o.ChannelIDFilter)
	}

	if o.SemanticEnabled && totalSemantic > 0 {
		result.WriteString("\n## Semantic Search Results\n\n")
		for i, r := range o.SemanticResults {
			writeSearchPostResult(&result, i+1, r, true, o.ChannelIDFilter)
		}
	}

	if totalKeyword > 0 {
		if o.SemanticEnabled {
			result.WriteString("\n## Keyword Search Results\n\n")
		} else {
			result.WriteString("\n")
		}
		for i, r := range o.KeywordResults {
			writeSearchPostResult(&result, i+1, r, false, o.ChannelIDFilter)
		}
	}

	return AppendPluginAnnotations(result.String(), o.PluginAnnotations), nil
}

func writeSearchPostResult(w *strings.Builder, index int, r mcptool.SearchPostResult, includeScore bool, channelIDFilter string) {
	var score float32
	if includeScore {
		score = r.Score
	}
	username := r.Username
	if username != "" {
		username = "@" + username
	}
	WritePost(w, PostEntry{
		HeaderLabel: fmt.Sprintf("Result %d", index),
		Username:    username,
		Score:       score,
		Post:        r.Post,
		ChannelName: r.ChannelName,
		TeamName:    r.TeamName,
		ShowChannel: channelIDFilter == "",
	})
}

// ReadChannelOutput formats the read_channel tool output for LLM consumption.
func ReadChannelOutput(o mcptool.ReadChannelOutput) (string, error) {
	if o.Channel == nil {
		return AppendPluginAnnotations("no channel data available", o.PluginAnnotations), nil
	}
	ch := o.Channel
	channelDisplayName := ch.DisplayName
	if channelDisplayName == "" {
		switch ch.Type {
		case model.ChannelTypeDirect:
			channelDisplayName = "Direct Message"
		case model.ChannelTypeGroup:
			channelDisplayName = "Group Message"
		default:
			channelDisplayName = ch.Name
		}
	}

	if len(o.Posts) == 0 {
		return AppendPluginAnnotations("no posts found in the specified timeframe", o.PluginAnnotations), nil
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Channel: %s (Team: %s)\n", channelDisplayName, o.TeamName)
	fmt.Fprintf(&result, "Found %d posts:\n\n", len(o.Posts))

	postIndex := BuildPostIndex(o.Posts)
	for i, post := range o.Posts {
		var replyAnnotation string
		if post.RootId != "" {
			if parentNum, ok := postIndex[post.RootId]; ok {
				replyAnnotation = fmt.Sprintf("(reply to Post %d)", parentNum)
			}
		}
		username := "Unknown User"
		if u := o.Users[post.UserId]; u != nil && u.Username != "" {
			username = u.Username
		}
		WritePost(&result, PostEntry{
			HeaderLabel:     fmt.Sprintf("Post %d", i+1),
			Username:        username,
			ReplyAnnotation: replyAnnotation,
			Post:            post,
		})
	}

	return AppendPluginAnnotations(result.String(), o.PluginAnnotations), nil
}

// ReadPostOutput formats the read_post tool output for LLM consumption.
func ReadPostOutput(o mcptool.ReadPostOutput) (string, error) {
	if len(o.Posts) == 0 {
		return AppendPluginAnnotations("no posts found", o.PluginAnnotations), nil
	}

	var result strings.Builder
	if o.ChannelName != "" && o.TeamName != "" {
		fmt.Fprintf(&result, "Channel: %s (Team: %s)\n", o.ChannelName, o.TeamName)
	}

	if len(o.Posts) > 0 {
		fmt.Fprintf(&result, "Channel ID: %s\n", o.Posts[0].ChannelId)

		var rootID string
		for _, post := range o.Posts {
			if post.RootId != "" {
				rootID = post.RootId
				break
			}
		}

		if rootID != "" {
			fmt.Fprintf(&result, "Root ID: %s\n", rootID)
		}
	}
	result.WriteString("\n")

	if o.IncludeThread && len(o.Posts) > 1 {
		fmt.Fprintf(&result, "Thread with %d posts:\n\n", len(o.Posts))
	}

	for i, post := range o.Posts {
		username := ""
		if u := o.Users[post.UserId]; u != nil {
			username = u.Username
		}
		WritePost(&result, PostEntry{
			HeaderLabel: fmt.Sprintf("Post %d", i+1),
			Username:    username,
			Post:        post,
		})
	}

	return AppendPluginAnnotations(result.String(), o.PluginAnnotations), nil
}

// ChannelInfoOutput formats the get_channel_info tool output for LLM consumption.
func ChannelInfoOutput(o mcptool.ChannelInfoOutput) (string, error) {
	switch len(o.Channels) {
	case 0:
		return AppendPluginAnnotations("no channel information available", o.PluginAnnotations), nil

	case 1:
		return AppendPluginAnnotations(formatSingleChannel(o), o.PluginAnnotations), nil

	default:
		return AppendPluginAnnotations(formatMultipleChannels(o), o.PluginAnnotations), nil
	}
}

func formatSingleChannel(o mcptool.ChannelInfoOutput) string {
	channel := o.Channels[0]
	teamName := ""
	if team := o.TeamByID[channel.TeamId]; team != nil {
		teamName = team.DisplayName
	}
	memberCount := int64(-1)
	if c, ok := o.MemberCountByChannelID[channel.Id]; ok {
		memberCount = c
	}

	var result strings.Builder
	WriteChannel(&result, ChannelEntry{
		HeaderLabel: "Channel Information:",
		Channel:     channel,
		TeamName:    teamName,
		TeamID:      channel.TeamId,
		MemberCount: memberCount,
		Role:        o.ChannelRoleByID[channel.Id],
	})
	return result.String()
}

func formatMultipleChannels(o mcptool.ChannelInfoOutput) string {
	var result strings.Builder
	fmt.Fprintf(&result, "Found %d channels with matching name:\n\n", len(o.Channels))

	for i, channel := range o.Channels {
		teamName := ""
		if team := o.TeamByID[channel.TeamId]; team != nil {
			teamName = team.DisplayName
		}
		memberCount := int64(-1)
		if c, ok := o.MemberCountByChannelID[channel.Id]; ok {
			memberCount = c
		}

		WriteChannel(&result, ChannelEntry{
			HeaderLabel: fmt.Sprintf("%d. %s", i+1, channel.DisplayName),
			Channel:     channel,
			TeamName:    teamName,
			TeamID:      channel.TeamId,
			MemberCount: memberCount,
			Role:        o.ChannelRoleByID[channel.Id],
		})
	}

	result.WriteString("Multiple channels found. To disambiguate, either:\n")
	result.WriteString("- Specify which team's channel you need\n")
	result.WriteString("- Call get_channel_info again with the team_id parameter\n")
	result.WriteString("- Use the specific channel_id from above in create_post\n")

	return result.String()
}

// ChannelMembersOutput formats the get_channel_members tool output for LLM consumption.
func ChannelMembersOutput(o mcptool.ChannelMembersOutput) (string, error) {
	if len(o.Rows) == 0 {
		return AppendPluginAnnotations("no members found in this channel", o.PluginAnnotations), nil
	}

	var result strings.Builder
	botsExcluded := 0
	written := 0

	for _, row := range o.Rows {
		user := row.User
		if user == nil {
			continue
		}
		if o.ExcludeBots && user.IsBot {
			botsExcluded++
			continue
		}

		if user.Username == "details unavailable" {
			WriteUser(&result, UserEntry{User: user})
		} else {
			WriteUser(&result, UserEntry{
				User: user,
				Role: MemberRole(row.SchemeAdmin, row.SchemeGuest, row.SchemeUser),
			})
		}
		written++
	}

	var header strings.Builder
	fmt.Fprintf(&header, "Channel Members (page %d, showing %d members):\n", o.Page, written)

	var footer string
	if botsExcluded > 0 {
		footer = fmt.Sprintf("\n(%d bot account(s) excluded — set exclude_bots=false to include them)\n", botsExcluded)
	}

	body := header.String() + result.String() + footer
	return AppendPluginAnnotations(body, o.PluginAnnotations), nil
}

// UserChannelsOutput formats the get_user_channels tool output for LLM consumption.
func UserChannelsOutput(o mcptool.UserChannelsOutput) (string, error) {
	if len(o.Channels) == 0 {
		msg := fmt.Sprintf("No channels found (page %d, %d total channels).", o.PageInfo.Page, o.PageInfo.TotalCount)
		return AppendPluginAnnotations(msg, o.PluginAnnotations), nil
	}

	start := o.PageInfo.Page * o.PageInfo.PerPage

	var result strings.Builder
	fmt.Fprintf(&result, "User Channels (page %d, showing %d of %d channels):\n\n", o.PageInfo.Page, len(o.Channels), o.PageInfo.TotalCount)

	for i, channel := range o.Channels {
		displayName := channel.DisplayName
		if displayName == "" {
			switch channel.Type {
			case model.ChannelTypeDirect:
				displayName = "Direct Message"
			case model.ChannelTypeGroup:
				displayName = "Group Message"
			default:
				displayName = channel.Name
			}
		}

		var teamName, teamID string
		if channel.TeamId != "" {
			if team, ok := o.TeamInfoByID[channel.TeamId]; ok && team != nil && team.DisplayName != "" {
				teamName = team.DisplayName
			}
			teamID = channel.TeamId
		}

		WriteChannel(&result, ChannelEntry{
			HeaderLabel: fmt.Sprintf("%d. **%s**", i+1+start, displayName),
			Channel:     channel,
			TeamName:    teamName,
			TeamID:      teamID,
			MemberCount: -1,
		})
	}

	if o.PageInfo.HasMore {
		fmt.Fprintf(&result, "Page %d of results shown. More channels available — use page=%d to see the next page.\n", o.PageInfo.Page, o.PageInfo.Page+1)
	}

	return AppendPluginAnnotations(result.String(), o.PluginAnnotations), nil
}

// CreateChannelOutput formats the create_channel tool output for LLM consumption.
func CreateChannelOutput(o mcptool.CreateChannelOutput) (string, error) {
	if o.Channel == nil {
		return AppendPluginAnnotations("channel created (no channel details available)", o.PluginAnnotations), nil
	}
	base := fmt.Sprintf("Successfully created channel '%s' with ID: %s", o.Channel.DisplayName, o.Channel.Id)
	return AppendPluginAnnotations(base, o.PluginAnnotations), nil
}

// AddUserToChannelOutput formats the add_user_to_channel tool output for LLM consumption.
func AddUserToChannelOutput(o mcptool.AddUserToChannelOutput) (string, error) {
	var base string
	if o.User != nil && o.Channel != nil {
		base = fmt.Sprintf("Successfully added user '%s' to channel '%s'", o.User.Username, o.Channel.DisplayName)
	} else {
		base = fmt.Sprintf("Successfully added user %s to channel %s", o.UserID, o.ChannelID)
	}
	return AppendPluginAnnotations(base, o.PluginAnnotations), nil
}

// CreatePostOutput formats the create_post tool output for LLM consumption.
func CreatePostOutput(o mcptool.CreatePostOutput) (string, error) {
	if o.Post == nil {
		return AppendPluginAnnotations("post created (no post details available)", o.PluginAnnotations), nil
	}
	channelName := ""
	if o.Channel != nil {
		channelName = o.Channel.DisplayName
	}
	teamName := ""
	if o.Team != nil {
		teamName = o.Team.DisplayName
	}
	base := fmt.Sprintf("Successfully created post in channel '%s' (Team: %s) with ID: %s%s",
		channelName, teamName, o.Post.Id, o.AttachmentMessage)
	return AppendPluginAnnotations(base, o.PluginAnnotations), nil
}

// CreatePostAsUserOutput formats the create_post_as_user tool output for LLM consumption.
func CreatePostAsUserOutput(o mcptool.CreatePostAsUserOutput) (string, error) {
	if o.Post == nil {
		return AppendPluginAnnotations("post created (no post details available)", o.PluginAnnotations), nil
	}
	base := fmt.Sprintf("Successfully created post with ID %s as user %s%s", o.Post.Id, o.Username, o.AttachmentMessage)
	return AppendPluginAnnotations(base, o.PluginAnnotations), nil
}

// DMOutput formats the dm tool output for LLM consumption.
func DMOutput(o mcptool.DMOutput) (string, error) {
	if o.Post == nil {
		return AppendPluginAnnotations("DM sent (no post details available)", o.PluginAnnotations), nil
	}
	var base string
	if o.DMSelf {
		base = fmt.Sprintf("Successfully sent DM to yourself with ID: %s%s", o.Post.Id, o.AttachmentMessage)
	} else {
		username := ""
		if o.TargetUser != nil {
			username = o.TargetUser.Username
		}
		base = fmt.Sprintf("Successfully sent DM to @%s with ID: %s%s", username, o.Post.Id, o.AttachmentMessage)
	}
	return AppendPluginAnnotations(base, o.PluginAnnotations), nil
}

// GroupMessageOutput formats the group_message tool output for LLM consumption.
func GroupMessageOutput(o mcptool.GroupMessageOutput) (string, error) {
	if o.Post == nil {
		return AppendPluginAnnotations("group message sent (no post details available)", o.PluginAnnotations), nil
	}
	handles := make([]string, 0, len(o.Usernames))
	for _, uname := range o.Usernames {
		handles = append(handles, "@"+uname)
	}
	base := fmt.Sprintf("Successfully sent group message to %s with ID: %s%s",
		strings.Join(handles, ", "), o.Post.Id, o.AttachmentMessage)
	return AppendPluginAnnotations(base, o.PluginAnnotations), nil
}

// TeamInfoOutput formats the get_team_info tool output for LLM consumption.
func TeamInfoOutput(o mcptool.TeamInfoOutput) (string, error) {
	switch len(o.Teams) {
	case 0:
		return AppendPluginAnnotations("no team information available", o.PluginAnnotations), nil

	case 1:
		var result strings.Builder
		WriteTeam(&result, TeamEntry{
			Team:        o.Teams[0],
			MemberCount: o.MemberCount,
		})
		return AppendPluginAnnotations(result.String(), o.PluginAnnotations), nil

	default:
		return AppendPluginAnnotations(formatTeamDisambiguation(o.Teams), o.PluginAnnotations), nil
	}
}

func formatTeamDisambiguation(teams []*model.Team) string {
	var msg strings.Builder
	msg.WriteString("Multiple teams match. Please specify which one by calling get_team_info with team_id:\n\n")
	for _, t := range teams {
		fmt.Fprintf(&msg, "- '%s' (URL name: %s, ID: %s)\n", t.DisplayName, t.Name, t.Id)
	}
	return msg.String()
}

// TeamMembersOutput formats the get_team_members tool output for LLM consumption.
func TeamMembersOutput(o mcptool.TeamMembersOutput) (string, error) {
	if len(o.Rows) == 0 {
		return AppendPluginAnnotations("no members found in this team", o.PluginAnnotations), nil
	}

	var body strings.Builder
	botsExcluded := 0
	written := 0

	for _, row := range o.Rows {
		user := row.User
		if user == nil {
			continue
		}
		if o.ExcludeBots && user.IsBot {
			botsExcluded++
			continue
		}
		if user.Username == "details unavailable" {
			WriteUser(&body, UserEntry{User: user})
		} else {
			WriteUser(&body, UserEntry{
				User: user,
				Role: MemberRole(row.SchemeAdmin, row.SchemeGuest, row.SchemeUser),
			})
		}
		written++
	}

	var header strings.Builder
	fmt.Fprintf(&header, "Team Members (page %d, showing %d members):\n", o.Page, written)

	var footer string
	if botsExcluded > 0 {
		footer = fmt.Sprintf("\n(%d bot account(s) excluded — set exclude_bots=false to include them)\n", botsExcluded)
	}

	return AppendPluginAnnotations(header.String()+body.String()+footer, o.PluginAnnotations), nil
}

// CreateTeamOutput formats the create_team tool output for LLM consumption.
func CreateTeamOutput(o mcptool.CreateTeamOutput) (string, error) {
	if o.Team == nil {
		return AppendPluginAnnotations("team created (no team details available)", o.PluginAnnotations), nil
	}
	base := fmt.Sprintf("Successfully created team '%s' with ID: %s%s", o.Team.DisplayName, o.Team.Id, o.TeamIconMessage)
	return AppendPluginAnnotations(base, o.PluginAnnotations), nil
}

// AddUserToTeamOutput formats the add_user_to_team tool output for LLM consumption.
func AddUserToTeamOutput(o mcptool.AddUserToTeamOutput) (string, error) {
	var base string
	if o.User != nil && o.Team != nil {
		base = fmt.Sprintf("Successfully added user '%s' to team '%s'", o.User.Username, o.Team.DisplayName)
	} else {
		base = fmt.Sprintf("Successfully added user %s to team %s", o.UserID, o.TeamID)
	}
	return AppendPluginAnnotations(base, o.PluginAnnotations), nil
}

// CreateUserOutput formats the create_user tool output for LLM consumption.
func CreateUserOutput(o mcptool.CreateUserOutput) (string, error) {
	if o.User == nil {
		return AppendPluginAnnotations("user created (no user details available)", o.PluginAnnotations), nil
	}
	base := fmt.Sprintf("Successfully created user '%s' with ID: %s%s", o.User.Username, o.User.Id, o.ProfileImageMessage)
	return AppendPluginAnnotations(base, o.PluginAnnotations), nil
}

// ListAgentsOutput formats the list_agents tool output for LLM consumption.
func ListAgentsOutput(o mcptool.ListAgentsOutput) (string, error) {
	if len(o.Agents) == 0 {
		return AppendPluginAnnotations("No agents are currently configured.", o.PluginAnnotations), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d agent(s):\n\n", len(o.Agents))
	for i, a := range o.Agents {
		fmt.Fprintf(&b, "%d. %s\n", i+1, a.DisplayName)
		fmt.Fprintf(&b, "   ID: %s\n", a.ID)
		fmt.Fprintf(&b, "   Username: @%s\n", a.Username)
		if o.CurrentBotUserID != "" && a.ID == o.CurrentBotUserID {
			b.WriteString("   ** This is YOU (the current agent) **\n")
		}
		b.WriteString("\n")
	}
	return AppendPluginAnnotations(b.String(), o.PluginAnnotations), nil
}
