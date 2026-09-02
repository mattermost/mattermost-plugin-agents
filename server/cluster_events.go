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
const clusterEventChannelAutoReplyInvalidate = "channel_autoreply_invalidate"

type mcpOAuthUserInvalidateClusterPayload struct {
	UserID string `json:"userID"`
}

type streamStopClusterPayload struct {
	PostID string `json:"postID"`
}

type channelAutoReplyInvalidateClusterPayload struct {
	ChannelID string `json:"channelID"`
}

// channelAutoReplyRefresher is the part of *autoreply.Service the cluster
// event handler needs. Narrowed to an interface so the handler is testable
// without a database.
type channelAutoReplyRefresher interface {
	RefreshChannel(channelID string) error
}

func (p *Plugin) publishClusterEvent(eventID string, data []byte) error {
	ev := model.PluginClusterEvent{Id: eventID, Data: data}
	opts := model.PluginClusterEventSendOptions{
		SendType: model.PluginClusterEventSendTypeReliable,
	}
	if err := p.API.PublishPluginClusterEvent(ev, opts); err != nil {
		p.pluginAPI.Log.Error("Failed to publish cluster event", "event", eventID, "error", err.Error())
		return err
	}
	return nil
}

func (p *Plugin) publishClusterEventWithPayload(eventID string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.publishClusterEvent(eventID, data)
}

// PublishConfigUpdate broadcasts a config update event to all other nodes in the cluster.
func (p *Plugin) PublishConfigUpdate() error {
	return p.publishClusterEvent(clusterEventConfigUpdate, nil)
}

// PublishAgentUpdate broadcasts an agent update event to all other nodes in the cluster.
func (p *Plugin) PublishAgentUpdate() error {
	return p.publishClusterEvent(clusterEventAgentUpdate, nil)
}

// PublishMCPOAuthUpdate broadcasts a per-user MCP OAuth cache invalidation to all other nodes.
func (p *Plugin) PublishMCPOAuthUpdate(userID string) error {
	if userID == "" {
		return nil
	}
	return p.publishClusterEventWithPayload(clusterEventMCPOAuthUserInvalidate, mcpOAuthUserInvalidateClusterPayload{UserID: userID})
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
	return p.publishClusterEventWithPayload(clusterEventStreamStop, streamStopClusterPayload{PostID: postID})
}

// PublishChannelAutoReplyInvalidate broadcasts a per-channel auto-reply cache
// invalidation to all other nodes in the cluster. The originating node has
// already updated its own cache; receivers re-read the channel's row from the
// database, so a duplicated or reordered event still converges.
func (p *Plugin) PublishChannelAutoReplyInvalidate(channelID string) error {
	if channelID == "" {
		return nil
	}
	return p.publishClusterEventWithPayload(clusterEventChannelAutoReplyInvalidate, channelAutoReplyInvalidateClusterPayload{ChannelID: channelID})
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
		mmapi.NewClient(p.pluginAPI).PublishWebSocketEvent(api.WebsocketEventBotsInvalidate, map[string]any{}, &model.WebsocketBroadcast{})

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

	case clusterEventChannelAutoReplyInvalidate:
		var payload channelAutoReplyInvalidateClusterPayload
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			p.pluginAPI.Log.Error("Failed to unmarshal channel auto-reply cluster invalidation payload", "error", err.Error())
			return
		}
		if payload.ChannelID == "" {
			p.pluginAPI.Log.Error("Received channel auto-reply cluster invalidation with empty channelID")
			return
		}
		if p.autoreplyService != nil {
			if err := p.autoreplyService.RefreshChannel(payload.ChannelID); err != nil {
				p.pluginAPI.Log.Error("Failed to refresh channel auto-reply cache on cluster event", "channel_id", payload.ChannelID, "error", err.Error())
			}
		}
	}
}
