// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/mmapi"
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
