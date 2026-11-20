// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package dictation

import (
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
)

const (
	// contextMaxNames bounds the names sent as hints. A transcription hint
	// helps only for names people say; 50 covers a team channel's regulars.
	contextMaxNames = 50
	// contextRecentPosts is how many of the newest posts are scanned for
	// recent posters: one page, the most the server returns at once.
	contextRecentPosts = 200
	// contextMemberPage is how many members fill the list after the recent
	// posters, so a large channel never loads every member. It leaves room
	// for bots and deactivated accounts, which are skipped.
	contextMemberPage = 100
)

// ChannelDirectory reads what channel context needs.
type ChannelDirectory interface {
	CanReadChannel(userID, channelID string) bool
	GetChannel(channelID string) (*model.Channel, error)
	// RecentPosts returns the channel's newest posts, newest first in Order.
	RecentPosts(channelID string, limit int) (*model.PostList, error)
	Members(channelID string, limit int) ([]*model.ChannelMember, error)
	Users(userIDs []string) ([]*model.User, error)
	// ShowFullName reports whether the server shows users' full names.
	ShowFullName() bool
}

type pluginChannelDirectory struct {
	client *pluginapi.Client
}

// NewPluginChannelDirectory reads channels through the plugin API.
func NewPluginChannelDirectory(client *pluginapi.Client) ChannelDirectory {
	return pluginChannelDirectory{client: client}
}

func (d pluginChannelDirectory) CanReadChannel(userID, channelID string) bool {
	return d.client.User.HasPermissionToChannel(userID, channelID, model.PermissionReadChannel)
}

func (d pluginChannelDirectory) GetChannel(channelID string) (*model.Channel, error) {
	return d.client.Channel.Get(channelID)
}

func (d pluginChannelDirectory) RecentPosts(channelID string, limit int) (*model.PostList, error) {
	return d.client.Post.GetPostsForChannel(channelID, 0, limit)
}

func (d pluginChannelDirectory) Members(channelID string, limit int) ([]*model.ChannelMember, error) {
	return d.client.Channel.ListMembers(channelID, 0, limit)
}

func (d pluginChannelDirectory) Users(userIDs []string) ([]*model.User, error) {
	return d.client.User.ListByUserIDs(userIDs)
}

// ShowFullName hides full names when the setting can't be read.
func (d pluginChannelDirectory) ShowFullName() bool {
	cfg := d.client.Configuration.GetConfig()
	return cfg != nil && model.SafeDereference(cfg.PrivacySettings.ShowFullName)
}

// checkChannel denies a user who can't read channelID. Without a channel
// directory nothing can be checked, so it denies too.
func (s *Service) checkChannel(userID, channelID string) error {
	if s.channels == nil || !s.channels.CanReadChannel(userID, channelID) {
		return fmt.Errorf("%w: channel not readable", ErrAccessDenied)
	}
	return nil
}

// channelContext builds the hints for channelID, which checkChannel has
// cleared. Any lookup error is returned, and the caller then sends no
// context at all.
func (s *Service) channelContext(channelID string) (format.DictationContext, error) {
	d := s.channels
	channel, err := d.GetChannel(channelID)
	if err != nil {
		return format.DictationContext{}, fmt.Errorf("failed to get the channel: %w", err)
	}
	if channel == nil || channel.Id != channelID {
		return format.DictationContext{}, fmt.Errorf("channel lookup returned another channel")
	}

	ids, err := contextUserIDs(d, channelID)
	if err != nil {
		return format.DictationContext{}, err
	}
	var names []string
	if len(ids) > 0 {
		users, usersErr := d.Users(ids)
		if usersErr != nil {
			return format.DictationContext{}, fmt.Errorf("failed to get users: %w", usersErr)
		}
		byID := make(map[string]*model.User, len(users))
		for _, u := range users {
			if u != nil {
				byID[u.Id] = u
			}
		}
		showFullName := d.ShowFullName()
		for _, id := range ids {
			u := byID[id]
			if u == nil || u.IsBot || u.DeleteAt != 0 {
				continue
			}
			names = append(names, format.DictationUserName(u, showFullName))
			if len(names) == contextMaxNames {
				break
			}
		}
	}

	channelName := ""
	if channel.Type == model.ChannelTypeOpen || channel.Type == model.ChannelTypePrivate {
		channelName = channel.DisplayName
	}
	return format.DictationChannelContext(channelName, names), nil
}

// contextUserIDs orders the people of a channel so a cut keeps the useful
// ones: recent posters first, most recent first, then other members. It
// reads one page of posts and one page of members.
func contextUserIDs(d ChannelDirectory, channelID string) ([]string, error) {
	posts, err := d.RecentPosts(channelID, contextRecentPosts)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent posts: %w", err)
	}
	members, err := d.Members(channelID, contextMemberPage)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel members: %w", err)
	}

	// Room for skipped bots and deactivated accounts.
	maxIDs := 2 * contextMaxNames
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] && len(ids) < maxIDs {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if posts != nil {
		for _, postID := range posts.Order {
			post := posts.Posts[postID]
			if post == nil || post.IsSystemMessage() || post.GetProp(model.PostPropsFromWebhook) == "true" {
				continue
			}
			add(post.UserId)
		}
	}
	for _, m := range members {
		if m != nil {
			add(m.UserId)
		}
	}
	return ids, nil
}
