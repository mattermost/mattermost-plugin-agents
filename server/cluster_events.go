// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"github.com/mattermost/mattermost-plugin-agents/api"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const clusterEventConfigUpdate = "config_update"
const clusterEventAgentUpdate = "agent_update"

func (p *Plugin) publishClusterEvent(eventID string) error {
	ev := model.PluginClusterEvent{Id: eventID}
	opts := model.PluginClusterEventSendOptions{
		SendType: model.PluginClusterEventSendTypeReliable,
	}
	if err := p.API.PublishPluginClusterEvent(ev, opts); err != nil {
		p.pluginAPI.Log.Error("Failed to publish cluster event", "event", eventID, "error", err.Error())
		return err
	}
	return nil
}

// PublishConfigUpdate broadcasts a config update event to all other nodes in the cluster.
func (p *Plugin) PublishConfigUpdate() error {
	return p.publishClusterEvent(clusterEventConfigUpdate)
}

// PublishAgentUpdate broadcasts an agent update event to all other nodes in the cluster.
func (p *Plugin) PublishAgentUpdate() error {
	return p.publishClusterEvent(clusterEventAgentUpdate)
}

// OnPluginClusterEvent handles cluster events from other nodes.
func (p *Plugin) OnPluginClusterEvent(_ *plugin.Context, ev model.PluginClusterEvent) {
	switch ev.Id {
	case clusterEventConfigUpdate:
		cfg, err := p.store.GetConfig()
		if err != nil {
			p.pluginAPI.Log.Error("Failed to reload config from database on cluster event", "error", err.Error())
			return
		}
		if cfg != nil {
			p.configuration.Update(cfg)
		}

	case clusterEventAgentUpdate:
		// Invalidate optimistic ensure snapshots and run EnsureBots so this node reloads DB-backed agents.
		p.bots.ForceRefreshOnNextEnsure()
		if err := p.bots.EnsureBots(); err != nil {
			p.pluginAPI.Log.Error("Failed to re-ensure bots after agent update cluster event", "error", err.Error())
		}
		// Clients connected to this node need the same RHS cache invalidation as on the originating node.
		mmapi.NewClient(p.pluginAPI).PublishWebSocketEvent(api.WebsocketEventBotsInvalidate, map[string]interface{}{}, &model.WebsocketBroadcast{})
	}
}
