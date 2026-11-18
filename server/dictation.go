// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-agents/v2/api"
	"github.com/mattermost/mattermost-plugin-agents/v2/dictation"
)

type websocketPublisher interface {
	PublishWebSocketEvent(event string, payload map[string]any, broadcast *model.WebsocketBroadcast)
}

// newDictationService builds the dictation service and reconciles it on
// config updates and license changes. Both reach every node, so each node
// tells its clients to refetch availability.
func (p *Plugin) newDictationService(license dictation.LicenseChecker, access dictation.AccessChecker, log dictation.Logger, ws websocketPublisher) *dictation.Service {
	svc := dictation.New(&p.configuration, license, access, log)
	svc.OnAvailabilityChange(func() {
		ws.PublishWebSocketEvent(api.WebsocketEventDictationAvailabilityChanged, map[string]any{}, &model.WebsocketBroadcast{})
	})
	p.configuration.RegisterUpdateListener(svc.Reconcile)
	p.licenseChangeListeners = append(p.licenseChangeListeners, svc.Reconcile)
	return svc
}
