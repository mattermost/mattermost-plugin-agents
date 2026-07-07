// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"

	"github.com/mattermost/mattermost-plugin-agents/v2/api"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const clusterEventConfigUpdate = "config_update"
const clusterEventAgentUpdate = "agent_update"
const clusterEventMCPOAuthUserInvalidate = "mcp_oauth_user_invalidate"
const clusterEventStreamStop = "stream_stop"

type mcpOAuthUserInvalidateClusterPayload struct {
	UserID string `json:"userID"`
}

type streamStopClusterPayload struct {
	PostID string `json:"postID"`
}

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

// PublishMCPOAuthUpdate broadcasts a per-user MCP OAuth cache invalidation to all other nodes.
func (p *Plugin) PublishMCPOAuthUpdate(userID string) error {
	if userID == "" {
		return nil
	}

	payload, err := json.Marshal(mcpOAuthUserInvalidateClusterPayload{UserID: userID})
	if err != nil {
		return err
	}

	ev := model.PluginClusterEvent{
		Id:   clusterEventMCPOAuthUserInvalidate,
		Data: payload,
	}
	opts := model.PluginClusterEventSendOptions{
		SendType: model.PluginClusterEventSendTypeReliable,
	}
	if err := p.API.PublishPluginClusterEvent(ev, opts); err != nil {
		p.pluginAPI.Log.Error("Failed to publish cluster event", "event", clusterEventMCPOAuthUserInvalidate, "error", err.Error())
		return err
	}
	return nil
}

// PublishStreamStop broadcasts a stop-streaming request to all other nodes so
// that whichever node holds the per-post cancel function in memory will cancel
// the in-flight LLM stream. The originating node has already canceled
// locally; this only reaches peers. Without sticky sessions the stop request
// can land on any node, so without this broadcast the click is silently
// dropped unless the request happens to hit the streaming node.
func (p *Plugin) PublishStreamStop(postID string) error {
	if postID == "" {
		return nil
	}

	payload, err := json.Marshal(streamStopClusterPayload{PostID: postID})
	if err != nil {
		return err
	}

	ev := model.PluginClusterEvent{
		Id:   clusterEventStreamStop,
		Data: payload,
	}
	opts := model.PluginClusterEventSendOptions{
		SendType: model.PluginClusterEventSendTypeReliable,
	}
	if err := p.API.PublishPluginClusterEvent(ev, opts); err != nil {
		p.pluginAPI.Log.Error("Failed to publish cluster event", "event", clusterEventStreamStop, "error", err.Error())
		return err
	}
	return nil
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

	case clusterEventMCPOAuthUserInvalidate:
		var payload mcpOAuthUserInvalidateClusterPayload
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			p.pluginAPI.Log.Error("Failed to unmarshal MCP OAuth cluster invalidation payload", "error", err.Error())
			return
		}
		if payload.UserID == "" {
			p.pluginAPI.Log.Error("Received MCP OAuth cluster invalidation with empty userID")
			return
		}
		if p.mcpClientManager != nil {
			p.mcpClientManager.InvalidateUserClients(payload.UserID)
		}

	case clusterEventStreamStop:
		var payload streamStopClusterPayload
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			p.pluginAPI.Log.Error("Failed to unmarshal stream stop cluster payload", "error", err.Error())
			return
		}
		if payload.PostID == "" {
			p.pluginAPI.Log.Error("Received stream stop cluster event with empty postID")
			return
		}
		if p.streamingService != nil {
			p.streamingService.StopStreaming(payload.PostID)
		}
	}
}
