// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/projects"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// wrapUpHistoryPosts is how many recent channel posts the wrap-up reads.
const wrapUpHistoryPosts = 200

// projectPlatform implements projects.Platform on the plugin API.
type projectPlatform struct {
	api    plugin.API
	client *pluginapi.Client
}

func (p projectPlatform) GetUser(userID string) (*model.User, error) {
	return p.client.User.Get(userID)
}
func (p projectPlatform) GetChannel(channelID string) (*model.Channel, error) {
	return p.client.Channel.Get(channelID)
}
func (p projectPlatform) CreateChannel(channel *model.Channel) (*model.Channel, error) {
	if err := p.client.Channel.Create(channel); err != nil {
		return nil, err
	}
	if _, err := p.client.Channel.AddMember(channel.Id, channel.CreatorId); err != nil {
		return nil, fmt.Errorf("failed to add the creator to the channel: %w", err)
	}
	if _, err := p.client.Channel.UpdateChannelMemberRoles(channel.Id, channel.CreatorId, model.ChannelUserRoleId+" "+model.ChannelAdminRoleId); err != nil {
		return nil, fmt.Errorf("failed to make the creator a channel admin: %w", err)
	}
	return channel, nil
}
func (p projectPlatform) ArchiveChannel(channelID string) error {
	return p.client.Channel.Delete(channelID)
}
func (p projectPlatform) AddTeamMember(teamID, userID string) error {
	_, err := p.client.Team.CreateMember(teamID, userID)
	return err
}
func (p projectPlatform) RemoveTeamMember(teamID, userID string) error {
	return p.client.Team.DeleteMember(teamID, userID, "")
}
func (p projectPlatform) AddChannelMember(channelID, userID string) error {
	_, err := p.client.Channel.AddMember(channelID, userID)
	return err
}
func (p projectPlatform) RemoveChannelMember(channelID, userID string) error {
	return p.client.Channel.DeleteMember(channelID, userID)
}
func (p projectPlatform) GetChannelMember(channelID, userID string) (*model.ChannelMember, error) {
	return p.client.Channel.GetMember(channelID, userID)
}
func (p projectPlatform) SetChannelAdmin(channelID, userID string, admin bool) error {
	roles := model.ChannelUserRoleId
	if admin {
		roles += " " + model.ChannelAdminRoleId
	}
	_, err := p.client.Channel.UpdateChannelMemberRoles(channelID, userID, roles)
	return err
}
func (p projectPlatform) BotChannelIDs(userID string) ([]string, error) {
	teams, appErr := p.api.GetTeamsForUser(userID)
	if appErr != nil {
		return nil, appErr
	}
	var ids []string
	for _, team := range teams {
		channels, appErr := p.api.GetChannelsForTeamForUser(team.Id, userID, false)
		if appErr != nil {
			return nil, appErr
		}
		for _, channel := range channels {
			if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
				continue
			}
			if !slices.Contains(ids, channel.Id) {
				ids = append(ids, channel.Id)
			}
		}
	}
	return ids, nil
}
func (p projectPlatform) CreateBot(bot *model.Bot) error { return p.client.Bot.Create(bot) }
func (p projectPlatform) DeactivateBot(botUserID string) error {
	_, err := p.client.Bot.UpdateActive(botUserID, false)
	return err
}
func (p projectPlatform) CreatePost(post *model.Post) error { return p.client.Post.CreatePost(post) }
func (p projectPlatform) PublishWebSocketEvent(event string, payload map[string]any, broadcast *model.WebsocketBroadcast) {
	p.api.PublishWebSocketEvent(event, payload, broadcast)
}
func (p projectPlatform) HasPermissionToTeam(userID, teamID string, permission *model.Permission) bool {
	return p.client.User.HasPermissionToTeam(userID, teamID, permission)
}
func (p projectPlatform) HasPermissionToChannel(userID, channelID string, permission *model.Permission) bool {
	return p.client.User.HasPermissionToChannel(userID, channelID, permission)
}
func (p projectPlatform) LogError(msg string, keyValuePairs ...any) {
	p.client.Log.Error(msg, keyValuePairs...)
}

// projectAutoReply turns on ambient replies through the auto-reply service.
type projectAutoReply struct{ service *autoreply.Service }

func (a projectAutoReply) EnableAmbient(_ context.Context, channelID, botUserID, actorID string) error {
	_, err := a.service.Set(autoreply.Setting{ChannelID: channelID, BotID: botUserID, Mode: autoreply.ModeAmbient, UpdatedBy: actorID})
	return err
}

// projectWrapUps writes the wrap-up with one non-streaming completion over
// the channel's recent posts.
type projectWrapUps struct {
	client   *pluginapi.Client
	projects func() *projects.Service
}

func (w projectWrapUps) WriteWrapUp(ctx context.Context, bot *bots.Bot, project *store.Project, instruction string) (string, error) {
	model := bot.LLM()
	if model == nil {
		return "", errors.New("the project agent has no language model")
	}
	posts, err := w.client.Post.GetPostsForChannel(project.ChannelID, 0, wrapUpHistoryPosts)
	if err != nil {
		return "", fmt.Errorf("failed to read the project channel: %w", err)
	}
	usernames := map[string]string{}
	var history strings.Builder
	ordered := posts.ToSlice()
	slices.Reverse(ordered)
	for _, post := range ordered {
		name, ok := usernames[post.UserId]
		if !ok {
			if user, userErr := w.client.User.Get(post.UserId); userErr == nil {
				name = user.Username
			}
			usernames[post.UserId] = name
		}
		history.WriteString(format.AuthoredPost(post, name))
		history.WriteString("\n")
	}
	return model.ChatCompletionNoStream(ctx, llm.CompletionRequest{
		Operation: llm.OperationProjectWrapUp,
		Posts: []llm.Post{
			{Role: llm.PostRoleSystem, Message: w.projects().SystemPromptSuffix(ctx, bot)},
			{Role: llm.PostRoleUser, Message: instruction + "\n\nRecent project channel messages, oldest first:\n" + history.String()},
		},
	})
}

// projectArtifacts saves Markdown through the channel artifact library.
type projectArtifacts struct{ library channelartifacts.Library }

func (a projectArtifacts) SaveMarkdown(ctx context.Context, actor channelartifacts.Actor, channelID, title string, body []byte) (string, error) {
	entry, err := a.library.Write(ctx, actor, channelartifacts.WriteRequest{
		SaveRequest: channelartifacts.SaveRequest{ChannelID: channelID, Title: title, Format: channelartifacts.FormatMarkdown},
		Body:        body,
	})
	if err != nil {
		return "", err
	}
	return entry.ID, nil
}

// ChannelWillBeArchived ends the project of an archived project channel. It
// never blocks the archive.
func (p *Plugin) ChannelWillBeArchived(c *plugin.Context, channel *model.Channel) string {
	if p.projects == nil || channel == nil {
		return ""
	}
	if err := p.projects.OnChannelArchived(context.Background(), channel.Id, ""); err != nil {
		p.pluginAPI.Log.Error("Failed to end the project of an archived channel", "channel_id", channel.Id, "error", err)
	}
	return ""
}
