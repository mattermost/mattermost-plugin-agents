// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package mcptool holds stable JSON types for Mattermost Agents embedded MCP tools:
// HTTP bodies for before/after tool hooks, and the per-tool output structs embedded
// in after-hook requests as JSON (the "output" field).
//
// Other plugins should import this package—not mcpserver/tools—when unmarshaling hook
// payloads or building typed responses the agents plugin will unmarshal.
//
// Wire compatibility follows the agents plugin release; new fields use omitempty where
// practical so older hook handlers stay tolerant.
package mcptool
