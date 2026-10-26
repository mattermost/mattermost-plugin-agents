// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package notifications

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/i18n"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Client is the Mattermost API surface notifications need.
type Client interface {
	DeliverPostNotifications(postID string, options *model.DeliverPostNotificationsOptions) error
	KVGet(key string, value any) error
	KVSetWithExpiry(key string, value any, ttl time.Duration) error
	KVDelete(key string) error
	GetPost(postID string) (*model.Post, error)
	GetUser(userID string) (*model.User, error)
	GetUserStatus(userID string) (*model.Status, error)
	GetChannel(channelID string) (*model.Channel, error)
	GetChannelMember(channelID, userID string) (*model.ChannelMember, error)
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
// that does not exist with a 404; a server without the API answers nil
// (the plugin RPC call fails and is logged), and any other error is treated
// as unsupported so agent posts are never created deferred on a server that
// cannot release them.
func ProbeSupport(api DeliverAPI) bool {
	appErr := api.DeliverPostNotifications(model.NewId(), nil)
	return appErr != nil && appErr.StatusCode == http.StatusNotFound
}

// Service delivers agent post notifications. On servers without deferred
// notifications it falls back to a push to the requester only.
type Service struct {
	client    Client
	i18n      *i18n.Bundle
	supported bool

	fallbackMu      sync.Mutex
	fallbackAllowed map[string]struct{}
}

func NewService(client Client, i18nBundle *i18n.Bundle, supported bool) *Service {
	return &Service{
		client:          client,
		i18n:            i18nBundle,
		supported:       supported,
		fallbackAllowed: make(map[string]struct{}),
	}
}

// Supported reports whether the server supports deferred post notifications.
func (s *Service) Supported() bool {
	return s != nil && s.supported
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

// Notify decides who to notify about the end of a run and delivers it.
// Delivery errors are logged, never returned: a missed notification must not
// fail the response.
func (s *Service) Notify(ctx context.Context, end StreamEnd) {
	if s == nil {
		return
	}
	delivery := Decide(end)

	_, span := telemetry.Tracer().Start(ctx, "deliver post notifications",
		trace.WithAttributes(
			telemetry.PostID.String(end.PostID),
			telemetry.ChannelID.String(end.ChannelID),
			telemetry.UserID.String(end.RequesterID),
			telemetry.NotificationOutcome.String(string(end.Outcome)),
			telemetry.NotificationPaused.Bool(end.PausedForRequester),
			telemetry.NotificationDeferred.Bool(end.PostDeferred),
			telemetry.NotificationDelivery.String(delivery.String()),
			telemetry.NotificationFallback.Bool(!s.supported),
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
		"deferred_notifications_supported", s.supported,
	)

	var err error
	switch delivery {
	case DeliveryNone:
		return
	case DeliveryEveryone:
		if s.supported {
			err = s.client.DeliverPostNotifications(end.PostID, nil)
		} else {
			err = s.pushToRequester(end)
		}
	case DeliveryRequesterOnly:
		if s.supported {
			err = s.deliverToRequester(end)
		} else {
			err = s.pushToRequester(end)
		}
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

func (s *Service) deliverToRequester(end StreamEnd) error {
	if end.RequesterID == "" {
		s.client.LogDebug("Agent post notification skipped: no requester", "post_id", end.PostID)
		return nil
	}
	if s.RecipientIsViewing(end.PostID, end.RequesterID) {
		s.client.LogDebug("Agent post notification skipped: requester is viewing the response", "post_id", end.PostID)
		return nil
	}
	return s.client.DeliverPostNotifications(end.PostID, &model.DeliverPostNotificationsOptions{
		UserIDs: []string{end.RequesterID},
	})
}
