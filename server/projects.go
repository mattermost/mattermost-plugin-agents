// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/autoreply"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/projects"
	"github.com/mattermost/mattermost-plugin-agents/v2/prompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// projectSetupSystemPrompt renders a project's setup conversation.
const projectSetupSystemPrompt = prompts.PromptProjectSetupSystem

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
func (p projectPlatform) GetTeam(teamID string) (*model.Team, error) {
	return p.client.Team.Get(teamID)
}
func (p projectPlatform) UsernameTaken(username string) (bool, error) {
	_, err := p.client.User.GetByUsername(username)
	if errors.Is(err, pluginapi.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
func (p projectPlatform) HideDirectChannel(userID, otherUserID string) error {
	_, err := p.client.Channel.GetByName("", model.GetDMNameFromIds(userID, otherUserID), false)
	if errors.Is(err, pluginapi.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if appErr := p.api.UpdatePreferencesForUser(userID, []model.Preference{{
		UserId: userID, Category: model.PreferenceCategoryDirectChannelShow, Name: otherUserID, Value: "false",
	}}); appErr != nil {
		return appErr
	}
	return nil
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
func (p projectPlatform) ChannelMemberIDs(channelID string) ([]string, error) {
	const perPage = 200
	var ids []string
	for page := 0; ; page++ {
		members, err := p.client.Channel.ListMembers(channelID, page, perPage)
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			ids = append(ids, member.UserId)
		}
		if len(members) < perPage {
			return ids, nil
		}
	}
}
func (p projectPlatform) SetChannelAdmin(channelID, userID string, admin bool) error {
	roles := model.ChannelUserRoleId
	if admin {
		roles += " " + model.ChannelAdminRoleId
	}
	_, err := p.client.Channel.UpdateChannelMemberRoles(channelID, userID, roles)
	return err
}
func (p projectPlatform) BotChannels(userID string) ([]*model.Channel, error) {
	teams, appErr := p.api.GetTeamsForUser(userID)
	if appErr != nil {
		return nil, appErr
	}
	var out []*model.Channel
	for _, team := range teams {
		channels, appErr := p.api.GetChannelsForTeamForUser(team.Id, userID, false)
		if appErr != nil {
			return nil, appErr
		}
		for _, channel := range channels {
			if channel.Type == model.ChannelTypeDirect || channel.Type == model.ChannelTypeGroup {
				continue
			}
			if !slices.ContainsFunc(out, func(c *model.Channel) bool { return c.Id == channel.Id }) {
				out = append(out, channel)
			}
		}
	}
	return out, nil
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

func (a projectAutoReply) EnableAmbient(_ context.Context, channelID, botUserID, actorID, instructions string) error {
	_, err := a.service.Set(autoreply.Setting{ChannelID: channelID, BotID: botUserID, Mode: autoreply.ModeAmbient, UpdatedBy: actorID, Instructions: instructions})
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
			{Role: llm.PostRoleSystem, Message: w.projects().SystemPromptSuffix(ctx, bot, llm.OperationProjectWrapUp)},
			{Role: llm.PostRoleUser, Message: instruction + "\n\nRecent project channel messages, oldest first:\n" + history.String()},
		},
	})
}

// projectArtifacts saves Markdown through the channel artifact library.
type projectArtifacts struct{ library channelartifacts.Library }

func (a projectArtifacts) SaveMarkdown(ctx context.Context, actor channelartifacts.Actor, channelID, title string, body []byte) (string, error) {
	entry, err := a.library.Write(ctx, actor, channelartifacts.WriteRequest{
		ChannelID: channelID, Title: title, Format: channelartifacts.FormatMarkdown,
		Body: body,
	})
	if err != nil {
		return "", err
	}
	return entry.ID, nil
}

func (a projectArtifacts) ChannelOf(ctx context.Context, actor channelartifacts.Actor, artifactID string) (string, error) {
	entry, err := a.library.Get(ctx, actor, artifactID)
	if err != nil {
		return "", err
	}
	return entry.ChannelID, nil
}

// UserHasJoinedChannel adds people who join a project channel to its board
// channel.
func (p *Plugin) UserHasJoinedChannel(_ *plugin.Context, member *model.ChannelMember, _ *model.User) {
	if p.projects == nil || member == nil {
		return
	}
	if err := p.projects.OnChannelMemberJoined(member.ChannelId, member.UserId); err != nil {
		p.pluginAPI.Log.Error("Failed to add a project member to the board channel", "channel_id", member.ChannelId, "user_id", member.UserId, "error", err)
	}
}

// UserHasLeftChannel removes people who leave a project channel from its
// board channel.
func (p *Plugin) UserHasLeftChannel(_ *plugin.Context, member *model.ChannelMember, _ *model.User) {
	if p.projects == nil || member == nil {
		return
	}
	if err := p.projects.OnChannelMemberLeft(member.ChannelId, member.UserId); err != nil {
		p.pluginAPI.Log.Error("Failed to remove a former project member from the board channel", "channel_id", member.ChannelId, "user_id", member.UserId, "error", err)
	}
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

// playbooksActingUserHeader names the user a plugin acts for; Playbooks
// trusts it alongside the Mattermost-Plugin-ID header core sets.
const playbooksActingUserHeader = "Mattermost-Plugin-Acting-User-Id"

// projectPlaybooks reads and finishes checklist runs through the Playbooks
// plugin's REST API, as the given user.
type projectPlaybooks struct {
	api plugin.API
}

type playbookRun struct {
	Name          string `json:"name"`
	CurrentStatus string `json:"current_status"`
	Checklists    []struct {
		Items []struct {
			State string `json:"state"`
		} `json:"items"`
	} `json:"checklists"`
}

func (p projectPlaybooks) do(ctx context.Context, method, path, userID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "/playbooks/api/v0"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(playbooksActingUserHeader, userID)
	resp := p.api.PluginHTTP(req)
	if resp == nil {
		return nil, errors.New("the Playbooks plugin is not available")
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		resp.Body.Close()
		return nil, playbooksStatusError{status: resp.StatusCode}
	}
	return resp, nil
}

type playbooksStatusError struct{ status int }

func (e playbooksStatusError) Error() string {
	return fmt.Sprintf("the Playbooks plugin returned status %d", e.status)
}

func (p projectPlaybooks) FinishRun(ctx context.Context, runID, actorUserID string) error {
	resp, err := p.do(ctx, http.MethodPut, "/runs/"+url.PathEscape(runID)+"/finish", actorUserID)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (p projectPlaybooks) RunSummary(ctx context.Context, runID, viewerUserID string) (*projects.ChecklistSummary, error) {
	resp, err := p.do(ctx, http.MethodGet, "/runs/"+url.PathEscape(runID), viewerUserID)
	if statusErr, ok := errors.AsType[playbooksStatusError](err); ok &&
		(statusErr.status == http.StatusForbidden || statusErr.status == http.StatusNotFound) {
		return &projects.ChecklistSummary{RunID: runID}, nil
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var run playbookRun
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&run); err != nil {
		return nil, fmt.Errorf("failed to read the checklist run: %w", err)
	}
	summary := &projects.ChecklistSummary{
		Available: true,
		RunID:     runID,
		Name:      run.Name,
		Status:    run.CurrentStatus,
		URL:       "/playbooks/runs/" + url.PathEscape(runID),
	}
	for _, checklist := range run.Checklists {
		for _, item := range checklist.Items {
			summary.TotalTasks++
			if item.State == "closed" {
				summary.CompletedTasks++
			}
		}
	}
	return summary, nil
}
