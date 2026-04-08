// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const clusterEventConfigUpdate = "config_update"

// PublishConfigUpdate broadcasts a config update event to all other nodes in the cluster.
// This is called after a config save via the admin API to ensure all nodes reload
// the latest config from the database.
func (p *Plugin) PublishConfigUpdate() error {
	ev := model.PluginClusterEvent{
		Id: clusterEventConfigUpdate,
	}
	opts := model.PluginClusterEventSendOptions{
		SendType: model.PluginClusterEventSendTypeReliable,
	}
	if err := p.API.PublishPluginClusterEvent(ev, opts); err != nil {
		p.pluginAPI.Log.Error("Failed to publish config update cluster event", "error", err.Error())
		return err
	}
	return nil
}

// OnPluginClusterEvent handles cluster events from other nodes.
// When a config_update event is received, it reloads the config from the database
// and updates the in-memory configuration.
func (p *Plugin) OnPluginClusterEvent(_ *plugin.Context, ev model.PluginClusterEvent) {
	if ev.Id != clusterEventConfigUpdate {
		return
	}

	cfg, err := p.store.GetConfig()
	if err != nil {
		p.pluginAPI.Log.Error("Failed to reload config from database on cluster event", "error", err.Error())
		return
	}
	if cfg != nil {
		p.configuration.Update(cfg)
	}
}
