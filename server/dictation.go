// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-agents/v2/api"
	"github.com/mattermost/mattermost-plugin-agents/v2/dictation"
)

type websocketPublisher interface {
	PublishWebSocketEvent(event string, payload map[string]any, broadcast *model.WebsocketBroadcast)
}

// kvLeaseStore keeps dictation session leases in the plugin KV store, which
// every node shares.
type kvLeaseStore struct {
	kv *pluginapi.KVService
}

func (s kvLeaseStore) Get(key string) ([]byte, error) {
	var value []byte
	if err := s.kv.Get(key, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (s kvLeaseStore) CompareAndSet(key string, old, value []byte, ttl time.Duration) (bool, error) {
	var oldValue any
	if old != nil {
		oldValue = old
	}
	return s.kv.Set(key, value, pluginapi.SetAtomic(oldValue), pluginapi.SetExpiry(ttl))
}

type pluginUsers struct {
	users *pluginapi.UserService
}

func (u pluginUsers) GetUser(userID string) (*model.User, error) {
	return u.users.Get(userID)
}

// newDictationService builds the dictation service and reconciles it on
// config updates and license changes. Both reach every node, so each node
// tells its clients to refetch availability.
func (p *Plugin) newDictationService(client *pluginapi.Client, license dictation.LicenseChecker, access dictation.AccessChecker, ws websocketPublisher) *dictation.Service {
	svc := dictation.New(&p.configuration, license, access, pluginUsers{&client.User}, kvLeaseStore{&client.KV}, &client.Log,
		dictation.WithSessionEndNotifier(func(userID, sessionID, reason string) {
			ws.PublishWebSocketEvent(api.WebsocketEventDictationSessionEnded,
				map[string]any{"session_id": sessionID, "reason": reason},
				&model.WebsocketBroadcast{UserId: userID})
		}),
	)
	svc.OnAvailabilityChange(func() {
		ws.PublishWebSocketEvent(api.WebsocketEventDictationAvailabilityChanged, map[string]any{}, &model.WebsocketBroadcast{})
	})
	p.configuration.RegisterUpdateListener(svc.Reconcile)
	p.licenseChangeListeners = append(p.licenseChangeListeners, svc.Reconcile)
	return svc
}
