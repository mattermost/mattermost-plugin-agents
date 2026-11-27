// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

// Websocket events the library publishes on its channel. The payload carries
// the entry ("artifact") or, for a delete, its "id".
const (
	EventCreated = "channel_artifact_created"
	EventUpdated = "channel_artifact_updated"
	EventDeleted = "channel_artifact_deleted"
)

// Names of the MCP read tools the built-in Mattermost MCP server offers for
// the library. The tool catalog builder drops them wherever the library does
// not apply.
const (
	ListToolName = "list_channel_artifacts"
	GetToolName  = "get_channel_artifact"
)
