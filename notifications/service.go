// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package notifications

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Client is the Mattermost API surface notifications need.
type Client interface {
	DeliverPostNotifications(postID string, options *model.DeliverPostNotificationsOptions) error
	KVGet(key string, value any) error
	KVSetWithExpiry(key string, value any, ttl time.Duration) error
	KVCompareAndSet(key string, oldValue, newValue any) (bool, error)
	GetPost(postID string) (*model.Post, error)
	GetUser(userID string) (*model.User, error)
	GetUserStatus(userID string) (*model.Status, error)
	GetChannel(channelID string) (*model.Channel, error)
	GetChannelMember(channelID, userID string) (*model.ChannelMember, error)
	GetPreferenceForUser(userID, category, name string) (model.Preference, error)
	GetConfig() *model.Config
	SendPushNotification(notification *model.PushNotification, userID string) error
	LogError(msg string, keyValuePairs ...any)
	LogWarn(msg string, keyValuePairs ...any)
	LogDebug(msg string, keyValuePairs ...any)
}

// Notifier is what the response paths use: mark a new agent post deferred,
// and notify once a run on it ends.
type Notifier interface {
	MarkDeferred(post *model.Post)
	Notify(ctx context.Context, end StreamEnd)
}

// DeliverAPI is the raw plugin API method probed for server support.
type DeliverAPI interface {
	DeliverPostNotifications(postID string, options *model.DeliverPostNotificationsOptions) *model.AppError
}

// ProbeSupport reports whether the server supports deferred post
// notifications. A supporting server answers a delivery request for a post
// that does not exist with a 404, and a server without the API answers nil
// (the plugin RPC call fails and is logged). Any other answer is returned as
// an error, with support unknown.
func ProbeSupport(api DeliverAPI) (bool, error) {
	appErr := api.DeliverPostNotifications(model.NewId(), nil)
	switch {
	case appErr == nil:
		return false, nil
	case appErr.StatusCode == http.StatusNotFound:
		return true, nil
	default:
		return false, appErr
	}
}

const defaultReprobeInterval = time.Minute

// defaultReleaseRetryDelays are the waits between attempts to release a
// deferred post. Releasing is conditional and happens at most once, so
// retrying after an error cannot notify the channel twice.
var defaultReleaseRetryDelays = []time.Duration{200 * time.Millisecond, 800 * time.Millisecond}

// Service delivers agent post notifications. Posts the server did not defer
// fall back to a push to the requester only.
type Service struct {
	client Client
	i18n   *i18n.Bundle

	supportMu sync.Mutex
	supported bool
	// probe is set when support is probed rather than given. After a failed
	// probe, support counts as missing until a re-probe succeeds; re-probes
	// run when support is next needed, at most once per reprobeInterval.
	probe           DeliverAPI
	probeFailed     bool
	probing         bool
	lastProbe       time.Time
	reprobeInterval time.Duration

	releaseRetryDelays []time.Duration

	fallbackMu      sync.Mutex
	fallbackAllowed map[string]struct{}
}

// NewService returns a Service with server support fixed to supported.
func NewService(client Client, i18nBundle *i18n.Bundle, supported bool) *Service {
	s := newService(client, i18nBundle)
	s.supported = supported
	return s
}

// NewProbedService returns a Service that probes api for server support.
func NewProbedService(client Client, i18nBundle *i18n.Bundle, api DeliverAPI) *Service {
	s := newService(client, i18nBundle)
	s.probe = api
	s.probing = true
	s.runProbe()
	return s
}

func newService(client Client, i18nBundle *i18n.Bundle) *Service {
	return &Service{
		client:             client,
		i18n:               i18nBundle,
		reprobeInterval:    defaultReprobeInterval,
		releaseRetryDelays: defaultReleaseRetryDelays,
		fallbackAllowed:    make(map[string]struct{}),
	}
}

// Supported reports whether the server supports deferred post notifications.
func (s *Service) Supported() bool {
	if s == nil {
		return false
	}
	s.supportMu.Lock()
	due := s.probe != nil && s.probeFailed && !s.probing && time.Since(s.lastProbe) >= s.reprobeInterval
	if !due {
		supported := s.supported
		s.supportMu.Unlock()
		return supported
	}
	s.probing = true
	s.supportMu.Unlock()
	return s.runProbe()
}

// runProbe probes support; the caller must have set probing.
func (s *Service) runProbe() bool {
	supported, err := ProbeSupport(s.probe)

	s.supportMu.Lock()
	s.probing = false
	s.lastProbe = time.Now()
	s.probeFailed = err != nil
	if err == nil {
		s.supported = supported
	}
	supported = s.supported
	s.supportMu.Unlock()

	if err != nil {
		s.client.LogWarn("Failed to probe server support for deferred agent post notifications; will retry", "error", err)
	}
	return supported
}

// MarkDeferred marks a new agent post so the server holds its notifications
// until Notify releases them. It does nothing on servers without support,
// where the prop would be stripped or ignored.
func (s *Service) MarkDeferred(post *model.Post) {
	if !s.Supported() || post == nil {
		return
	}
	post.AddProp(model.PostPropsDeferredNotification, true)
}

