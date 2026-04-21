// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcptool

import "github.com/mattermost/mattermost/server/public/model"

// SearchPostResult is one row in search_posts semantic or keyword result lists.
type SearchPostResult struct {
	Post        *model.Post `json:"Post"`
	ChannelName string      `json:"ChannelName"`
	TeamName    string      `json:"TeamName"`
	Username    string      `json:"Username"`
	Score       float32     `json:"Score"`
	Source      string      `json:"Source"` // "semantic" or "keyword"
}

// SearchPostsOutput is the after-hook DTO for the search_posts tool.
type SearchPostsOutput struct {
	Query             string             `json:"query"`
	ChannelIDFilter   string             `json:"channel_id_filter,omitempty"`
	SemanticEnabled   bool               `json:"semantic_enabled"`
	SemanticResults   []SearchPostResult `json:"semantic_results,omitempty"`
	KeywordResults    []SearchPostResult `json:"keyword_results,omitempty"`
	Terms             []string           `json:"terms,omitempty"`
	PluginAnnotations []string           `json:"plugin_annotations,omitempty"`
}

// ReadChannelOutput is the after-hook DTO for the read_channel tool.
type ReadChannelOutput struct {
	Channel           *model.Channel         `json:"channel"`
	Posts             []*model.Post          `json:"posts"`
	Users             map[string]*model.User `json:"users,omitempty"`
	TeamName          string                 `json:"team_name,omitempty"`
	PluginAnnotations []string               `json:"plugin_annotations,omitempty"`
}

// ChannelInfoOutput is the after-hook DTO for the get_channel_info tool.
//
// Channels holds the resolved channel(s):
//   - len(Channels) == 1: unique match.
//   - len(Channels) > 1: multiple candidates from a name lookup (disambiguation).
//
// TeamByID and MemberCountByChannelID provide enrichment data keyed by the
// returned channels. Not-found and input-validation failures are returned as
// tool errors instead.
type ChannelInfoOutput struct {
	Channels               []*model.Channel       `json:"channels"`
	TeamByID               map[string]*model.Team `json:"team_by_id,omitempty"`
	MemberCountByChannelID map[string]int64       `json:"member_count_by_channel_id,omitempty"`
	ChannelRoleByID        map[string]string      `json:"channel_role_by_id,omitempty"`
	PluginAnnotations      []string               `json:"plugin_annotations,omitempty"`
}

// UserChannelsPageInfo describes pagination for get_user_channels output.
type UserChannelsPageInfo struct {
	Page       int  `json:"page"`
	PerPage    int  `json:"per_page"`
	TotalCount int  `json:"total_count"`
	HasMore    bool `json:"has_more"`
}

// UserChannelsOutput is the after-hook DTO for the get_user_channels tool.
type UserChannelsOutput struct {
	Channels          []*model.Channel       `json:"channels"`
	PageInfo          UserChannelsPageInfo   `json:"page_info"`
	TeamInfoByID      map[string]*model.Team `json:"team_info_by_id,omitempty"`
	PluginAnnotations []string               `json:"plugin_annotations,omitempty"`
}

// ChannelMemberRow is one member row for get_channel_members (user + role flags).
type ChannelMemberRow struct {
	User        *model.User `json:"user"`
	SchemeAdmin bool        `json:"scheme_admin"`
	SchemeGuest bool        `json:"scheme_guest"`
	SchemeUser  bool        `json:"scheme_user"`
}

// ChannelMembersOutput is the after-hook DTO for the get_channel_members tool.
type ChannelMembersOutput struct {
	Channel           *model.Channel     `json:"channel"`
	Rows              []ChannelMemberRow `json:"rows"`
	Page              int                `json:"page"`
	ExcludeBots       bool               `json:"exclude_bots"`
	PluginAnnotations []string           `json:"plugin_annotations,omitempty"`
}

// SearchUsersOutput is the after-hook DTO for the search_users tool.
type SearchUsersOutput struct {
	Term              string        `json:"term"`
	Users             []*model.User `json:"users"`
	PluginAnnotations []string      `json:"plugin_annotations,omitempty"`
}

// ReadPostOutput is the after-hook DTO for the read_post tool.
type ReadPostOutput struct {
	Posts             []*model.Post          `json:"posts"`
	Users             map[string]*model.User `json:"users,omitempty"`
	ChannelID         string                 `json:"channel_id,omitempty"`
	ChannelName       string                 `json:"channel_name,omitempty"`
	TeamName          string                 `json:"team_name,omitempty"`
	IncludeThread     bool                   `json:"include_thread"`
	PluginAnnotations []string               `json:"plugin_annotations,omitempty"`
}

