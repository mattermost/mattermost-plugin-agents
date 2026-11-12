// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package notifications

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost/server/public/model"
)

// maxFallbackPushMessageRunes keeps the fallback push body well under push
// proxy payload limits; the full response is one tap away.
const maxFallbackPushMessageRunes = 1000

func fallbackKey(postID, userID string) string {
	return postID + "_" + userID
}

// ConsumeFallbackPush reports whether the push for postID to userID is the
// one pushToRequester is sending, so the push hook lets exactly that push
// through on servers where agent pushes are otherwise blocked.
func (s *Service) ConsumeFallbackPush(postID, userID string) bool {
	if s == nil {
		return false
	}
	key := fallbackKey(postID, userID)
	s.fallbackMu.Lock()
	defer s.fallbackMu.Unlock()
	if _, ok := s.fallbackAllowed[key]; !ok {
		return false
	}
	delete(s.fallbackAllowed, key)
	return true
}

// pushToRequester is the notification for posts the server did not defer
// (no server support, or a shared channel), which notified nobody useful at
// creation: a single push to the requester when the response ends, subject
// to the same status, preference and mute checks the server applies to its
// own pushes. It is also the last resort when the server cannot notify the
// requester about a deferred post.
func (s *Service) pushToRequester(end StreamEnd) error {
	if end.RequesterID == "" {
		s.client.LogDebug("Agent post fallback push skipped: no requester", "post_id", end.PostID)
		return nil
	}
	if s.RecipientIsViewing(end.PostID, end.RequesterID) {
		s.client.LogDebug("Agent post fallback push skipped: requester is viewing the response", "post_id", end.PostID)
		return nil
	}

	cfg := s.client.GetConfig()
	if cfg == nil || !model.SafeDereference(cfg.EmailSettings.SendPushNotifications) {
		return nil
	}

	post, err := s.client.GetPost(end.PostID)
	if err != nil {
		return fmt.Errorf("failed to get post: %w", err)
	}
	recipient, err := s.client.GetUser(end.RequesterID)
	if err != nil {
		return fmt.Errorf("failed to get requester: %w", err)
	}
	if recipient.NotifyProps[model.PushNotifyProp] == model.UserNotifyNone {
		return nil
	}
	channel, err := s.client.GetChannel(post.ChannelId)
	if err != nil {
		return fmt.Errorf("failed to get channel: %w", err)
	}
	member, err := s.client.GetChannelMember(channel.Id, end.RequesterID)
	if err != nil {
		return fmt.Errorf("failed to get requester channel membership: %w", err)
	}
	if member.NotifyProps[model.PushNotifyProp] == model.ChannelNotifyNone ||
		member.NotifyProps[model.MarkUnreadNotifyProp] == model.ChannelMarkUnreadMention {
		return nil
	}
	status, err := s.client.GetUserStatus(end.RequesterID)
	if err != nil {
		return fmt.Errorf("failed to get requester status: %w", err)
	}
	isCRTReply := post.RootId != "" && s.crtEnabled(cfg, end.RequesterID)
	if !statusAllowsPush(recipient.NotifyProps, status, channel.Id, isCRTReply) {
		s.client.LogDebug("Agent post fallback push skipped: requester status", "post_id", end.PostID)
		return nil
	}
	sender, err := s.client.GetUser(post.UserId)
	if err != nil {
		return fmt.Errorf("failed to get sender: %w", err)
	}

	msg := s.buildFallbackPush(cfg, post, channel, sender, recipient)

	key := fallbackKey(post.Id, end.RequesterID)
	s.fallbackMu.Lock()
	s.fallbackAllowed[key] = struct{}{}
	s.fallbackMu.Unlock()
	// The push hook runs inside SendPushNotification; clear an entry it did
	// not consume (no sessions, push disabled) so it cannot exempt a later push.
	defer func() {
		s.fallbackMu.Lock()
		delete(s.fallbackAllowed, key)
		s.fallbackMu.Unlock()
	}()

	if err := s.client.SendPushNotification(msg, end.RequesterID); err != nil {
		return fmt.Errorf("failed to send push notification: %w", err)
	}
	return nil
}

// crtEnabled mirrors the server's IsCRTEnabledForUser, including treating a
// failed preference lookup (usually no preference saved) as the default.
func (s *Service) crtEnabled(cfg *model.Config, userID string) bool {
	switch setting := model.SafeDereference(cfg.ServiceSettings.CollapsedThreads); setting {
	case model.CollapsedThreadsDisabled:
		return false
	case model.CollapsedThreadsAlwaysOn:
		return true
	default:
		enabled := setting == model.CollapsedThreadsDefaultOn
		if pref, err := s.client.GetPreferenceForUser(userID, model.PreferenceCategoryDisplaySettings, model.PreferenceNameCollapsedThreadsEnabled); err == nil {
			enabled = pref.Value == "on"
		}
		return enabled
	}
}

// statusAllowsPush mirrors the server's doesStatusAllowPushNotification.
// Replies with collapsed threads on are pushed even while the user is active
// in the channel, since being in the channel does not show the thread.
func statusAllowsPush(userNotifyProps model.StringMap, status *model.Status, channelID string, isCRTReply bool) bool {
	if status.Status == model.StatusDnd || status.Status == model.StatusOutOfOffice {
		return false
	}
	pushStatus, ok := userNotifyProps[model.PushStatusNotifyProp]
	sendOnline := status.ActiveChannel != channelID ||
		model.GetMillis()-status.LastActivityAt > model.StatusChannelTimeout ||
		isCRTReply
	switch {
	case (pushStatus == model.StatusOnline || !ok) && sendOnline:
		return true
	case pushStatus == model.StatusAway && (status.Status == model.StatusAway || status.Status == model.StatusOffline):
		return true
	case pushStatus == model.StatusOffline && status.Status == model.StatusOffline:
		return true
	}
	return false
}

func (s *Service) buildFallbackPush(cfg *model.Config, post *model.Post, channel *model.Channel, sender, recipient *model.User) *model.PushNotification {
	senderName := sender.GetDisplayName(model.SafeDereference(cfg.TeamSettings.TeammateNameDisplay))
	if senderName == "" {
		senderName = sender.Username
	}
	isDM := channel.Type == model.ChannelTypeDirect
	contents := model.SafeDereference(cfg.EmailSettings.PushNotificationContents)

	msg := &model.PushNotification{
		Category:    model.CategoryCanReply,
		Version:     model.PushMessageV2,
		Type:        model.PushTypeMessage,
		TeamId:      channel.TeamId,
		ChannelId:   channel.Id,
		PostId:      post.Id,
		RootId:      post.RootId,
		SenderId:    post.UserId,
		SenderName:  senderName,
		PostType:    post.Type,
		ChannelType: channel.Type,
	}
	if contents != model.GenericNoChannelNotification || isDM {
		msg.ChannelName = channel.DisplayName
		if isDM {
			msg.ChannelName = senderName
		}
	}

	T := i18n.LocalizerFunc(s.i18n, recipient.Locale)
	// A post with only files, or paused on tool calls, has no text to show.
	text := strings.TrimSpace(truncateRunes(model.ClearMentionTags(post.Message), maxFallbackPushMessageRunes))
	switch {
	case contents == model.FullNotification && text != "":
		if isDM {
			msg.Message = text
		} else {
			msg.Message = senderName + ": " + text
		}
	case isDM:
		msg.Message = T("agents.notifications.push_generic_dm", "sent you a message.")
	default:
		msg.Message = T("agents.notifications.push_generic_channel", "%s replied to you.", senderName)
	}
	return msg
}

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
