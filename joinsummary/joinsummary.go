// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package joinsummary generates a short "catch-up" summary of a channel's
// recent activity and shows it, ephemerally, only to the user who just joined.
// It is best-effort: any failure is logged and the join proceeds silently.
package joinsummary

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// defaultLookbackDays bounds the recent-activity window when the admin has
	// not configured one.
	defaultLookbackDays = 7

	// defaultMinPosts is the minimum number of eligible messages before a
	// summary is worth generating.
	defaultMinPosts = 3
)

// summarySystemPrompt instructs the model to produce a newcomer-oriented
// catch-up. It asks for a brief lead-in line so the ephemeral post is
// self-explanatory in the channel's own language without a separately
// localized header.
const summarySystemPrompt = `You are welcoming a user who just joined a Mattermost channel. Given the channel's recent messages, write a short catch-up for them.

Start with one brief lead-in line (e.g. "Here's what's been happening in this channel recently:"), then give a concise summary as a few bullet points covering the key topics, decisions, and any open questions a newcomer should know about. Be factual and do not invent anything that is not in the messages. If there is little of substance, say so in one sentence instead.`

// ConfigGetter returns the current channel-join-summary configuration.
type ConfigGetter func() config.ChannelJoinSummaryConfig

// Service orchestrates generation and delivery of channel-join summaries.
type Service struct {
	client       mmapi.Client
	bots         *bots.MMBots
	configGetter ConfigGetter
}

// New constructs a Service.
func New(client mmapi.Client, botsService *bots.MMBots, configGetter ConfigGetter) *Service {
	return &Service{
		client:       client,
		bots:         botsService,
		configGetter: configGetter,
	}
}

// HandleUserJoinedChannel is invoked from the UserHasJoinedChannel plugin hook.
// It performs cheap gating synchronously and then dispatches the slow LLM work
// to a goroutine so the join is never blocked. actor (who performed the add) is
// currently unused but kept to mirror the hook signature and allow future
// self-join vs added-by-other differentiation.
func (s *Service) HandleUserJoinedChannel(channelMember *model.ChannelMember, _ *model.User) {
	if s == nil || channelMember == nil {
		return
	}

	cfg := s.configGetter()
	if !cfg.Enabled {
		return
	}

	userID := channelMember.UserId
	// Never summarize for our own agent bots being added to channels.
	if s.bots.IsAnyBot(userID) {
		return
	}

	channel, err := s.client.GetChannel(channelMember.ChannelId)
	if err != nil {
		s.client.LogWarn("channel join summary: failed to get channel", "error", err, "channel_id", channelMember.ChannelId)
		return
	}
	if !shouldSummarizeChannel(channel) {
		return
	}

	go s.generateAndSend(context.Background(), cfg, userID, channel)
}

// generateAndSend fetches recent activity, asks the LLM for a summary, and
// delivers it as an ephemeral post to the joining user.
func (s *Service) generateAndSend(ctx context.Context, cfg config.ChannelJoinSummaryConfig, userID string, channel *model.Channel) {
	minPosts := resolveMinPosts(cfg.MinPosts)

	since := time.Now().Add(-time.Duration(resolveLookbackDays(cfg.LookbackDays)) * 24 * time.Hour).UnixMilli()
	postList, err := s.client.GetPostsSince(channel.Id, since)
	if err != nil {
		s.client.LogWarn("channel join summary: failed to fetch posts", "error", err, "channel_id", channel.Id)
		return
	}

	threadData, err := mmapi.GetMetadataForPosts(s.client, postList)
	if err != nil {
		s.client.LogWarn("channel join summary: failed to fetch post metadata", "error", err, "channel_id", channel.Id)
		return
	}

	threadData.Posts = filterSummarizablePosts(threadData.Posts)
	if len(threadData.Posts) < minPosts {
		// Not enough substance to be worth an ephemeral interruption.
		return
	}

	bot := s.bots.GetBotByUsernameOrFirst(cfg.Bot)
	if bot == nil {
		s.client.LogWarn("channel join summary: no bot available", "channel_id", channel.Id)
		return
	}

	userPrompt := fmt.Sprintf("Channel: %s\n\nRecent messages:\n%s", channelName(channel), format.ThreadData(threadData))
	req := llm.CompletionRequest{
		Posts: []llm.Post{
			{Role: llm.PostRoleSystem, Message: summarySystemPrompt},
			{Role: llm.PostRoleUser, Message: userPrompt},
		},
	}

	summary, err := bot.LLM().ChatCompletionNoStream(ctx, req, llm.WithToolsDisabled())
	if err != nil {
		s.client.LogWarn("channel join summary: LLM completion failed", "error", err, "channel_id", channel.Id)
		return
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}

	post := &model.Post{
		ChannelId: channel.Id,
		Message:   summary,
	}
	// Attribute the ephemeral post to the summarizing agent so it renders as
	// coming from the bot rather than the joining user.
	if mmBot := bot.GetMMBot(); mmBot != nil {
		post.UserId = mmBot.UserId
	}
	s.client.SendEphemeralPost(userID, post)
}

// shouldSummarizeChannel reports whether a channel is a candidate for a join
// summary: only active public and private channels, never DMs, group messages,
// or archived channels.
func shouldSummarizeChannel(channel *model.Channel) bool {
	if channel == nil || channel.DeleteAt != 0 {
		return false
	}
	switch channel.Type {
	case model.ChannelTypeOpen, model.ChannelTypePrivate:
		return true
	default:
		return false
	}
}

// filterSummarizablePosts drops deleted posts and system messages (joins,
// leaves, header changes, etc.) so only real conversation feeds the summary.
func filterSummarizablePosts(posts []*model.Post) []*model.Post {
	return slices.DeleteFunc(posts, func(post *model.Post) bool {
		return post == nil || post.DeleteAt != 0 || post.Type != ""
	})
}

// channelName returns the human-readable channel name for the prompt, falling
// back to the internal name when no display name is set.
func channelName(channel *model.Channel) string {
	if channel.DisplayName != "" {
		return channel.DisplayName
	}
	return channel.Name
}

func resolveLookbackDays(v int) int {
	if v <= 0 {
		return defaultLookbackDays
	}
	return v
}

func resolveMinPosts(v int) int {
	if v <= 0 {
		return defaultMinPosts
	}
	return v
}