// CreateChannelOutput is the after-hook DTO for the create_channel tool.
type CreateChannelOutput struct {
	Channel           *model.Channel `json:"channel"`
	PluginAnnotations []string       `json:"plugin_annotations,omitempty"`
}

// AddUserToChannelOutput is the after-hook DTO for the add_user_to_channel tool.
type AddUserToChannelOutput struct {
	UserID            string         `json:"user_id"`
	ChannelID         string         `json:"channel_id"`
	User              *model.User    `json:"user,omitempty"`
	Channel           *model.Channel `json:"channel,omitempty"`
	PluginAnnotations []string       `json:"plugin_annotations,omitempty"`
}

// CreatePostOutput is the after-hook DTO for the create_post tool.
type CreatePostOutput struct {
	Post              *model.Post    `json:"post"`
	Channel           *model.Channel `json:"channel,omitempty"`
	Team              *model.Team    `json:"team,omitempty"`
	AttachmentMessage string         `json:"attachment_message,omitempty"`
	PluginAnnotations []string       `json:"plugin_annotations,omitempty"`
}

// CreatePostAsUserOutput is the after-hook DTO for the create_post_as_user tool.
type CreatePostAsUserOutput struct {
	Post              *model.Post `json:"post"`
	Username          string      `json:"username"`
	AttachmentMessage string      `json:"attachment_message,omitempty"`
	PluginAnnotations []string    `json:"plugin_annotations,omitempty"`
}

// DMOutput is the after-hook DTO for the dm tool.
type DMOutput struct {
	Post              *model.Post `json:"post"`
	TargetUser        *model.User `json:"target_user,omitempty"`
	DMSelf            bool        `json:"dm_self"`
	AttachmentMessage string      `json:"attachment_message,omitempty"`
	PluginAnnotations []string    `json:"plugin_annotations,omitempty"`
}

// GroupMessageOutput is the after-hook DTO for the group_message tool.
type GroupMessageOutput struct {
	Post              *model.Post `json:"post"`
	Usernames         []string    `json:"usernames"`
	AttachmentMessage string      `json:"attachment_message,omitempty"`
	PluginAnnotations []string    `json:"plugin_annotations,omitempty"`
}

// TeamInfoOutput is the after-hook DTO for the get_team_info tool.
//
// Teams holds the resolved team(s):
//   - len(Teams) == 1: unique match; MemberCount is populated.
//   - len(Teams) > 1: multiple candidates from a name lookup (disambiguation).
//
// Not-found and input-validation failures are returned as tool errors instead.
type TeamInfoOutput struct {
	Teams             []*model.Team `json:"teams"`
	MemberCount       int64         `json:"member_count,omitempty"`
	PluginAnnotations []string      `json:"plugin_annotations,omitempty"`
}

// TeamMemberRow is one row of get_team_members output (user + role flags).
type TeamMemberRow struct {
	User        *model.User `json:"user"`
	SchemeAdmin bool        `json:"scheme_admin"`
	SchemeGuest bool        `json:"scheme_guest"`
	SchemeUser  bool        `json:"scheme_user"`
}

// TeamMembersOutput is the after-hook DTO for the get_team_members tool.
type TeamMembersOutput struct {
	Rows              []TeamMemberRow `json:"rows"`
	Page              int             `json:"page"`
	ExcludeBots       bool            `json:"exclude_bots"`
	PluginAnnotations []string        `json:"plugin_annotations,omitempty"`
}

// CreateTeamOutput is the after-hook DTO for the create_team tool.
type CreateTeamOutput struct {
	Team              *model.Team `json:"team"`
	TeamIconMessage   string      `json:"team_icon_message,omitempty"`
	PluginAnnotations []string    `json:"plugin_annotations,omitempty"`
}

// AddUserToTeamOutput is the after-hook DTO for the add_user_to_team tool.
type AddUserToTeamOutput struct {
	UserID            string      `json:"user_id"`
	TeamID            string      `json:"team_id"`
	User              *model.User `json:"user,omitempty"`
	Team              *model.Team `json:"team,omitempty"`
	PluginAnnotations []string    `json:"plugin_annotations,omitempty"`
}

// CreateUserOutput is the after-hook DTO for the create_user tool.
type CreateUserOutput struct {
	User                *model.User `json:"user"`
	ProfileImageMessage string      `json:"profile_image_message,omitempty"`
	PluginAnnotations   []string    `json:"plugin_annotations,omitempty"`
}

// ListAgentsOutput is the after-hook DTO for the list_agents tool.
type ListAgentsOutput struct {
	Agents            []AgentInfo `json:"agents"`
	CurrentBotUserID  string      `json:"current_bot_user_id,omitempty"`
	PluginAnnotations []string    `json:"plugin_annotations,omitempty"`
}

// AgentInfo holds display fields for one AI agent in ListAgentsOutput.
type AgentInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username"`
}

