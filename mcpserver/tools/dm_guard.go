// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// errDirectOrGroupInaccessible is returned when a bot session names a DM or GM
// channel (or a post/file in one). Tool errors here are not localized.
var errDirectOrGroupInaccessible = fmt.Errorf("direct and group messages are not accessible to this agent")

// errUnverifiedChannelReference is returned when a referenced ID cannot be
// resolved to a channel. Rejecting keeps an unresolvable reference from
// skipping the DM/GM check, and reports the real reason rather than implying
// the target was a DM.
var errUnverifiedChannelReference = fmt.Errorf("could not resolve the channel for a referenced ID; it may be invalid or inaccessible")

type guardedArgKind int

const (
	guardedChannelID guardedArgKind = iota
	guardedPostID
	guardedFileID
)

// guardedArgKeys are argument names the ID guard resolves to a channel.
// channel_ids / post_ids / file_ids share a kind with their singular forms.
var guardedArgKeys = map[string]guardedArgKind{
	"channel_id":       guardedChannelID,
	"channel_ids":      guardedChannelID,
	"post_id":          guardedPostID,
	"post_ids":         guardedPostID,
	"root_id":          guardedPostID,
	"thread_id":        guardedPostID,
	"reply_to_post_id": guardedPostID,
	"file_id":          guardedFileID,
	"file_ids":         guardedFileID,
}

// identifierArgsNotResolvedByGuard are schema property names that identify a
// channel or post but are not Mattermost IDs. channel_name cannot look up a D/G
// via GetChannelByName (those channels have no team); GetChannelsForTeamForUser
// can still return DMs, so get_channel_info filters results instead.
var identifierArgsNotResolvedByGuard = map[string]bool{
	"channel_name": true,
}

func isDirectOrGroupChannel(ch *model.Channel) bool {
	return ch != nil && ch.IsGroupOrDirect()
}

func withoutDirectAndGroup(channels []*model.Channel) []*model.Channel {
	out := make([]*model.Channel, 0, len(channels))
	for _, ch := range channels {
		if isDirectOrGroupChannel(ch) {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// channelResolver maps IDs to channels, memoized for one tool call.
type channelResolver struct {
	mcpContext *MCPToolContext
	byChannel  map[string]*model.Channel
	byPost     map[string]*model.Channel
	byFile     map[string]*model.Channel
}

func newChannelResolver(mcpContext *MCPToolContext) *channelResolver {
	return &channelResolver{
		mcpContext: mcpContext,
		byChannel:  make(map[string]*model.Channel),
		byPost:     make(map[string]*model.Channel),
		byFile:     make(map[string]*model.Channel),
	}
}

func (r *channelResolver) channel(id string) (*model.Channel, error) {
	if ch, ok := r.byChannel[id]; ok {
		return ch, nil
	}
	ch, _, err := r.mcpContext.Client.GetChannel(r.mcpContext.Ctx, id)
	if err != nil {
		return nil, err
	}
	r.byChannel[id] = ch
	return ch, nil
}

func (r *channelResolver) channelByPost(postID string) (*model.Channel, error) {
	if ch, ok := r.byPost[postID]; ok {
		return ch, nil
	}
	post, _, err := r.mcpContext.Client.GetPost(r.mcpContext.Ctx, postID, "")
	if err != nil {
		return nil, err
	}
	ch, err := r.channel(post.ChannelId)
	if err != nil {
		return nil, err
	}
	r.byPost[postID] = ch
	return ch, nil
}

func (r *channelResolver) channelByFile(fileID string) (*model.Channel, error) {
	if ch, ok := r.byFile[fileID]; ok {
		return ch, nil
	}
	info, _, err := r.mcpContext.Client.GetFileInfo(r.mcpContext.Ctx, fileID)
	if err != nil {
		return nil, err
	}
	var ch *model.Channel
	switch {
	case info.ChannelId != "":
		ch, err = r.channel(info.ChannelId)
	case info.PostId != "":
		ch, err = r.channelByPost(info.PostId)
	default:
		return nil, fmt.Errorf("file %s is not attached to a channel or post", fileID)
	}
	if err != nil {
		return nil, err
	}
	r.byFile[fileID] = ch
	return ch, nil
}

// rejectDirectOrGroupArgs scans tool arguments for channel/post/file IDs and
// rejects the call when any resolve to a DM or GM. No-op for human sessions.
func rejectDirectOrGroupArgs(mcpContext *MCPToolContext, arguments any) error {
	if mcpContext == nil || !mcpContext.IsBotSession {
		return nil
	}
	args := argumentsMap(arguments)
	if len(args) == 0 {
		return nil
	}
	return scanGuardedArgs(args, newChannelResolver(mcpContext))
}

func scanGuardedArgs(args map[string]any, r *channelResolver) error {
	for key, val := range args {
		if kind, ok := guardedArgKeys[key]; ok {
			if err := rejectResolvedIDs(kind, val, r); err != nil {
				return err
			}
		}
		switch nested := val.(type) {
		case map[string]any:
			if err := scanGuardedArgs(nested, r); err != nil {
				return err
			}
		case []any:
			for _, item := range nested {
				if m, ok := item.(map[string]any); ok {
					if err := scanGuardedArgs(m, r); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func rejectResolvedIDs(kind guardedArgKind, val any, r *channelResolver) error {
	for _, id := range stringIDs(val) {
		ch, err := resolveGuardedID(kind, id, r)
		if err != nil {
			return errUnverifiedChannelReference
		}
		if isDirectOrGroupChannel(ch) {
			return errDirectOrGroupInaccessible
		}
	}
	return nil
}

func resolveGuardedID(kind guardedArgKind, id string, r *channelResolver) (*model.Channel, error) {
	switch kind {
	case guardedChannelID:
		return r.channel(id)
	case guardedPostID:
		return r.channelByPost(id)
	case guardedFileID:
		return r.channelByFile(id)
	default:
		return nil, fmt.Errorf("unhandled guarded argument kind %d", kind)
	}
}

func argumentsMap(arguments any) map[string]any {
	switch v := arguments.(type) {
	case map[string]any:
		return v
	case json.RawMessage:
		var m map[string]any
		if err := json.Unmarshal(v, &m); err != nil {
			return nil
		}
		return m
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil
		}
		return m
	}
}

func stringIDs(v any) []string {
	switch x := v.(type) {
	case string:
		if x != "" {
			return []string{x}
		}
	case []any:
		ids := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok && s != "" {
				ids = append(ids, s)
			}
		}
		return ids
	}
	return nil
}

func filterPostsFromDirectAndGroup(mcpContext *MCPToolContext, posts []*model.Post) []*model.Post {
	if mcpContext == nil || !mcpContext.IsBotSession {
		return posts
	}
	r := newChannelResolver(mcpContext)
	out := make([]*model.Post, 0, len(posts))
	for _, post := range posts {
		if post == nil {
			continue
		}
		ch, err := r.channel(post.ChannelId)
		if err != nil || isDirectOrGroupChannel(ch) {
			continue
		}
		out = append(out, post)
	}
	return out
}

func looksLikeChannelOrPostIdentifier(name string) bool {
	if name == "scheduled_post_id" {
		return false
	}
	switch name {
	case "channel_name", "root_id", "thread_id", "file_id", "file_ids":
		return true
	}
	return strings.Contains(name, "channel_id") || strings.Contains(name, "post_id")
}
