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
// resolved to an open or private channel. Rejecting keeps an unresolvable or
// unexpected reference from skipping the DM/GM check.
var errUnverifiedChannelReference = fmt.Errorf("could not resolve the channel for a referenced ID; it may be invalid or inaccessible")

type guardedArgKind int

const (
	guardedChannelID guardedArgKind = iota
	guardedPostID
	guardedFileID
)

// guardedArgKeys are argument names the ID guard resolves to a channel.
// channel_ids / post_ids / file_ids share a kind with their singular forms.
// Matching is case-insensitive because encoding/json is.
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

// guardedIfIDKeys are argument names that are a Mattermost ID only when the
// value itself looks like one (e.g. search_posts "in" may be a name or an ID).
var guardedIfIDKeys = map[string]guardedArgKind{
	"in":     guardedChannelID,
	"before": guardedPostID,
	"after":  guardedPostID,
}

// isVerifiedOpenOrPrivate is the allow-list for bot sessions: only explicitly
// open or private channels pass. nil, D, G, boards, and unknown types do not.
func isVerifiedOpenOrPrivate(ch *model.Channel) bool {
	return ch != nil && (ch.Type == model.ChannelTypeOpen || ch.Type == model.ChannelTypePrivate)
}

func withoutDirectAndGroup(channels []*model.Channel) []*model.Channel {
	out := make([]*model.Channel, 0, len(channels))
	for _, ch := range channels {
		if isVerifiedOpenOrPrivate(ch) {
			out = append(out, ch)
		}
	}
	return out
}

// channelResolver maps IDs to channels, memoized for one MCP tool call.
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

func (mcpContext *MCPToolContext) channelResolver() *channelResolver {
	if mcpContext.resolver == nil {
		mcpContext.resolver = newChannelResolver(mcpContext)
	}
	return mcpContext.resolver
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
	ch, err := r.channelForFileInfo(info)
	if err != nil {
		return nil, err
	}
	r.byFile[fileID] = ch
	return ch, nil
}

func (r *channelResolver) channelForFileInfo(info *model.FileInfo) (*model.Channel, error) {
	if info == nil {
		return nil, fmt.Errorf("file info is missing")
	}
	switch {
	case info.ChannelId != "":
		return r.channel(info.ChannelId)
	case info.PostId != "":
		return r.channelByPost(info.PostId)
	default:
		return nil, fmt.Errorf("file %s is not attached to a channel or post", info.Id)
	}
}

func rejectIfNotOpenOrPrivate(ch *model.Channel) error {
	if isVerifiedOpenOrPrivate(ch) {
		return nil
	}
	if ch != nil && ch.IsGroupOrDirect() {
		return errDirectOrGroupInaccessible
	}
	return errUnverifiedChannelReference
}

// rejectDirectOrGroupArgs scans tool arguments for channel/post/file IDs and
// rejects the call when any do not resolve to an open or private channel.
// No-op for human sessions.
func rejectDirectOrGroupArgs(mcpContext *MCPToolContext, arguments any) error {
	if !mcpContext.IsBotSession {
		return nil
	}
	args := argumentsMap(arguments)
	if len(args) == 0 {
		return nil
	}
	return scanGuardedArgs(args, mcpContext.channelResolver())
}

