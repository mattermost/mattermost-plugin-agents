// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// sourceChannelsParam is the llm.Context.Parameters and MCP result Meta key
// listing the Mattermost channels a tool result drew from.
const sourceChannelsParam = "source_channels"

// reportedSourceToolsParam lists bare tool names that included source-channel
// metadata. The key must match conversations.reportedSourceToolsParam.
const reportedSourceToolsParam = "source_channels_reported_tools"

func applySourceChannelsMeta(llmContext *llm.Context, toolName string, meta sdkmcp.Meta) {
	channels, ok := sourceChannelsFromMeta(meta)
	if !ok || llmContext == nil {
		return
	}
	if llmContext.Parameters == nil {
		llmContext.Parameters = make(map[string]any)
	}
	existing, _ := llmContext.Parameters[sourceChannelsParam].([]*model.Channel)
	llmContext.Parameters[sourceChannelsParam] = mergeSourceChannels(existing, channels)
	if toolName == "" {
		return
	}
	reported, _ := llmContext.Parameters[reportedSourceToolsParam].(map[string]bool)
	if reported == nil {
		reported = map[string]bool{}
	}
	reported[toolName] = true
	llmContext.Parameters[reportedSourceToolsParam] = reported
}

func sourceChannelsFromMeta(meta sdkmcp.Meta) ([]*model.Channel, bool) {
	if meta == nil {
		return nil, false
	}
	raw, ok := meta[sourceChannelsParam]
	if !ok {
		return nil, false
	}
	switch v := raw.(type) {
	case []*model.Channel:
		return v, true
	case []any:
		out := make([]*model.Channel, 0, len(v))
		for _, item := range v {
			ch, ok := channelFromMetaItem(item)
			if !ok {
				return nil, false
			}
			out = append(out, ch)
		}
		return out, true
	case []map[string]any:
		out := make([]*model.Channel, 0, len(v))
		for _, item := range v {
			ch, ok := channelFromMetaMap(item)
			if !ok {
				return nil, false
			}
			out = append(out, ch)
		}
		return out, true
	default:
		return nil, false
	}
}

func channelFromMetaItem(item any) (*model.Channel, bool) {
	switch v := item.(type) {
	case *model.Channel:
		return v, v != nil
	case map[string]any:
		return channelFromMetaMap(v)
	default:
		return nil, false
	}
}

func channelFromMetaMap(item map[string]any) (*model.Channel, bool) {
	id, _ := item["id"].(string)
	if id == "" {
		return nil, false
	}
	ch := &model.Channel{Id: id}
	if t, ok := item["type"].(string); ok {
		ch.Type = model.ChannelType(t)
	}
	if teamID, ok := item["team_id"].(string); ok {
		ch.TeamId = teamID
	}
	if name, ok := item["name"].(string); ok {
		ch.Name = name
	}
	if displayName, ok := item["display_name"].(string); ok {
		ch.DisplayName = displayName
	}
	return ch, true
}

func mergeSourceChannels(existing, added []*model.Channel) []*model.Channel {
	out := make([]*model.Channel, 0, len(existing)+len(added))
	seen := make(map[string]bool, len(existing)+len(added))
	for _, ch := range append(existing, added...) {
		if ch == nil {
			continue
		}
		key := ch.Id
		if key == "" || seen[key] {
			if key == "" {
				out = append(out, ch)
			}
			continue
		}
		seen[key] = true
		out = append(out, ch)
	}
	return out
}