// UsesLegacyNotifications reports whether agent posts in channelID notify
// when they are created rather than when the response ends: on servers
// without deferred notifications, and in shared channels, where the server
// does not defer posts.
func (s *Service) UsesLegacyNotifications(channelID string) bool {
	if !s.Supported() {
		return true
	}
	return s.channelIsShared(channelID)
}

func (s *Service) channelIsShared(channelID string) bool {
	if channelID == "" {
		return false
	}
	channel, err := s.client.GetChannel(channelID)
	if err != nil {
		s.client.LogWarn("Failed to get channel for agent post notifications; assuming it is not shared", "channel_id", channelID, "error", err)
		return false
	}
	return channel.IsShared()
}

// isLegacy reports whether the post notified the channel when it was
// created. A deferred post never did, whatever the cached probe says: the
// marker proves the server supports releasing it.
func (s *Service) isLegacy(end StreamEnd) bool {
	if end.PostDeferred {
		return false
	}
	return s.UsesLegacyNotifications(end.ChannelID)
}

// Notify decides who to notify about the end of a run and delivers it.
// Delivery errors are logged, never returned: a missed notification must not
// fail the response.
func (s *Service) Notify(ctx context.Context, end StreamEnd) {
	if s == nil {
		return
	}
	delivery := Decide(end)
	if delivery == DeliveryNone {
		s.client.LogDebug("Agent post notification decision", "post_id", end.PostID, "outcome", string(end.Outcome), "delivery", delivery.String())
		return
	}
	legacy := s.isLegacy(end)

	ctx, span := telemetry.Tracer().Start(ctx, "deliver post notifications",
		trace.WithAttributes(
			telemetry.PostID.String(end.PostID),
			telemetry.ChannelID.String(end.ChannelID),
			telemetry.UserID.String(end.RequesterID),
			telemetry.NotificationOutcome.String(string(end.Outcome)),
			telemetry.NotificationPaused.Bool(end.PausedForRequester),
			telemetry.NotificationDeferred.Bool(end.PostDeferred),
			telemetry.NotificationDelivery.String(delivery.String()),
			telemetry.NotificationFallback.Bool(legacy),
		),
	)
	defer span.End()

	s.client.LogDebug("Agent post notification decision",
		"post_id", end.PostID,
		"outcome", string(end.Outcome),
		"paused_for_requester", end.PausedForRequester,
		"update_failed", end.UpdateFailed,
		"post_deferred", end.PostDeferred,
		"delivery", delivery.String(),
		"legacy", legacy,
	)

	var err error
	switch {
	case legacy:
		err = s.pushToRequester(end)
	case delivery == DeliveryEveryone:
		err = s.release(ctx, end)
	default:
		err = s.notifyRequester(end)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to deliver post notifications")
		s.client.LogError("Failed to deliver agent post notifications",
			"post_id", end.PostID,
			"delivery", delivery.String(),
			"error", err,
		)
	}
}

// release notifies the channel about a deferred post. If the server keeps
// failing and the post is still deferred, only the requester is notified, so
// the answer is not left silent.
func (s *Service) release(ctx context.Context, end StreamEnd) error {
	err := s.client.DeliverPostNotifications(end.PostID, nil)
	for _, delay := range s.releaseRetryDelays {
		if err == nil || errors.Is(err, pluginapi.ErrNotFound) {
			break
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(delay):
		}
		err = s.client.DeliverPostNotifications(end.PostID, nil)
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, pluginapi.ErrNotFound) {
		s.client.LogDebug("Agent post notification skipped: post no longer exists", "post_id", end.PostID)
		return nil
	}

	s.client.LogError("Failed to release agent post notifications to the channel", "post_id", end.PostID, "error", err)
	post, getErr := s.client.GetPost(end.PostID)
	if getErr != nil {
		return errors.Join(err, getErr)
	}
	if !post.HasDeferredNotification() {
		// The server released the post before failing part of the delivery.
		return nil
	}
	return s.notifyRequester(end)
}

// notifyRequester notifies only the requester, unless they are viewing the
// response. If the server cannot, the requester gets the fallback push.
func (s *Service) notifyRequester(end StreamEnd) error {
	if end.RequesterID == "" {
		s.client.LogDebug("Agent post notification skipped: no requester", "post_id", end.PostID)
		return nil
	}
	if s.RecipientIsViewing(end.PostID, end.RequesterID) {
		s.client.LogDebug("Agent post notification skipped: requester is viewing the response", "post_id", end.PostID)
		return nil
	}
	err := s.client.DeliverPostNotifications(end.PostID, &model.DeliverPostNotificationsOptions{
		UserIDs: []string{end.RequesterID},
	})
	if err == nil || errors.Is(err, pluginapi.ErrNotFound) {
		return nil
	}
	s.client.LogWarn("Failed to notify the requester about an agent post; sending a push instead", "post_id", end.PostID, "error", err)
	return s.pushToRequester(end)
}