func scanGuardedArgs(args map[string]any, r *channelResolver) error {
	for key, val := range args {
		lower := strings.ToLower(key)
		if kind, ok := guardedArgKeys[lower]; ok {
			if err := rejectResolvedIDs(kind, val, r); err != nil {
				return err
			}
		} else if kind, ok := guardedIfIDKeys[lower]; ok {
			if err := rejectResolvedIDsIfID(kind, val, r); err != nil {
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
		if err := rejectIfNotOpenOrPrivate(ch); err != nil {
			return err
		}
	}
	return nil
}

func rejectResolvedIDsIfID(kind guardedArgKind, val any, r *channelResolver) error {
	for _, id := range stringIDs(val) {
		if !model.IsValidId(id) {
			continue
		}
		ch, err := resolveGuardedID(kind, id, r)
		if err != nil {
			return errUnverifiedChannelReference
		}
		if err := rejectIfNotOpenOrPrivate(ch); err != nil {
			return err
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

// visiblePostList drops posts that are not in an open or private channel for
// bot sessions. Human sessions get the list unchanged.
func visiblePostList(mcpContext *MCPToolContext, postList *model.PostList) *model.PostList {
	if postList == nil || !mcpContext.IsBotSession {
		return postList
	}
	r := mcpContext.channelResolver()
	out := &model.PostList{Posts: make(map[string]*model.Post, len(postList.Posts))}
	for id, post := range postList.Posts {
		if post == nil {
			continue
		}
		ch, err := r.channel(post.ChannelId)
		if err != nil || !isVerifiedOpenOrPrivate(ch) {
			continue
		}
		out.Posts[id] = post
	}
	if len(out.Posts) == len(postList.Posts) {
		return postList
	}
	return out
}

// Verifies results locally; a remote includeDirectChannels flag is not trusted.
func filterScheduledPosts(mcpContext *MCPToolContext, posts []*model.ScheduledPost) []*model.ScheduledPost {
	if !mcpContext.IsBotSession {
		return posts
	}
	r := mcpContext.channelResolver()
	out := make([]*model.ScheduledPost, 0, len(posts))
	for _, sp := range posts {
		if sp == nil {
			continue
		}
		ch, err := r.channel(sp.ChannelId)
		if err != nil || !isVerifiedOpenOrPrivate(ch) {
			continue
		}
		out = append(out, sp)
	}
	return out
}

// Verifies results locally; a remote ExcludeDirect flag is not trusted.
func filterThreads(mcpContext *MCPToolContext, threads []*model.ThreadResponse) []*model.ThreadResponse {
	if !mcpContext.IsBotSession {
		return threads
	}
	r := mcpContext.channelResolver()
	out := make([]*model.ThreadResponse, 0, len(threads))
	for _, tr := range threads {
		if tr == nil || tr.Post == nil {
			continue
		}
		ch, err := r.channel(tr.Post.ChannelId)
		if err != nil || !isVerifiedOpenOrPrivate(ch) {
			continue
		}
		out = append(out, tr)
	}
	return out
}

func filterFileInfos(mcpContext *MCPToolContext, infos []*model.FileInfo) []*model.FileInfo {
	if !mcpContext.IsBotSession {
		return infos
	}
	r := mcpContext.channelResolver()
	out := make([]*model.FileInfo, 0, len(infos))
	for _, info := range infos {
		ch, err := r.channelForFileInfo(info)
		if err != nil || !isVerifiedOpenOrPrivate(ch) {
			continue
		}
		out = append(out, info)
	}
	return out
}

func filterChannelMembers(mcpContext *MCPToolContext, members model.ChannelMembers) model.ChannelMembers {
	if !mcpContext.IsBotSession {
		return members
	}
	r := mcpContext.channelResolver()
	out := make(model.ChannelMembers, 0, len(members))
	for i := range members {
		ch, err := r.channel(members[i].ChannelId)
		if err != nil || !isVerifiedOpenOrPrivate(ch) {
			continue
		}
		out = append(out, members[i])
	}
	return out
}

func filterSidebarCategories(mcpContext *MCPToolContext, categories []*model.SidebarCategoryWithChannels) []*model.SidebarCategoryWithChannels {
	if !mcpContext.IsBotSession {
		return categories
	}
	r := mcpContext.channelResolver()
	out := make([]*model.SidebarCategoryWithChannels, 0, len(categories))
	for _, cat := range categories {
		if cat == nil || cat.Type == model.SidebarCategoryDirectMessages {
			continue
		}
		copied := *cat
		visible := make([]string, 0, len(cat.Channels))
		for _, id := range cat.Channels {
			ch, err := r.channel(id)
			if err != nil || !isVerifiedOpenOrPrivate(ch) {
				continue
			}
			visible = append(visible, id)
		}
		copied.Channels = visible
		out = append(out, &copied)
	}
	return out
}
